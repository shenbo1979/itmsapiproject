package switchmod

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 本文件的用例回归「Telnet 登录提示判定」：真机用户名提示是 ">>User name:"（含空格、且含 ">"），
// 老口径只认 "username:"/"login:" 会把它误判成"已登录"（见 docs/09-olt-m2.md §5.2 #18）。

// fakeTelnetLoginDevice 逐字节模仿真机（172.20.247.88:23）的登录过程：
// IAC WILL ECHO + IAC WILL SGA + "\r\n>>User name:" → ">>User password:" → MA5800 命令提示符。
type fakeTelnetLoginDevice struct {
	listener net.Listener
	done     chan struct{}
	mu       sync.Mutex
	lines    []string
}

func startFakeTelnetLoginDevice(t *testing.T) *fakeTelnetLoginDevice {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	device := &fakeTelnetLoginDevice{listener: listener, done: make(chan struct{})}
	go device.serve()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-device.done:
		case <-time.After(3 * time.Second):
		}
	})
	return device
}

func (d *fakeTelnetLoginDevice) serve() {
	defer close(d.done)
	conn, err := d.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	write := func(text string) { _, _ = conn.Write([]byte(text)) }
	readLine := func() (string, bool) {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return "", false
		}
		line = strings.TrimRight(line, "\r\n")
		d.mu.Lock()
		d.lines = append(d.lines, line)
		d.mu.Unlock()
		return line, true
	}

	// 真机欢迎信息（20 字节）：IAC WILL ECHO + IAC WILL SGA + CRLF + ">>User name:"
	write("\xff\xfb\x01\xff\xfb\x03\r\n>>User name:")
	if _, ok := readLine(); !ok { // 登录账号
		return
	}
	write("\r\n>>User password:")
	if _, ok := readLine(); !ok { // 口令
		return
	}
	write("\r\n  User last login information:\r\n    Last login IP    : 127.0.0.1\r\nMA5800-X7>\r\n")
	for {
		line, ok := readLine()
		if !ok {
			return
		}
		if line == "" {
			write("\r\nMA5800-X7>\r\n")
			continue
		}
		write("\r\n" + line + "\r\nMA5800-X7>\r\n")
	}
}

func (d *fakeTelnetLoginDevice) hostPort(t *testing.T) (string, int64) {
	t.Helper()
	host, portText, err := net.SplitHostPort(d.listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi(%q): %v", portText, err)
	}
	return host, int64(port)
}

func (d *fakeTelnetLoginDevice) receivedLines() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.lines...)
}

// TestSwitchTelnetLoginHandlesSpacedUserNamedPrompt 回归：用户名提示带空格时，
// 登录必须先发账号、再发口令，并且登录后会话可正常执行命令。
func TestSwitchTelnetLoginHandlesSpacedUserNamedPrompt(t *testing.T) {
	module := newTestModule(t)
	device := startFakeTelnetLoginDevice(t)
	host, port := device.hostPort(t)

	client := &TelnetClient{owner: module, Host: host, Port: port, Maker: "huawei", Timeout: 3 * time.Second}
	if err := client.SwitchConnect("oltuser", "secret"); err != nil {
		t.Fatalf("SwitchConnect: %v", err)
	}
	defer client.Conn.Close()

	if lines := device.receivedLines(); len(lines) < 2 || lines[0] != "oltuser" || lines[1] != "secret" {
		t.Fatalf("登录应先发账号再发口令，实际收到 %#v", lines)
	}
	if err := client.SendCommand("display version"); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	output, err := client.ReadResponse(time.Second)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if !strings.Contains(output, "display version") {
		t.Fatalf("登录后命令回显缺失：%q", output)
	}
}
