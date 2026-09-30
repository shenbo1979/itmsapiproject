// 由 tools/port_switch_batch2c.py 从 switchapi/rpc_client.go 2173-2426 搬运
// （Telnet 客户端：连接、命令发送、分页读取；登录记录与注册校验走模块注入）。
package switchmod

import (
	"fmt"
	"net"
	"strings"
	"time"

	"itmsapiproject/internal/devcli"
)

// telnetDialTimeout 用于建立 Telnet 连接（老实现同名变量，测试可替换）。
var telnetDialTimeout = net.DialTimeout

func (t *TelnetClient) SwitchConnect(username, password string) error {
	// 交换机接口的注册信息验证：未注册/已过期时不允许操作交换机。
	if err := t.owner.requireRegistration(); err != nil {
		return err
	}
	address := t.Host + ":" + fmt.Sprintf("%d", t.Port)
	conn, err := telnetDialTimeout("tcp", address, t.Timeout)
	if err != nil {
		return err
	}
	t.Conn = conn
	// 读取欢迎信息
	welcomemsg, welcomeerr := t.ReadResponse(time.Duration(500) * time.Millisecond)
	if welcomeerr != nil {
		return fmt.Errorf("读取欢迎信息错误;已读取=%q;原因=%v", welcomemsg, welcomeerr)
	}
	// 部分盛科设备首次登录会先询问是否修改默认账号密码。必须先回复 N，
	// 等待设备回到命令提示符后，才能继续发送后续命令。
	lowerWelcome := strings.ToLower(welcomemsg)
	if strings.Contains(lowerWelcome, "change now?") && strings.Contains(lowerWelcome, "[y/n]") {
		if err := t.SendCommand("no"); err != nil {
			return fmt.Errorf("回复默认密码安全提示失败: %v", err)
		}
		promptOutput, err := t.ReadResponse(500 * time.Millisecond)
		if err != nil {
			return fmt.Errorf("回复默认密码安全提示后读取命令提示失败;已读取=%q;原因=%v", welcomemsg, err)
		}
		welcomemsg += promptOutput
		lowerWelcome = strings.ToLower(welcomemsg)
	}
	// 用户名/口令提示的判定统一走 internal/devcli（先做空白归一化再匹配），
	// 与 OLT 连接层同一口径：Huawei OLT 的用户名提示是 ">>User name:"（"User" 与 "name" 之间有空格），
	// 老口径只认 "username:"/"login:" 会漏判（见 docs/09-olt-m2.md §5.2 #18）。
	usernamePrompt := devcli.LooksLikeUsernamePrompt(welcomemsg)
	passwordPrompt := devcli.LooksLikePasswordPrompt(welcomemsg)
	// 免登录设备（部分盛科/华为设备直接进 CLI）：欢迎信息里已经是命令提示符。
	// 关键：必须先把用户名/口令提示排除掉 —— 提示里带 ">"（">>User name:"）时，
	// 老口径会把它误判成"已登录"，随后把命令发到登录提示上（表现为登录成功但所有命令都失败）。
	if !usernamePrompt && !passwordPrompt &&
		(strings.Contains(welcomemsg, "#") || strings.Contains(welcomemsg, ">")) {
		if err := t.prepareSwitchSession(welcomemsg); err != nil {
			return fmt.Errorf("登录后初始化会话失败: %v", err)
		}
		t.owner.recordLoginConnection(t.Host, t.Port, 2, username, password, t.Maker)
		return nil
	}
	if usernamePrompt {
		if err := t.SendCommand(username); err != nil {
			return err
		}
	} else {
		if err := t.SendCommand(""); err != nil {
			return err
		}
	}

	// 读取登录响应
	var loginstr string
	var sessionOutput strings.Builder
	passwordSent := false
	for {
		loginmsg, loginerr := t.ReadResponse(time.Duration(300) * time.Millisecond)
		if loginerr != nil {
			return fmt.Errorf("读取响应错误;阶段=登录;已读取=%q;本次读取=%q;原因=%v", loginstr, loginmsg, loginerr)
		}
		loginstr += loginmsg
		sessionOutput.WriteString(loginmsg)

		// 顺序很关键：先按"提示 → 应答"，再判"提示符结束"，最后才判"登录失败"。
		// 用户名提示必须排在"提示符结束"之前 —— ">>User name:" 含 ">"，否则会被当成登录完成。
		if !passwordSent && devcli.LooksLikePasswordPrompt(loginmsg) {
			if err := t.SendCommand(password); err != nil {
				return err
			}
			passwordSent = true
			continue
		}

		if !passwordSent && devcli.LooksLikeUsernamePrompt(loginmsg) {
			// 设备重新要用户名（首次校验未过）时按老口径重发，直到口令已发出为止。
			if err := t.SendCommand(username); err != nil {
				return err
			}
			continue
		}

		if strings.Contains(loginmsg, "<") || strings.Contains(loginmsg, ">") || strings.Contains(loginmsg, "]") || strings.Contains(loginmsg, "#") {
			break
		}

		// 设备已返回登录失败信息时结束读取，交由下方统一处理（关闭连接并报错）。
		// ReadResponse 对读取超时按空结果处理，若不在此处退出，登录循环会一直阻塞在失效会话上。
		if isTelnetLoginFailure(loginstr) {
			break
		}
	}
	if loginstr == "" || isTelnetLoginFailure(loginstr) {
		// 关闭被拒绝的连接：既避免连接泄漏，也避免调用方继续使用失效会话。
		if t.Conn != nil {
			t.Conn.Close()
			t.Conn = nil
		}
		return fmt.Errorf("操作失败,账号或者密码错误或该账号已经存在连接;")
	}
	if err := t.prepareSwitchSession(loginstr); err != nil {
		return err
	}
	t.owner.recordLoginConnection(t.Host, t.Port, 2, username, password, t.Maker)
	return nil
}

func isTelnetLoginFailure(loginOutput string) bool {
	lower := strings.ToLower(loginOutput)
	failurePatterns := []string{
		"username or password invalid",
		"authentication failed",
		"access denied",
		"login failed",
		"incorrect password",
		"invalid password",
		"error: username or password",
		"reenter times have reached the upper limit",
		"too many users",
		"user interface maximum",
		"connection closed by foreign host",
	}
	for _, pattern := range failurePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func (t *TelnetClient) prepareSwitchSession(initialOutput string) error {
	sessionOutput := initialOutput
	lowerOutput := strings.ToLower(sessionOutput)

	if strings.Contains(lowerOutput, "press y or enter") ||
		strings.Contains(lowerOutput, "press any key") ||
		strings.Contains(lowerOutput, "press enter to continue") {
		if err := t.SendCommand(""); err != nil {
			return fmt.Errorf("发送继续命令失败;已读取=%q;原因=%v", sessionOutput, err)
		}
		nextOutput, err := t.ReadResponse(500 * time.Millisecond)
		if err != nil {
			return fmt.Errorf("读取继续响应失败;已读取=%q;原因=%v", sessionOutput, err)
		}
		sessionOutput += nextOutput
	}

	initCommands := []string{
		"",
		"screen-length disable",
		"screen-length 0 temporary",
	}
	for _, cmd := range initCommands {
		if err := t.SendCommand(cmd); err != nil {
			return err
		}
		resp, err := t.ReadResponse(500 * time.Millisecond)
		if err != nil {
			return err
		}
		sessionOutput += resp
		if strings.Contains(strings.ToLower(sessionOutput), "unrecognized command") ||
			strings.Contains(strings.ToLower(sessionOutput), "incomplete command") {
			continue
		}
	}
	if strings.Contains(sessionOutput, "---- More ----") || strings.Contains(sessionOutput, "--More--") {
		if err := t.SendCommand(" "); err != nil {
			return err
		}
		_, _ = t.ReadResponse(500 * time.Millisecond)
	}
	return nil
}

// 发送命令
func (t *TelnetClient) SendCommand(cmd string) error {
	_, err := t.Conn.Write([]byte(cmd + "\n"))
	return err
}

func telnetIdleReadTimeout(timeout time.Duration) time.Duration {
	const (
		minIdleTimeout = 200 * time.Millisecond
		maxIdleTimeout = 400 * time.Millisecond
	)

	if timeout <= 0 {
		return minIdleTimeout
	}
	if timeout <= minIdleTimeout {
		return timeout
	}

	idleTimeout := timeout / 3
	if idleTimeout < minIdleTimeout {
		idleTimeout = minIdleTimeout
	}
	if idleTimeout > maxIdleTimeout {
		idleTimeout = maxIdleTimeout
	}
	return idleTimeout
}

// 读取响应(带超时)
func (t *TelnetClient) ReadResponse(timeout time.Duration) (string, error) {
	var response strings.Builder
	buffer := make([]byte, 4096)
	idleTimeout := telnetIdleReadTimeout(timeout)
	receivedData := false
	for {
		readTimeout := timeout
		if receivedData {
			readTimeout = idleTimeout
		}
		if err := t.Conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return response.String(), err
		}
		n, err := t.Conn.Read(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break // 静默超时，说明这一轮输出已经结束
			}
			return response.String(), err
		}
		if n > 0 {
			receivedData = true
			response.Write(buffer[:n])
		}
	}
	return response.String(), nil
}

func (t *TelnetClient) ReadPagedResponse(timeout time.Duration, morePatterns []string) (string, error) {
	var response strings.Builder
	for {
		chunk, err := t.ReadResponse(timeout)
		if err != nil {
			return response.String(), err
		}
		if chunk == "" {
			return response.String(), nil
		}

		hasMore := false
		for _, pattern := range morePatterns {
			if strings.Contains(chunk, pattern) {
				hasMore = true
				chunk = strings.ReplaceAll(chunk, pattern, "")
			}
		}
		response.WriteString(chunk)

		if !hasMore {
			return response.String(), nil
		}
		if err := t.SendCommand(" "); err != nil {
			return response.String(), err
		}
	}
}
