package oltconn

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startFakeOlt 起一个最小 OLT 模拟服务：登录提示 → 命令回显 → 一条会分页的命令。
// 用于在无真机时验证「登录流程 + 分页翻页 + 提示符判定」。
func startFakeOlt(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		writer := bufio.NewWriter(conn)
		reader := bufio.NewReader(conn)
		write := func(text string) {
			_, _ = writer.WriteString(text)
			_ = writer.Flush()
		}
		write("Username:")
		_, _ = reader.ReadString('\n') // 用户名
		write("\r\nPassword:")
		_, _ = reader.ReadString('\n') // 口令
		write("\r\nMA5800-X7>\r\n")
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			command := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(command, "screen-length"):
				write(command + "\r\nMA5800-X7>\r\n")
			case command == "display board 0":
				write("  Board Name\r\n---- More ----")            // 第一页停在分页提示
				if _, err := reader.ReadString('\n'); err != nil { // 客户端翻页（空格）
					return
				}
				write("\r\n  0/1  GPBD\r\nMA5800-X7>\r\n")
			case command == "":
				write("\r\nMA5800-X7>\r\n")
			default:
				write(command + "\r\n  ok\r\nMA5800-X7>\r\n")
			}
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

// TestTelnetLoginAndPagedCommand 覆盖：Telnet 登录流程 + 关闭分页 + 分页命令自动翻页。
func TestTelnetLoginAndPagedCommand(t *testing.T) {
	address, stop := startFakeOlt(t)
	defer stop()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, _ := strconv.Atoi(portText)
	params := Params{Type: 2, IP: host, Port: port, Username: "oltuser", Password: "secret", Timeout: 3 * time.Second}

	session, err := Connect(params)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()
	if session.Kind() != KindTelnet {
		t.Fatalf("Kind = %s, want telnet", session.Kind())
	}
	if _, err := RunCommand(session, "screen-length 0 temporary", 3*time.Second); err != nil {
		t.Fatalf("关闭分页命令失败: %v", err)
	}
	output, err := RunCommand(session, "display board 0", 3*time.Second)
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(output, "GPBD") {
		t.Fatalf("分页第二页内容缺失: %q", output)
	}
	if HasPaging(output) {
		t.Fatalf("返回内容仍含分页提示（未翻页）: %q", output)
	}
}

// TestParamsKindSelection 覆盖通道选择口径：
// 新口径 0=TELNET / 1=SSH；老口径 2=TELNET / 3=SSH 兼容；TypeAuto(-1) 按端口推断。
func TestParamsKindSelection(t *testing.T) {
	cases := []struct {
		name   string
		params Params
		want   Kind
	}{
		{"Type=0 → TELNET（新口径默认）", Params{Type: 0, Port: 22}, KindTelnet},
		{"Type=1 → SSH（新口径）", Params{Type: 1, Port: 23}, KindSSH},
		{"Type=2 → TELNET（老口径兼容）", Params{Type: 2, Port: 22}, KindTelnet},
		{"Type=3 → SSH（老口径兼容）", Params{Type: 3, Port: 23}, KindSSH},
		{"未指定（TypeAuto）+ 端口 22 → SSH", Params{Type: TypeAuto, Port: 22}, KindSSH},
		{"未指定（TypeAuto）+ 端口 23 → TELNET", Params{Type: TypeAuto, Port: 23}, KindTelnet},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := item.params.Kind(); got != item.want {
				t.Fatalf("Kind = %s, want %s", got, item.want)
			}
		})
	}
	if got := effectivePort(Params{Type: 1}); got != 22 {
		t.Fatalf("SSH 缺省端口 = %d, want 22", got)
	}
	if got := effectivePort(Params{Type: 0}); got != 23 {
		t.Fatalf("TELNET 缺省端口 = %d, want 23", got)
	}
}

// startHuaweiOlt 模拟现场真机的登录提示（172.20.247.88:23 实测原样）：
// 连接后设备先发 Telnet 协商 IAC WILL ECHO / IAC WILL SGA，再接 "\r\n>>User name:"（合计 20 字节），
// 之后是 ">>User password:"，登录成功给 MA5800 提示符。用于复现/回归「登录后未取得命令提示符（已读 20 字节）」。
func startHuaweiOlt(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		writer := bufio.NewWriter(conn)
		reader := bufio.NewReader(conn)
		write := func(text string) {
			_, _ = writer.WriteString(text)
			_ = writer.Flush()
		}
		// 实测真机欢迎信息（20 字节）：IAC WILL ECHO + IAC WILL SGA + CRLF + ">>User name:"
		_, _ = conn.Write([]byte{0xff, 0xfb, 0x01, 0xff, 0xfb, 0x03})
		write("\r\n>>User name:")
		if _, err := reader.ReadString('\n'); err != nil { // 用户名
			return
		}
		write("\r\n>>User password:")
		if _, err := reader.ReadString('\n'); err != nil { // 口令
			return
		}
		write("\r\n  User last login information:\r\n" +
			"    Last login time  : 2026-09-21 10:00:00\r\n" +
			"    Last login IP    : 10.0.0.1\r\nMA5800-X7>")
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			command := strings.TrimSpace(line)
			switch {
			case command == "":
				write("\r\nMA5800-X7>")
			case strings.HasPrefix(command, "screen-length"), strings.HasPrefix(command, "terminal length"):
				write("\r\nMA5800-X7>")
			case command == "display board 0":
				write("\r\n  ---------\r\n  1       H901GPBD   Normal\r\nMA5800-X7>")
			default:
				write("\r\n" + command + "\r\nMA5800-X7>")
			}
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

// TestLoginHuaweiUserNamedPrompt 是现场故障的回归用例：
// Huawei OLT 的用户名提示是 ">>User name:"（User 与 name 之间有空格），
// 若判定只认 "username"/"login:"，登录流程永远不会发用户名，
// 最终报「登录后未取得命令提示符（已读 20 字节）」（20 = 3 + 3 + 2 + 12）。
func TestLoginHuaweiUserNamedPrompt(t *testing.T) {
	address, stop := startHuaweiOlt(t)
	defer stop()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, _ := strconv.Atoi(portText)
	session, err := Connect(Params{Type: 2, IP: host, Port: port, Username: "oltuser", Password: "secret", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	output, err := RunCommand(session, "display board 0", 3*time.Second)
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(output, "H901GPBD") {
		t.Fatalf("登录后命令回显缺失: %q", output)
	}
}

// startConfigModeOlt 模拟一台登录后停在用户模式（`>`）、必须 enable → config 才能进配置模式的
// 华为 OLT（与真机 MA5603T 一致）。用于回归「缺少配置模式提升会被采集流程卡死」的缺陷。
func startConfigModeOlt(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		writer := bufio.NewWriter(conn)
		reader := bufio.NewReader(conn)
		write := func(text string) {
			_, _ = writer.WriteString(text)
			_ = writer.Flush()
		}
		write("Username:")
		_, _ = reader.ReadString('\n')
		write("\r\nPassword:")
		_, _ = reader.ReadString('\n')
		write("\r\nMA5603T>\r\n") // 登录后停在用户模式
		mode := "user"            // "user" | "priv" | "config"
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			command := strings.TrimSpace(line)
			switch {
			case command == "enable":
				mode = "priv"
				write("\r\nMA5603T#")
			case command == "scroll":
				write("\r\n  { <cr>|number<U><10,512> }:\r\nMA5603T#")
			case command == "config":
				mode = "config"
				write("\r\nMA5603T(config)#")
			case command == "":
				write(promptOLT(mode))
			case command == "display current-configuration":
				if mode != "config" {
					write("\r\n  % Unknown command, the error locates at '^'\r\nMA5603T(config)#")
					continue
				}
				write("\r\n  display current-configuration\r\n  hostname OLT-TEST\r\n  return\r\n" + promptOLT(mode))
			default:
				write("\r\n  ok\r\n" + promptOLT(mode))
			}
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

func promptOLT(mode string) string {
	switch mode {
	case "config":
		return "\r\nMA5603T(config)#"
	case "priv":
		return "\r\nMA5603T#"
	default:
		return "\r\nMA5603T>"
	}
}

// TestEnterConfigModeEscalatesToPrivilegedExec 回归：登录后处于用户模式的华为 OLT，
// 必须经 enable → scroll → "" → config 提升到配置模式，否则 display 命令会得到
// "Unknown command" 并让采集永不结束（线上「刷新配置显示采集中… 再无响应」根因）。
func TestEnterConfigModeEscalatesToPrivilegedExec(t *testing.T) {
	address, stop := startConfigModeOlt(t)
	defer stop()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, _ := strconv.Atoi(portText)

	session, err := Connect(Params{Type: 2, IP: host, Port: port, Username: "oltman", Password: "secret", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	if err := EnterConfigMode(session); err != nil {
		t.Fatalf("EnterConfigMode: %v", err)
	}
	output, err := RunCommand(session, "display current-configuration", 3*time.Second)
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(output, "return") {
		t.Fatalf("配置采集未返回 return: %q", output)
	}
}

// TestEnterConfigModeFailsWithoutSupport 覆盖设备不支持 enable/config（仍停在用户模式）时，
// EnterConfigMode 应 fail-fast 返回错误，而不是把会话放给采集流程去无限空转。
func TestEnterConfigModeFailsWithoutSupport(t *testing.T) {
	address, stop := startFakeOlt(t) // 该假设备对 enable/config 一律回显 MA5800-X7>，永不进入 (config)
	defer stop()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, _ := strconv.Atoi(portText)

	session, err := Connect(Params{Type: 2, IP: host, Port: port, Username: "oltuser", Password: "secret", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	if err := EnterConfigMode(session); err == nil {
		t.Fatal("设备不支持配置模式提升时应返回错误（fail-fast），避免后续采集无限空转")
	}
}
func TestHelpers(t *testing.T) {
	if !LooksLikePrompt("MA5800-X7>") || !LooksLikePrompt("MA5800-X7>>") || !LooksLikePrompt("OLT#") {
		t.Fatal("常见提示符应被识别")
	}
	if LooksLikePrompt("display board 0") {
		t.Fatal("普通回显不应被当成提示符")
	}
	if !HasPaging("...---- More ----") {
		t.Fatal("分页提示应被识别")
	}
	// MA5603T 实测分页提示：`---- More ( Press 'Q' to break ) ----`
	if !HasPaging("X\r\n---- More ( Press 'Q' to break ) ----") {
		t.Fatal("MA5603T 分页提示应被识别")
	}
	if got := stripPaging("a---- More ----b"); got != "ab" {
		t.Fatalf("stripPaging = %q, want ab", got)
	}
	if !LooksLikeLoginFailure("Error: Authentication failed") {
		t.Fatal("登录失败回显应被识别")
	}
	if LooksLikeLoginFailure("display board 0\r\n  ok") {
		t.Fatal("正常回显不应被判为登录失败")
	}
}

// TestIdleReadTimeoutBounds 覆盖方案 A 缩短后的静默窗口边界：
// oltReadTimeout=200ms 时应取 120ms（minIdle）；大超时时夹到 280ms（maxIdle）；
// 极小超时直接返回其自身。用于锁定"连接/刷新提速"不回归。
func TestIdleReadTimeoutBounds(t *testing.T) {
	const oltReadTimeout = 200 * time.Millisecond
	if got := idleReadTimeout(oltReadTimeout); got != 120*time.Millisecond {
		t.Fatalf("oltReadTimeout(%v) 静默窗口 = %v, want 120ms", oltReadTimeout, got)
	}
	if got := idleReadTimeout(5 * time.Second); got != 280*time.Millisecond {
		t.Fatalf("大 timeout 静默窗口 = %v, want 280ms", got)
	}
	if got := idleReadTimeout(50 * time.Millisecond); got != 50*time.Millisecond {
		t.Fatalf("极小 timeout = %v, want 50ms", got)
	}
	if got := idleReadTimeout(0); got != 120*time.Millisecond {
		t.Fatalf("timeout<=0 默认 = %v, want 120ms", got)
	}
}

// readChunkSession 是一次性发送多段回显的假 Session：每次 Read 按顺序吐出一段，
// 用于覆盖 RunCommandUntilSignal 的「分批回显 + 参数提示回车 + 末尾 `#` 结束」逻辑。
type readChunkSession struct {
	chunks []string
	index  int
	sent   []string
	readC  int
}

func (s *readChunkSession) Send(command string) error { s.sent = append(s.sent, command); return nil }
func (s *readChunkSession) Kind() Kind                { return KindTelnet }
func (s *readChunkSession) Close() error              { return nil }
func (s *readChunkSession) ReadPaged(timeout time.Duration) (string, error) {
	return s.Read(timeout)
}
func (s *readChunkSession) Read(timeout time.Duration) (string, error) {
	s.readC++
	for s.index < len(s.chunks) && s.chunks[s.index] == "" {
		s.index++
	}
	var viaSend string
	if s.index < len(s.chunks) {
		viaSend = s.chunks[s.index]
		s.index++
	}
	return viaSend, nil
}

func TestRunCommandUntilSignal(t *testing.T) {
	session := &readChunkSession{chunks: []string{
		"display board 0",  // 命令 echo
		"  ---- More ----", // 分页 → 应发空格翻页
		"  0/1  GPBD",      // 正文
		"MA5603T",          //（塞入提示符前缀）
		"(config)#",        // 末尾 `#` → 结束
	}}
	out, err := RunCommandUntilSignal(session, 5*time.Second)
	if err != nil {
		t.Fatalf("RunCommandUntilSignal: %v", err)
	}
	if !strings.Contains(out, "GPBD") {
		t.Fatalf("回显缺少正文: %q", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "#") {
		t.Fatalf("回显应以 `#` 结尾: %q", out)
	}
	joined := strings.Join(session.sent, "|")
	if !strings.Contains(joined, " ") {
		t.Fatalf("命中分页时应发送空格翻页, sent=%q", joined)
	}
}

func TestRunCommandUntilSignalScrollPrompt(t *testing.T) {
	// display/panel 子命令停在参数选择提示：命中 `{ <cr>` 开头的交互提示 → 应补发一个空行（""）继续。
	// 关键回归：这类提示中间选项不固定（backplane<K>|frameid/slotid<S>…），
	// 早期只匹配 `number<U>` 会漏掉而永久卡住，故这里用非 number 的变体。
	session := &readChunkSession{chunks: []string{
		"display panel", // echo
		"  { <cr>|backplane<K>|frameid/slotid<S><Length 1-15> }:", // 参数选择提示
		"",                 // 空段（由补发的回车触发）
		"MA5603T(config)#", // 结束
	}}
	out, err := RunCommandUntilSignal(session, 5*time.Second)
	if err != nil {
		t.Fatalf("RunCommandUntilSignal(panel): %v", err)
	}
	if !strings.Contains(out, "backplane") {
		t.Fatalf("回显应保留参数提示尾部: %q", out)
	}
	if !hasEmptySent(session.sent) {
		t.Fatalf("命中参数提示补回车时应发空行, sent=%q", session.sent)
	}
}

// TestIsContinuationPrompt 覆盖各种 MA5600 交互式参数选择提示：判定必须基于
// `{ <cr>` 前缀而非某一种中间选项（number<U> / backplane / sof tware ... 各不相同）。
func TestIsContinuationPrompt(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"scroll number 变体", "  { <cr>|number<U><10,512> }:", true},
		{"backplane 变体", "  { <cr>|backplane<K>|frameid/slotid<S><Length 1-15> }:", true},
		{"software/power 变体", "\r\n{ <cr>|sof tware<S>|power<K> }: ", true},
		{"普通回显", "  SlotID  BoardName  Status", false},
		{"普通命令 echo", "display board 0", false},
		{"已在提示符", "MA5603T(config)#", false},
	}
	for _, item := range cases {
		if got := isContinuationPrompt(item.in); got != item.want {
			t.Fatalf("%s: isContinuationPrompt(%q) = %v, want %v", item.name, item.in, got, item.want)
		}
	}
}

func hasEmptySent(sent []string) bool {
	for _, item := range sent {
		if item == "" {
			return true
		}
	}
	return false
}
