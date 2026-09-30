// 由 tools/port_switch_batch2a.py 从 switchapi/handler_switch_terminal.go 整体搬运（SSH 交互终端）
package switchmod

import (
	"bytes"
	"fmt"
	"github.com/google/uuid"
	sshlib "golang.org/x/crypto/ssh"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const switchTerminalIdleTimeout = 30 * time.Minute

type switchTerminalSession struct {
	client    *Client
	session   *sshlib.Session
	stdin     io.WriteCloser
	chunks    chan terminalReadResult
	done      chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
	lastUsed  time.Time
}

type terminalReadResult struct {
	text string
	err  error
}

type switchTerminalRequest struct {
	SessionID string `json:"sessionId"`
	Command   string `json:"command"`
}

var switchTerminalStore = struct {
	sync.Mutex
	sessions map[string]*switchTerminalSession
}{sessions: make(map[string]*switchTerminalSession)}

func (m *Module) handleSwitchTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	nAct, err := strconv.Atoi(r.FormValue("nAct"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
		return
	}
	cleanupExpiredSwitchTerminals()

	switch nAct {
	case 1:
		var req SwitchRequest
		if err := m.decodeEncryptedSwitchRequest(r.FormValue("saveData"), &req); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		if req.Type != 3 {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "仅支持 SSH 终端连接"})
			return
		}
		terminal, initialOutput, err := m.openSwitchTerminal(req)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "SSH 连接失败: " + err.Error()})
			return
		}
		id := uuid.NewString()
		switchTerminalStore.Lock()
		switchTerminalStore.sessions[id] = terminal
		switchTerminalStore.Unlock()
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: map[string]string{"sessionId": id, "output": initialOutput}})
	case 2:
		var request switchTerminalRequest
		if err := m.decodeFormJSON(r, "saveData", &request); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		command := strings.TrimRight(request.Command, "\r\n")
		if request.SessionID == "" || command == "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "sessionId and command are required"})
			return
		}
		if len(command) > 8192 {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "命令长度超过限制"})
			return
		}
		terminal := getSwitchTerminal(request.SessionID)
		if terminal == nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "SSH 会话已关闭或超时"})
			return
		}
		output, err := terminal.run(command)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "SSH 命令执行失败: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: map[string]string{"output": output}})
	case 3:
		var request switchTerminalRequest
		if err := m.decodeFormJSON(r, "saveData", &request); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		closeSwitchTerminal(request.SessionID)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

func (m *Module) openSwitchTerminal(req SwitchRequest) (*switchTerminalSession, string, error) {
	if strings.TrimSpace(req.Hostip) == "" || strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		return nil, "", fmt.Errorf("host, username and password are required")
	}
	if req.Port == 0 {
		req.Port = 22
	}
	client, err := m.connectSwitchSSHClient(req)
	if err != nil {
		return nil, "", err
	}
	session, err := client.newSession()
	if err != nil {
		_ = client.Close()
		return nil, "", err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, "", err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, "", err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, "", err
	}
	if err := session.RequestPty("vt100", 160, 48, sshlib.TerminalModes{}); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, "", err
	}
	if err := session.Shell(); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, "", err
	}

	terminal := &switchTerminalSession{
		client: client, session: session, stdin: stdin,
		chunks: make(chan terminalReadResult, 64), done: make(chan struct{}), lastUsed: time.Now(),
	}
	terminal.forwardOutput(stdout)
	terminal.forwardOutput(stderr)
	initialOutput, err := terminal.readUntilPrompt(2 * time.Second)
	if err != nil {
		terminal.close()
		return nil, "", err
	}
	return terminal, initialOutput, nil
}

func (terminal *switchTerminalSession) forwardOutput(reader io.Reader) {
	go func() {
		buf := make([]byte, 16*1024)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				select {
				case terminal.chunks <- terminalReadResult{text: string(buf[:n])}:
				case <-terminal.done:
					return
				}
			}
			if err != nil {
				select {
				case terminal.chunks <- terminalReadResult{err: err}:
				case <-terminal.done:
				}
				return
			}
		}
	}()
}

func (terminal *switchTerminalSession) run(command string) (string, error) {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if _, err := io.WriteString(terminal.stdin, command+"\n"); err != nil {
		return "", err
	}
	output, err := terminal.readUntilPrompt(15 * time.Second)
	terminal.lastUsed = time.Now()
	return output, err
}

func (terminal *switchTerminalSession) readUntilPrompt(timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var output bytes.Buffer
	for {
		select {
		case result := <-terminal.chunks:
			if result.text != "" {
				for _, marker := range []string{"---- More ----", "--More--", "More"} {
					if strings.Contains(result.text, marker) {
						result.text = strings.ReplaceAll(result.text, marker, "")
						if _, err := io.WriteString(terminal.stdin, " "); err != nil {
							return output.String(), err
						}
					}
				}
				output.WriteString(result.text)
				trimmed := strings.TrimSpace(output.String())
				if strings.HasSuffix(trimmed, ">") || strings.HasSuffix(trimmed, "#") || strings.HasSuffix(trimmed, "]") {
					return output.String(), nil
				}
			}
			if result.err != nil && result.err != io.EOF {
				return output.String(), result.err
			}
			if result.err == io.EOF {
				return output.String(), nil
			}
		case <-timer.C:
			return output.String(), nil
		}
	}
}

func (terminal *switchTerminalSession) close() {
	if terminal == nil {
		return
	}
	terminal.closeOnce.Do(func() {
		close(terminal.done)
		_ = terminal.session.Close()
		_ = terminal.client.Close()
	})
}

func (terminal *switchTerminalSession) isExpired(now time.Time) bool {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	return now.Sub(terminal.lastUsed) > switchTerminalIdleTimeout
}

func getSwitchTerminal(id string) *switchTerminalSession {
	switchTerminalStore.Lock()
	defer switchTerminalStore.Unlock()
	return switchTerminalStore.sessions[id]
}

func closeSwitchTerminal(id string) {
	switchTerminalStore.Lock()
	terminal := switchTerminalStore.sessions[id]
	delete(switchTerminalStore.sessions, id)
	switchTerminalStore.Unlock()
	terminal.close()
}

func cleanupExpiredSwitchTerminals() {
	now := time.Now()
	var expired []*switchTerminalSession
	switchTerminalStore.Lock()
	for id, terminal := range switchTerminalStore.sessions {
		if terminal.isExpired(now) {
			delete(switchTerminalStore.sessions, id)
			expired = append(expired, terminal)
		}
	}
	switchTerminalStore.Unlock()
	for _, terminal := range expired {
		terminal.close()
	}
}
