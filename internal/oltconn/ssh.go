package oltconn

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	sshlib "golang.org/x/crypto/ssh"
)

// sshSession 是 SSH 通道实现：在 PTY 交互 shell 上复用与 Telnet 相同的读写/分页语义。
//
// 为什么用交互 shell 而不是"每条命令一个 exec channel"：
//   - 老实现的业务流程本身依赖同一会话（登录 → 关闭分页 → 连续发命令、需要保持视图上下文）；
//   - OLT 的回显与分页（`---- More ----`）只在交互模式下稳定。
type sshSession struct {
	client  *sshlib.Client
	session *sshlib.Session
	stdin   io.WriteCloser
	reader  *idleReader
	address string
}

// DialSSH 建立 SSH 连接并开好交互 shell（应用层登录仍走 Login）。
func DialSSH(address, username, password string, timeout time.Duration) (Session, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	config := &sshlib.ClientConfig{
		User: username,
		Auth: []sshlib.AuthMethod{
			sshlib.Password(password),
			sshlib.KeyboardInteractive(func(_ string, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for index := range questions {
					answers[index] = password
				}
				return answers, nil
			}),
		},
		// 设备多为自签证书或无证书，内网运维场景不做主机密钥校验（与 switch 模块同口径）。
		HostKeyCallback: sshlib.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}
	client, err := sshlib.Dial("tcp", address, config)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接 %s 失败: %w", address, err)
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	modes := sshlib.TerminalModes{sshlib.ECHO: 0, sshlib.TTY_OP_ISPEED: 115200, sshlib.TTY_OP_OSPEED: 115200}
	if err := session.RequestPty("vt100", 200, 512, modes); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("申请 SSH PTY 失败: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("获取 SSH stdin 失败: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("获取 SSH stdout 失败: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("获取 SSH stderr 失败: %w", err)
	}
	reader := newIdleReader(stdout, stderr)
	if err := session.Shell(); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("启动 SSH shell 失败: %w", err)
	}
	return &sshSession{client: client, session: session, stdin: stdin, reader: reader, address: address}, nil
}

func (s *sshSession) Kind() Kind { return KindSSH }

func (s *sshSession) Send(command string) error {
	if s.stdin == nil {
		return fmt.Errorf("会话已关闭")
	}
	_, err := s.stdin.Write([]byte(command + "\n"))
	return err
}

func (s *sshSession) Read(timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return s.reader.readIdle(timeout, idleReadTimeout(timeout))
}

func (s *sshSession) ReadPaged(timeout time.Duration) (string, error) {
	var builder strings.Builder
	for {
		chunk, err := s.Read(timeout)
		if err != nil {
			return builder.String(), err
		}
		if chunk == "" {
			return builder.String(), nil
		}
		if HasPaging(chunk) {
			builder.WriteString(stripPaging(chunk))
			if err := s.Send(" "); err != nil {
				return builder.String(), err
			}
			continue
		}
		builder.WriteString(chunk)
		return builder.String(), nil
	}
}

func (s *sshSession) Close() error {
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
	if s.session != nil {
		_ = s.session.Close()
		s.session = nil
	}
	if s.client != nil {
		err := s.client.Close()
		s.client = nil
		return err
	}
	return nil
}

// idleReader 把 SSH 的 stdout/stderr 合并进一个带信号量的缓冲，供 readIdle 按"静默窗口"取用。
type idleReader struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	signal chan struct{}
	closed bool
	err    error
}

func newIdleReader(pipes ...io.Reader) *idleReader {
	reader := &idleReader{signal: make(chan struct{}, 1)}
	for _, pipe := range pipes {
		go reader.consume(pipe)
	}
	return reader
}

func (r *idleReader) consume(pipe io.Reader) {
	buffer := make([]byte, 4096)
	for {
		n, err := pipe.Read(buffer)
		if n > 0 {
			r.mu.Lock()
			r.buffer.Write(buffer[:n])
			r.mu.Unlock()
			r.notify()
		}
		if err != nil {
			r.mu.Lock()
			if r.err == nil {
				r.err = err
			}
			r.closed = true
			r.mu.Unlock()
			r.notify()
			return
		}
	}
}

func (r *idleReader) notify() {
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

// readIdle 收集输出：还没有任何输出时最多等 timeout；一旦有输出，再用 idle 静默窗口判定结束。
func (r *idleReader) readIdle(timeout, idle time.Duration) (string, error) {
	var builder strings.Builder
	deadline := time.Now().Add(timeout)
	for {
		wait := idle
		if builder.Len() == 0 {
			wait = time.Until(deadline)
		}
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		if wait <= 0 {
			return builder.String(), nil
		}
		select {
		case <-r.signal:
			r.mu.Lock()
			builder.WriteString(r.buffer.String())
			r.buffer.Reset()
			closed, readErr := r.closed, r.err
			r.mu.Unlock()
			if closed && readErr != nil && builder.Len() == 0 {
				// 通道已关闭且没有任何输出：返回错误（上层据此报"会话已结束"）
				if readErr == io.EOF {
					return "", fmt.Errorf("SSH 会话已结束（未读到输出）")
				}
				return "", readErr
			}
		case <-time.After(wait):
			if builder.Len() > 0 {
				return builder.String(), nil // 静默窗口内无新数据：本轮结束
			}
		}
	}
}
