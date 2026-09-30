package oltconn

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"itmsapiproject/internal/devcli"
)

// Dial 按 Params 建立会话（Telnet / SSH 自动选择，见 Params.Kind）。
func Dial(params Params) (Session, error) {
	host := strings.TrimSpace(params.IP)
	if host == "" {
		return nil, fmt.Errorf("缺少设备地址（IP）")
	}
	address := net.JoinHostPort(host, strconv.Itoa(effectivePort(params))) // IPv6 安全（老实现用字符串拼接）
	if params.Kind() == KindSSH {
		return DialSSH(address, params.Username, params.Password, params.effectiveTimeout())
	}
	return DialTelnet(address, params.effectiveTimeout())
}

// Connect 建立会话并完成登录（老实现 connectOltTelnet 的等价物，并按需支持 SSH）。
func Connect(params Params) (Session, error) {
	session, err := Dial(params)
	if err != nil {
		return nil, err
	}
	if err := Login(session, params); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

// Login 完成应用层登录（Telnet 与 SSH 同一流程，兼容免登录设备）：
// 出现 Username/login 提示 → 发用户名；出现 Password 提示 → 发口令；直到出现命令提示符。
func Login(session Session, params Params) error {
	host := describeParams(params)
	timeout := DefaultLoginTimeout
	output, err := session.Read(timeout)
	if err != nil && strings.TrimSpace(output) == "" {
		return fmt.Errorf("读取 %s 欢迎信息失败: %w", host, err)
	}
	usernameSent, passwordSent := false, false
	if devcli.LooksLikeUsernamePrompt(output) {
		if err := session.Send(params.Username); err != nil {
			return fmt.Errorf("发送用户名失败: %w", err)
		}
		usernameSent = true
	}

	deadline := time.Now().Add(4 * timeout)
	for time.Now().Before(deadline) {
		chunk, readErr := session.Read(timeout)
		output += chunk
		if LooksLikeLoginFailure(output) {
			return fmt.Errorf("%s 登录失败：账号或密码不正确", host)
		}
		if !usernameSent && devcli.LooksLikeUsernamePrompt(output) {
			if err := session.Send(params.Username); err != nil {
				return fmt.Errorf("发送用户名失败: %w", err)
			}
			usernameSent = true
			continue
		}
		if !passwordSent && devcli.LooksLikePasswordPrompt(output) {
			if err := session.Send(params.Password); err != nil {
				return fmt.Errorf("发送口令失败: %w", err)
			}
			passwordSent = true
			continue
		}
		// 免登录设备：欢迎信息里直接就是命令提示符
		if !usernameSent && LooksLikePrompt(chunk) {
			return nil
		}
		if usernameSent && LooksLikePrompt(chunk) {
			return nil
		}
		if readErr != nil && strings.TrimSpace(chunk) == "" {
			break
		}
	}
	return fmt.Errorf("%s 登录后未取得命令提示符（已读 %d 字节）", host, len(output))
}

// RunCommand 发送命令并读取回显（自动翻页）；等价于老实现的 readOLTCommandUntil。
func RunCommand(session Session, command string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("命令不能为空")
	}
	if err := session.Send(command); err != nil {
		return "", err
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return session.ReadPaged(timeout)
}

// commandEndMarker 是 MA5603T 配置模式命令回显的结束标记：完整回显以 `MA5603T(config)#`
// 收尾（末尾是 `#`）。因此判定「一条只读命令回显读完」用「累计回显以 `#` 结尾」。
const commandEndMarker = "#"

// continuationPromptRe 匹配 MA5600 的交互式参数选择提示，例如：
//
//	{ <cr>|number<U><10,512> }            （scroll 模式参数）
//	{ <cr>|backplane<K>|frameid/slotid<S><Length 1-15> }   （部分 display/panel 子命令）
//	{ <cr>|sof tware<S>|power<K> } etc.
//
// 这类提示都以 `{ <cr>` 开头并以 `}:`/`}` 结尾，含义是"设备在等输入"。因为各子命令
// 中间选项五花八门（<cr>|number<K>|... 各不同），所以**不能只认某一种**（早期只匹配
// `number<U>` 导致 scroll 之外、如 display panel 的提示匹配不上而卡死）。凡命中这类
// 提示都应当补发一个回车（空行）让命令继续执行/退出到 `#`。
//
// 归约：只要整段中出现过 `{ <cr>` 开头、且其后（到行尾）是参数选择提示（以 `}`/`}:` 收尾），
// 即视为设备在等输入。因为循环里「回到 `#`」判定在最前——能走到这里说明**尚未回到提示符**，
// 此时补一个回车把提示推开是安全且必须的（最多让设备再弹一次同样的提示，不会有害）。
//
// 不用「末 N 字节截断」：提示本身可超过 64 字节（如
// `{ <cr>|backplane<K>|frameid/slotid<S><Length 1-15> }:`），截断会把 `{ <cr>` 前缀丢掉而误判。
func isContinuationPrompt(out string) bool {
	idx := strings.LastIndex(out, "{ <cr>")
	if idx < 0 {
		return false
	}
	rest := out[idx:]
	// 提示可能带前导空格被换行包裹；取该行去掉两侧空白，看是否以 `}` 或 `}:` 结尾。
	line := rest
	if nl := strings.IndexAny(rest, "\r\n"); nl >= 0 {
		line = rest[:nl]
	}
	line = strings.TrimSpace(line)
	return strings.HasSuffix(line, "}") || strings.HasSuffix(line, "}:")
}

// teletypeHelp 补发回车，推开 MA5600 交互式参数选择提示（{ <cr|... }: 或 scroll 参数提示）。
// 老实现 readOLTCommandUntil 在解释到 `{:}` 时 Send("") 实现同样动作。
func pushContinuation(session Session) error {
	return session.Send("")
}

// RunCommandUntilSignal 以「设备回显回到命令提示符（末尾 `#`）」作为一条命令读取的结束标志，
// 替代此前「静默超时窗口」判定。
//
// 背景（docs/09 真机阶段）：MA5603T 把回显分批下发，段间停顿常超 200ms
// （`oltReadTimeout=200ms` 随之采用 120ms 静默窗口），导致 ReadPaged 提前把命令读断在开头
// （实测 `display board 0` 只拿到 7 字节 "display"）。改用「回到 `config)#`（末尾 `#`）」
// 作为命令完成，回显内容必然完整，与设备分段上报无关。
//
// 规则：
//   - 每周期（500ms）轮询读取；命中分页提示发空格翻页；停在参数提示（如 scroll）补回车；
//   - 累计回显以 `#` 结尾即判定命令完成；
//   - 整体不超过 maxWait，超时返回已读内容与错误。
func RunCommandUntilSignal(session Session, maxWait time.Duration) (string, error) {
	if session == nil {
		return "", fmt.Errorf("会话未初始化")
	}
	if maxWait <= 0 {
		maxWait = DefaultTimeout
	}
	const cycle = 500 * time.Millisecond
	var sb strings.Builder
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		chunk, err := session.Read(cycle)
		if len(chunk) > 0 {
			sb.WriteString(chunk)
		}
		accum := sb.String()
		// 回到提示符（末尾 `#`）→ 命令结束。注意：必须放在分页/参数判定之前，
		// 否则回显中混入过 `----`/`<cr>` 会因 HasPaging/continuation 恒真而永远到不了结束判定。
		if strings.HasSuffix(strings.TrimRight(accum, " \t\r\n"), commandEndMarker) {
			return sb.String(), nil
		}
		// 命中交互式参数选择提示（{ <cr>|... } 开头）→ 补回车继续执行
		if isContinuationPrompt(accum) {
			if err := pushContinuation(session); err != nil {
				return sb.String(), err
			}
			continue
		}
		// 命中分页提示 → 发空格翻页
		if HasPaging(chunk) || HasPaging(accum) {
			if err := session.Send(" "); err != nil {
				return sb.String(), err
			}
			continue
		}
		// 尚未结束：即使静默超时无新数据也继续等，不因静默提前收尾
		_ = err
	}
	return sb.String(), fmt.Errorf("读取命令回显超时（已读 %d 字节，未回到提示符）", sb.Len())
}

func DisablePaging(session Session) {
	candidates := []string{
		"screen-length 0 temporary", // Huawei / MA5800
		"screen-length disable",
		"terminal length 0", // 兼容部分设备
	}
	for _, command := range candidates {
		output, err := RunCommand(session, command, 3*time.Second)
		if err != nil {
			continue
		}
		lower := strings.ToLower(output)
		if strings.Contains(lower, "unrecognized") || strings.Contains(lower, "invalid") ||
			strings.Contains(lower, "incomplete") || strings.Contains(lower, "unknown") {
			continue
		}
		return
	}
}

// EnterConfigMode 在已登录会话上进入系统视图（配置模式），与老实现 connectOltTelnet 的 Connect()
// 尾部一致：enable → scroll → 空行 → config，直到回显出现 `(config)`。
//
// 背景：Huawei OLT 登录后默认停在用户模式（`MA5603T>`），`display current-configuration`、
// `display board` 等只有特权/配置模式才可执行；若停留在用户模式直接发这些命令会得到
// "Unknown command"，且 `collectSavedConfiguration` 的结束判定（出现独立行 `return`）永远
// 不满足，从而在提示符回显上无限空转。
func EnterConfigMode(session Session) error {
	if session == nil {
		return fmt.Errorf("会话未初始化")
	}
	steps := []struct {
		command string
		wait    time.Duration
	}{
		{"enable", 500 * time.Millisecond},
		{"scroll", 500 * time.Millisecond},
		{"", 300 * time.Millisecond},
		{"config", 500 * time.Millisecond},
	}
	for _, step := range steps {
		if err := session.Send(step.command); err != nil {
			return err
		}
		if reachedConfigPrompt(session, step.wait) {
			return nil
		}
	}
	return fmt.Errorf("登录后未能进入配置模式（设备未回显 (config) 提示符）")
}

// reachedConfigPrompt 在 wait 时长内轮询读取回显，命中 `(config)` 返回 true。
func reachedConfigPrompt(session Session, wait time.Duration) bool {
	if wait <= 0 {
		wait = 500 * time.Millisecond
	}
	deadline := time.Now().Add(wait)
	var out strings.Builder
	for time.Now().Before(deadline) {
		chunk, err := session.Read(wait)
		out.WriteString(chunk)
		if strings.Contains(out.String(), "(config)") {
			return true
		}
		if err != nil && len(chunk) == 0 {
			// 静默超时且无新数据：本步无更多输出
			return strings.Contains(out.String(), "(config)")
		}
	}
	return strings.Contains(out.String(), "(config)")
}

// effectivePort 返回实际连接端口：未指定时 SSH=22、Telnet=23（老实现默认 Telnet 23）。
func effectivePort(params Params) int {
	if params.Port > 0 {
		return params.Port
	}
	if params.Kind() == KindSSH {
		return 22
	}
	return 23
}

// DisablePaging 尽力关闭分页（各厂商命令不同，失败不影响后续命令——ReadPaged 会兜底翻页）。
