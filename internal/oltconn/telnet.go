package oltconn

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// telnetSession 是 Telnet 通道实现（老实现 TelnetClient 的等价物；去掉了包级全局与交换机专用逻辑）。
type telnetSession struct {
	conn    net.Conn
	address string
}

// DialTelnet 建立 Telnet 连接（只建链路，不登录；登录见 Login）。
func DialTelnet(address string, timeout time.Duration) (Session, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, fmt.Errorf("Telnet 连接 %s 失败: %w", address, err)
	}
	return &telnetSession{conn: conn, address: address}, nil
}

func (s *telnetSession) Kind() Kind { return KindTelnet }

// Send 发送一条命令（补换行；与老实现 TelnetClient.SendCommand 一致）。
func (s *telnetSession) Send(command string) error {
	if s.conn == nil {
		return fmt.Errorf("会话已关闭")
	}
	_, err := s.conn.Write([]byte(command + "\n"))
	return err
}

// Read 读取一轮输出（与 switch 模块 TelnetClient.ReadResponse 同策略）：
// 首次读取用 timeout，一旦收到数据就改用较短的静默窗口判定"本轮输出结束"，
// 从而既不会话未说完就返回，也不会在静默设备上一直挂住。
func (s *telnetSession) Read(timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	var builder strings.Builder
	buffer := make([]byte, 4096)
	received := false
	for {
		readTimeout := timeout
		if received {
			readTimeout = idleReadTimeout(timeout)
		}
		if err := s.conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return builder.String(), err
		}
		n, err := s.conn.Read(buffer)
		if n > 0 {
			received = true
			builder.Write(buffer[:n])
		}
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break // 静默超时：本轮输出结束
			}
			return builder.String(), err
		}
	}
	return builder.String(), nil
}

// ReadPaged 读取可能分页的输出：命中分页提示则发送空格翻页，直到不再出现分页提示。
func (s *telnetSession) ReadPaged(timeout time.Duration) (string, error) {
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

func (s *telnetSession) Close() error {
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// idleReadTimeout 计算"本轮输出结束"的静默窗口（120~280ms，夹在 timeout/3）。
// minIdle 从 200ms 降到 120ms：oltReadTimeout=200ms 时窗口由 200→120ms，每条命令收尾省 80ms，
// 显著缩短 OLT 多命令串行采集（连接/刷新）的总耗时（仅等"设备说尽"，不减少采集内容）。
func idleReadTimeout(timeout time.Duration) time.Duration {
	const (
		minIdle = 120 * time.Millisecond
		maxIdle = 280 * time.Millisecond
	)
	if timeout <= 0 {
		return minIdle
	}
	if timeout <= minIdle {
		return timeout
	}
	idle := timeout / 3
	if idle < minIdle {
		idle = minIdle
	}
	if idle > maxIdle {
		idle = maxIdle
	}
	return idle
}
