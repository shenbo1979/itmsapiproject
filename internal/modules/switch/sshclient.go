// 由 tools/port_switch_batch2.py 从 switchapi/switchdb.go 的 811-末尾（SSH 客户端 + 厂商解析） 段搬运
// （package 改名、包级全局改为注入：注册校验走 m.requireRegistration、登录记录走 m.recordLoginConnection）。
package switchmod

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	sshlib "golang.org/x/crypto/ssh"
)

func (m *Module) Connect(host string, port int64, username, password string, kexAlgorithms []string) (*Client, error) {
	return m.ConnectWithOptions(host, port, username, password, kexAlgorithms)
}

func (m *Module) ConnectWithLoginInfo(host string, port int64, username, password string, kexAlgorithms []string, loginInfo SwitchLoginInfo) (*Client, error) {
	// 交换机接口的注册信息验证：未注册/已过期时不允许操作交换机。
	if err := m.requireRegistration(); err != nil {
		return nil, err
	}
	client, err := m.ConnectWithOptions(host, port, username, password, kexAlgorithms)
	if err != nil {
		return nil, err
	}
	m.recordLoginConnection(host, port, 3, loginInfo.Username, loginInfo.Password, loginInfo.Maker)
	return client, nil
}

func buildSSHKeyExchanges(kexAlgorithms []string) []string {
	defaults := []string{
		"mlkem768x25519-sha256",
		"curve25519-sha256",
		"curve25519-sha256@libssh.org",
		"ecdh-sha2-nistp256",
		"ecdh-sha2-nistp384",
		"ecdh-sha2-nistp521",
		"diffie-hellman-group-exchange-sha256",
		"diffie-hellman-group14-sha256",
		"diffie-hellman-group14-sha1",
	}
	seen := make(map[string]struct{}, len(defaults)+len(kexAlgorithms))
	var merged []string
	for _, item := range kexAlgorithms {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		merged = append(merged, item)
	}
	for _, item := range defaults {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

func (m *Module) ConnectWithOptions(host string, port int64, username, password string, kexAlgorithms []string) (*Client, error) {
	keyboardInteractive := sshlib.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range questions {
			answers[i] = password
		}
		return answers, nil
	})

	config := &sshlib.ClientConfig{
		User:            username,
		Auth:            []sshlib.AuthMethod{sshlib.Password(password), keyboardInteractive},
		HostKeyCallback: sshlib.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
		Config: sshlib.Config{
			KeyExchanges: buildSSHKeyExchanges(kexAlgorithms),
		},
	}

	// 老实现用 fmt.Sprintf("%s:%d") 拼地址，IPv6 会拼成 ::1:22 而失败（go vet 也报该问题）。
	// 这里用 net.JoinHostPort：IPv4 结果与老实现一致，同时支持文档里承诺的 IPv6 设备。
	addr := net.JoinHostPort(host, strconv.FormatInt(port, 10))
	tcpConn, err := net.DialTimeout("tcp", addr, config.Timeout)
	if err != nil {
		m.logf("SSH阶段TCP连接失败 target=%s error=%v", addr, err)
		return nil, err
	}
	if err := tcpConn.SetDeadline(time.Now().Add(config.Timeout)); err != nil {
		_ = tcpConn.Close()
		return nil, err
	}
	conn, channels, requests, err := sshlib.NewClientConn(tcpConn, addr, config)
	if err != nil {
		m.logf("SSH阶段握手或认证失败 target=%s local=%s error=%v", addr, tcpConn.LocalAddr(), err)
		_ = tcpConn.Close()
		return nil, err
	}
	if err := tcpConn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	clientConn := sshlib.NewClient(conn, channels, requests)

	return &Client{
		owner:         m,
		conn:          clientConn,
		host:          host,
		port:          port,
		username:      username,
		password:      password,
		kexAlgorithms: append([]string(nil), kexAlgorithms...),
	}, nil
}

func (c *Client) RunScript(script string) (string, error) {
	session, err := c.newSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	output, err := session.CombinedOutput(script)
	if err != nil && strings.Contains(err.Error(), "without exit status or exit signal") {
		err = nil
	}
	return string(output), err
}

func (c *Client) RunInteractive(commands []string) (string, error) {
	var options InteractiveOptions
	options.PreCommands = []string{"screen-length 0 temporary"}
	options.MorePatterns = []string{"---- More ----", "--More--", "More"}
	options.PromptSuffixes = []string{">", "#", "]"}
	options.CommandDelay = 1200
	options.ReadTimeout = 1500 * time.Millisecond
	options.AutoExit = true
	return c.RunInteractiveWithOptions(commands, options)
}

// RunDeviceCollectionInteractive runs read-only inventory commands with a
// shorter send-to-read delay.  The prompt-driven read still waits for the
// device to finish each command, while avoiding the fixed 1.2s pause that is
// appropriate for configuration commands but costly for multi-command polls.
func (c *Client) RunDeviceCollectionInteractive(commands []string) (string, error) {
	return c.RunInteractiveWithOptions(commands, InteractiveOptions{
		PreCommands:    []string{"screen-length 0 temporary"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		PromptSuffixes: []string{">", "#", "]"},
		CommandDelay:   250 * time.Millisecond,
		ReadTimeout:    1500 * time.Millisecond,
		// Inventory commands must remain on one Shell. Some Huawei devices close
		// the entire SSH transport after receiving "quit" from the first session.
		AutoExit: false,
	})
}

func (c *Client) RunInteractiveWithOptions(commands []string, options InteractiveOptions) (string, error) {
	if len(options.MorePatterns) == 0 {
		options.MorePatterns = []string{"---- More ----", "--More--", "More"}
	}
	if len(options.PromptSuffixes) == 0 {
		options.PromptSuffixes = []string{">", "#", "]"}
	}
	if options.CommandDelay == 0 {
		options.CommandDelay = 1200
	}
	if options.ReadTimeout == 0 {
		options.ReadTimeout = 1500 * time.Millisecond
	}
	if strings.TrimSpace(options.ExitCommand) == "" {
		options.ExitCommand = "quit"
	}
	session, err := c.newSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return "", err
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", err
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		return "", err
	}

	if err := session.RequestPty("vt100", 512, 200, sshlib.TerminalModes{}); err != nil {
		return "", err
	}
	if err := session.Shell(); err != nil {
		return "", err
	}

	type readResult struct {
		text string
		err  error
	}
	readResults := make(chan readResult, 32)
	readDone := make(chan struct{})
	defer close(readDone)
	forwardOutput := func(reader io.Reader) {
		go func() {
			buf := make([]byte, 10240)
			for {
				n, readErr := reader.Read(buf)
				if n > 0 {
					chunk := string(buf[:n])
					select {
					case readResults <- readResult{text: chunk}:
					case <-readDone:
						return
					}
				}
				if readErr != nil {
					select {
					case readResults <- readResult{err: readErr}:
					case <-readDone:
					}
					return
				}
			}
		}()
	}
	forwardOutput(stdout)
	forwardOutput(stderr)
	activeReaders := 2

	readUntilPrompt := func() (string, error) {
		var output bytes.Buffer
		timer := time.NewTimer(options.ReadTimeout)
		defer timer.Stop()
		resetTimer := func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(options.ReadTimeout)
		}
		for {
			select {
			case result := <-readResults:
				if result.text != "" {
					resetTimer()
					chunk := result.text
					for _, pattern := range options.MorePatterns {
						if strings.Contains(chunk, pattern) {
							chunk = strings.ReplaceAll(chunk, pattern, "")
							if _, werr := io.WriteString(stdin, " "); werr != nil {
								return output.String(), werr
							}
						}
					}
					output.WriteString(chunk)
					trimmed := strings.TrimSpace(output.String())
					// Treat confirmation prompts as intermediate output so the caller can answer.
					if hasInteractiveConfirmationPrompt(trimmed) {
						return output.String(), nil
					}
					for _, suffix := range options.PromptSuffixes {
						if strings.HasSuffix(trimmed, suffix) {
							return output.String(), nil
						}
					}
				}
				if result.err != nil {
					activeReaders--
					if result.err != io.EOF {
						return output.String(), result.err
					}
					if activeReaders == 0 {
						return output.String(), nil
					}
				}
			case <-timer.C:
				// A quiet device must not leave the request waiting indefinitely.
				return output.String(), nil
			}
		}
	}

	var output bytes.Buffer
	time.Sleep(options.CommandDelay)
	initialOutput, err := readUntilPrompt()
	if err != nil {
		return output.String(), err
	}
	output.WriteString(initialOutput)

	runOne := func(cmd string) error {
		if _, err := io.WriteString(stdin, cmd+"\n"); err != nil {
			return err
		}
		time.Sleep(options.CommandDelay)
		cmdOutput, err := readUntilPrompt()
		if err != nil {
			output.WriteString(cmdOutput)
			return err
		}
		output.WriteString(cmdOutput)
		if options.StopOnErrorOutput {
			if detail := matchCommandFailureOutput(cmd, cmdOutput); detail != "" {
				return fmt.Errorf("command %q: %s", cmd, detail)
			}
		}
		if options.AutoConfirm && hasInteractiveConfirmationPrompt(cmdOutput) {
			if _, err := io.WriteString(stdin, "y\n"); err != nil {
				return err
			}
			time.Sleep(options.CommandDelay)
			confirmationOutput, err := readUntilPrompt()
			output.WriteString(confirmationOutput)
			if err != nil {
				return err
			}
			if options.StopOnErrorOutput {
				if detail := matchCommandFailureOutput(cmd, confirmationOutput); detail != "" {
					return fmt.Errorf("command %q: %s", cmd, detail)
				}
			}
		}
		return nil
	}

	for _, cmd := range options.PreCommands {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		if err := runOne(cmd); err != nil {
			return output.String(), err
		}
	}

	for _, cmd := range commands {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		if err := runOne(cmd); err != nil {
			return output.String(), err
		}
	}
	if options.WaitAfterCommands > 0 {
		time.Sleep(options.WaitAfterCommands)
	}

	if options.AutoExit {
		if err := runOne(options.ExitCommand); err != nil && !strings.Contains(err.Error(), "EOF") {
			return output.String(), err
		}
		waitResult := make(chan error, 1)
		go func() {
			waitResult <- session.Wait()
		}()
		select {
		case err := <-waitResult:
			if err != nil && !strings.Contains(err.Error(), "without exit status or exit signal") && !strings.Contains(err.Error(), "EOF") {
				return output.String(), err
			}
		case <-time.After(options.ReadTimeout):
			// Some switch shells return a prompt instead of closing the SSH session.
			// The deferred Close releases the session without blocking the request.
		}
	}
	return output.String(), nil
}

func matchCommandFailureOutput(command, output string) string {
	failurePattern := regexp.MustCompile(`(?i)\b(?:unknown|failed)\b`)
	zeroFailurePattern := regexp.MustCompile(`(?i)\bfailed\s*:\s*0\b`)
	command = strings.ToLower(strings.Join(strings.Fields(command), " "))
	for _, raw := range strings.Split(normalizeCLIOutput(output), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !failurePattern.MatchString(line) || zeroFailurePattern.MatchString(line) {
			continue
		}
		normalizedLine := strings.ToLower(strings.Join(strings.Fields(line), " "))
		if isInteractiveCommandEcho(normalizedLine, command) {
			continue
		}
		return line
	}
	return ""
}

func isInteractiveCommandEcho(line, command string) bool {
	if command == "" {
		return false
	}
	if line == command {
		return true
	}
	for _, marker := range []string{">", "#", "]"} {
		if index := strings.LastIndex(line, marker); index >= 0 && strings.TrimSpace(line[index+len(marker):]) == command {
			return true
		}
	}
	return false
}

func switchInteractiveExitCommandForMaker(maker string) string {
	if GetSwitchMaker(maker) == "ruijie" {
		return "exit"
	}
	return "quit"
}

func hasInteractiveConfirmationPrompt(output string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(output, " ", ""))
	return strings.Contains(normalized, "[Y/N]") || strings.Contains(normalized, "[Y/N]:")
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) newSession() (*sshlib.Session, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("ssh connection is not initialized")
	}
	session, err := c.conn.NewSession()
	if err == nil {
		return session, nil
	}
	if !shouldRetrySSHSession(err) {
		return nil, err
	}
	if reconnectErr := c.reconnect(); reconnectErr != nil {
		return nil, fmt.Errorf("%v; reconnect failed: %w", err, reconnectErr)
	}
	session, retryErr := c.conn.NewSession()
	if retryErr != nil {
		return nil, retryErr
	}
	return session, nil
}

func (c *Client) reconnect() error {
	if c == nil {
		return fmt.Errorf("ssh client is nil")
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	if c.owner == nil {
		return fmt.Errorf("ssh client has no owner module")
	}
	newClient, err := c.owner.ConnectWithOptions(c.host, c.port, c.username, c.password, c.kexAlgorithms)
	if err != nil {
		return err
	}
	c.conn = newClient.conn
	return nil
}

func shouldRetrySSHSession(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	retryPatterns := []string{
		"unexpected packet in response to channel open",
		"ssh: rejected: connect failed",
		"channel open failed",
		"administratively prohibited",
	}
	for _, pattern := range retryPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func normalizeCLIOutput(data string) string {
	replacer := strings.NewReplacer(
		"\r", "",
		"\x00", "",
		"\b", "",
	)
	return replacer.Replace(data)
}

// ParseHuaweiVersionInfo 从 display version 输出中提取华为交换机版本信息
// 华为 / H3C 解析模块：负责从版本、光模块、接口和路由表输出中抽取结构化字段。
func ParseHuaweiVersionInfo(data string) map[string]string {
	result := make(map[string]string)
	versionPattern := regexp.MustCompile(`\bV\d+R\d+C\d+(?:SPC\d+)?(?:B\d+)?\b`)
	comwareVersionPattern := regexp.MustCompile(`\b\d+\.\d+\.\d+,\s*Release\s+[A-Za-z0-9]+\b`)
	data = normalizeCLIOutput(data)
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if strings.Contains(line, "VRP (R) software, Version") {
			result["softwareVersionLine"] = line
			if matched := versionPattern.FindString(line); matched != "" {
				result["softwareVersion"] = matched
			} else {
				result["softwareVersion"] = line
			}
			if start := strings.Index(line, "("); start >= 0 {
				if end := strings.Index(line[start:], ")"); end > 0 {
					meta := strings.TrimSpace(line[start+1 : start+end])
					fields := strings.Fields(meta)
					if len(fields) > 0 && result["deviceModel"] == "" && !strings.HasPrefix(fields[0], "V") {
						result["deviceModel"] = fields[0]
					}
				}
			}
		} else if strings.Contains(line, "Comware Software, Version") {
			result["softwareVersionLine"] = line
			if matched := comwareVersionPattern.FindString(line); matched != "" {
				result["softwareVersion"] = matched
			} else if idx := strings.Index(line, "Version"); idx >= 0 {
				result["softwareVersion"] = strings.TrimSpace(strings.TrimPrefix(line[idx:], "Version"))
			} else {
				result["softwareVersion"] = line
			}
		} else if strings.Contains(line, "H3C") && strings.Contains(line, "uptime is") {
			parts := strings.SplitN(line, "uptime is", 2)
			if len(parts) == 2 {
				deviceName := strings.TrimSpace(parts[0])
				result["deviceName"] = deviceName
				result["uptime"] = strings.TrimSpace(parts[1])
				deviceType := strings.TrimSpace(strings.TrimPrefix(deviceName, "H3C"))
				if deviceType != "" {
					result["deviceType"] = deviceType
					result["deviceModel"] = deviceType
				}
			}
		} else if strings.HasPrefix(line, "H3C ") && result["deviceName"] == "" {
			result["deviceName"] = line
			deviceType := strings.TrimSpace(strings.TrimPrefix(line, "H3C"))
			if deviceType != "" {
				result["deviceType"] = deviceType
				result["deviceModel"] = deviceType
			}
		} else if strings.HasPrefix(line, "BOARD TYPE:") {
			deviceType := strings.TrimSpace(strings.TrimPrefix(line, "BOARD TYPE:"))
			if deviceType != "" {
				result["deviceType"] = deviceType
				result["deviceModel"] = deviceType
				if result["deviceName"] == "" {
					result["deviceName"] = "H3C " + deviceType
				}
			}
		} else if strings.Contains(line, "with") && strings.Contains(line, "Processor") && strings.HasPrefix(line, "S") {
			fields := strings.Fields(line)
			if len(fields) > 0 && result["deviceModel"] == "" {
				result["deviceType"] = fields[0]
				result["deviceModel"] = fields[0]
				if result["deviceName"] == "" {
					result["deviceName"] = "H3C " + fields[0]
				}
			}
		} else if strings.Contains(line, "HUAWEI") && strings.Contains(line, "uptime is") {
			parts := strings.SplitN(line, "uptime is", 2)
			if len(parts) == 2 {
				deviceName := strings.TrimSpace(parts[0])
				result["deviceName"] = deviceName
				result["uptime"] = strings.TrimSpace(parts[1])
				nameFields := strings.Fields(deviceName)
				if len(nameFields) >= 2 {
					result["deviceType"] = nameFields[len(nameFields)-1]
					result["deviceModel"] = nameFields[len(nameFields)-1]
				}
			}
		} else if strings.HasPrefix(line, "VRP Version") {
			result["vrpVersion"] = strings.TrimSpace(strings.TrimPrefix(line, "VRP Version"))
		} else if strings.Contains(line, "the device board version is") {
			result["boardVersion"] = strings.TrimSpace(line)
		} else if strings.Contains(line, "Patch Version") {
			idx := strings.Index(line, ":")
			if idx > 0 {
				result["patchVersion"] = strings.TrimSpace(line[idx+1:])
			}
		}
	}
	if result["deviceName"] == "" && result["deviceModel"] != "" {
		if strings.Contains(data, "H3C") || strings.Contains(data, "Comware Software") {
			result["deviceName"] = "H3C " + result["deviceModel"]
		} else {
			result["deviceName"] = "HUAWEI " + result["deviceModel"]
		}
	}
	return result
}

// TODO 华为交换机
var metricFloatPattern = regexp.MustCompile(`[-+]?\d+(?:\.\d+)?`)

func parseMetricFloat(line string) (float64, bool) {
	raw := line
	if idx := strings.LastIndex(raw, ":"); idx >= 0 {
		raw = raw[idx+1:]
	}
	raw = strings.TrimSpace(raw)
	match := metricFloatPattern.FindString(raw)
	if match == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ParseHuaweiTransceiverInfo 从 output 中提取华为光模块温度、电压、电流及收发光功率。
func ParseHuaweiTransceiverInfo(data string) map[string]TransceiverInfo {
	data = normalizeCLIOutput(data)
	result := make(map[string]TransceiverInfo)
	lines := strings.Split(data, "\n")
	var cur *TransceiverInfo
	absentPattern := regexp.MustCompile(`Info:\s+Port\s+(\S+),\s+transceiver is absent\.`)
	inDiagnosticSection := false
	flush := func() {
		if cur != nil {
			if cur.TransceiverDiagnostic == "" {
				cur.TransceiverDiagnostic = "unknown"
			}
			result[cur.Port] = *cur
			cur = nil
		}
		inDiagnosticSection = false
	}
	for _, val := range lines {
		line := strings.TrimSpace(val)
		if len(line) == 0 {
			continue
		}
		if matches := absentPattern.FindStringSubmatch(line); len(matches) == 2 {
			flush()
			port := normalizeSwitchPortName(matches[1])
			result[port] = TransceiverInfo{
				Port:                  port,
				TransceiverDiagnostic: "absent",
			}
			continue
		}
		// 新的端口诊断块起始行
		if strings.Contains(line, "transceiver diagnostic information") {
			flush()
			port := strings.Fields(line)[0]
			cur = &TransceiverInfo{Port: normalizeSwitchPortName(port), TransceiverDiagnostic: "unsupported"}
			inDiagnosticSection = true
			continue
		}
		if strings.Contains(line, " transceiver information:") {
			flush()
			port := strings.TrimSpace(strings.TrimSuffix(strings.SplitN(line, " transceiver information:", 2)[0], ":"))
			cur = &TransceiverInfo{Port: normalizeSwitchPortName(port), TransceiverDiagnostic: "unsupported"}
			continue
		}
		if strings.HasPrefix(line, "display interface ") && strings.Contains(line, " transceiver verbose") {
			flush()
			rest := strings.TrimPrefix(line, "display interface ")
			rest = strings.TrimSuffix(rest, " transceiver verbose")
			port := normalizeSwitchPortName(strings.ToUpper(strings.ReplaceAll(rest, " ", "")))
			cur = &TransceiverInfo{Port: port, TransceiverDiagnostic: "unsupported"}
			continue
		}
		if strings.HasPrefix(line, "display transceiver verbose ") {
			flush()
			rest := strings.TrimPrefix(line, "display transceiver verbose ")
			port := normalizeSwitchPortName(strings.ToUpper(strings.ReplaceAll(rest, " ", "")))
			cur = &TransceiverInfo{Port: port, TransceiverDiagnostic: "unsupported"}
			continue
		}
		if cur == nil {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "transceiver type"):
			if idx := strings.Index(line, ":"); idx >= 0 {
				cur.ModuleType = strings.TrimSpace(line[idx+1:])
			}
			continue
		case strings.Contains(lower, "wave length"), strings.Contains(lower, "wavelength"):
			if idx := strings.Index(line, ":"); idx >= 0 {
				cur.Wavelength = strings.TrimSpace(line[idx+1:])
			}
			continue
		}
		if strings.Contains(lower, "does not support diagnostic information") {
			cur.TransceiverDiagnostic = "unsupported"
			continue
		}
		if strings.Contains(lower, "diagnostic information") {
			inDiagnosticSection = true
			continue
		}
		if strings.HasPrefix(line, "---") {
			inDiagnosticSection = false
			continue
		}
		if !inDiagnosticSection && strings.Contains(line, "transceiver diagnostic information") {
			inDiagnosticSection = true
		}
		if !inDiagnosticSection {
			continue
		}
		switch {
		case strings.Contains(lower, "temperature"):
			if v, ok := parseMetricFloat(line); ok {
				cur.TransceiverDiagnostic = "available"
				cur.Temperature = v
			}
		case strings.Contains(lower, "voltage"):
			if v, ok := parseMetricFloat(line); ok {
				cur.TransceiverDiagnostic = "available"
				cur.Voltage = v
			}
		case strings.Contains(lower, "bias current") && !strings.Contains(lower, "threshold"):
			if v, ok := parseMetricFloat(line); ok {
				cur.TransceiverDiagnostic = "available"
				cur.Current = v
			}
		case strings.Contains(lower, "rx power") &&
			!strings.Contains(lower, "warning") &&
			!strings.Contains(lower, "threshold"):
			if v, ok := parseMetricFloat(line); ok {
				cur.TransceiverDiagnostic = "available"
				cur.RxPower = v
			}
		case strings.Contains(lower, "tx power") &&
			!strings.Contains(lower, "warning") &&
			!strings.Contains(lower, "threshold"):
			if v, ok := parseMetricFloat(line); ok {
				cur.TransceiverDiagnostic = "available"
				cur.TxPower = v
			}
		}
	}
	flush()
	return result
}

func ParseHuaweiInterfacePorts(data string) []string {
	data = normalizeCLIOutput(data)
	portMap := make(map[string]struct{})
	lines := strings.Split(data, "\n")
	for _, val := range lines {
		line := strings.TrimSpace(val)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		port := normalizeSwitchPortName(fields[0])
		if !strings.Contains(port, "/") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(port), "vlanif") || strings.HasPrefix(strings.ToLower(port), "null") {
			continue
		}
		portMap[port] = struct{}{}
	}
	ports := make([]string, 0, len(portMap))
	for port := range portMap {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	return ports
}

// ParseHuaweiPortVlan 从 display port vlan 输出中提取每个端口的 VLAN 映射信息。
// 表头格式为：Port / Link Type / PVID / Trunk VLAN List / Port Description。
// 端口描述过长时会换行续接到下一行（无端口列），需拼接还原。
func ParseHuaweiPortVlan(data string) map[string]SwitchVlanMapping {
	data = normalizeCLIOutput(data)
	result := make(map[string]SwitchVlanMapping)
	lines := strings.Split(data, "\n")
	inSection := false
	lastPort := ""
	portPattern := regexp.MustCompile(`^(?:[0-9A-Za-z-]+)?[0-9A-Za-z-]*/[0-9A-Za-z-]+/[0-9A-Za-z-]+$|^Eth-Trunk\d+$|^40GE\d+/\d+/\d+$|^100GE\d+/\d+/\d+$|^25GE\d+/\d+/\d+$|^10GE\d+/\d+/\d+$`)
	for _, val := range lines {
		// 定位 display port vlan 表头
		if strings.Contains(val, "Link Type") && strings.Contains(val, "PVID") {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		// 遇到设备提示符或空行，结束该段落
		if strings.HasPrefix(strings.TrimSpace(val), "<") {
			break
		}
		raw := strings.TrimSpace(val)
		if len(raw) == 0 || strings.HasPrefix(raw, "---") {
			continue
		}
		lowerRaw := strings.ToLower(raw)
		if strings.HasPrefix(lowerRaw, "display ") || strings.HasPrefix(lowerRaw, "route flags:") || strings.HasPrefix(lowerRaw, "routing tables:") || strings.HasPrefix(lowerRaw, "destination/mask") {
			break
		}
		parts := strings.Fields(raw)
		// 数据行：端口 链路类型 PVID(整数) Trunk列表 [描述...]
		if len(parts) >= 4 {
			port := normalizeSwitchPortName(parts[0])
			if portPattern.MatchString(port) {
				if pvid, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
					m := SwitchVlanMapping{
						Port:          port,
						LinkType:      parts[1],
						PVID:          pvid,
						TrunkVLANList: parts[3],
					}
					if len(parts) >= 5 {
						m.PortDescription = strings.Join(parts[4:], " ")
					}
					result[m.Port] = m
					lastPort = m.Port
					continue
				}
			}
		}
		// 某些长描述换行场景，首列为空，只续接上一端口描述
		if lastPort != "" {
			m := result[lastPort]
			if m.PortDescription != "" {
				m.PortDescription += raw
			} else {
				m.PortDescription = raw
			}
			result[lastPort] = m
		}
	}
	return result
}

// 分析HUAWEI(华为)交换机信息
func ParseHuaweiInterfaceInfo(data string) []SwitchInterfaceInfo {
	data = normalizeCLIOutput(data)
	transceiverMap := ParseHuaweiTransceiverInfo(data)
	vlanMap := ParseHuaweiPortVlan(data)
	var results []SwitchInterfaceInfo
	lines := strings.Split(data, "\n")
	inBriefSection := false
	for _, val := range lines {
		line := strings.TrimSpace(val)
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "display interface brief"):
			inBriefSection = true
			continue
		case inBriefSection && strings.HasPrefix(lower, "display "):
			inBriefSection = false
			continue
		case inBriefSection && strings.HasPrefix(line, "<"):
			inBriefSection = false
			continue
		}
		if !inBriefSection {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 || !isHuaweiPhysicalInterfaceLine(parts[0]) {
			continue
		}
		info := SwitchInterfaceInfo{}
		info.Port = normalizeSwitchPortName(parts[0])
		info.Status = strings.ToLower(parts[1])
		if isHuaweiBriefStatus(parts[2:]) {
			info.Mode = parts[2]
		} else {
			if len(parts) >= 3 {
				info.Speed = parts[2]
			}
			if len(parts) >= 4 {
				info.Duplex = parts[3]
			}
			if len(parts) >= 5 {
				info.Type = parts[4]
			}
		}
		if t, ok := transceiverMap[info.Port]; ok {
			info.TransceiverDiagnostic = t.TransceiverDiagnostic
			info.ModuleType = t.ModuleType
			info.Wavelength = t.Wavelength
			info.Temperature = t.Temperature
			info.Voltage = t.Voltage
			info.Current = t.Current
			info.TxPower = t.TxPower
			info.RxPower = t.RxPower
		} else {
			info.TransceiverDiagnostic = "unknown"
		}
		if v, ok := vlanMap[info.Port]; ok {
			info.VlanMapping = v
		}
		results = append(results, info)
	}
	return results
}

func isHuaweiPhysicalInterfaceLine(port string) bool {
	port = normalizeSwitchPortName(port)
	lower := strings.ToLower(port)
	if strings.HasPrefix(lower, "vlanif") || strings.HasPrefix(lower, "null") {
		return false
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^(?:10GE|25GE|40GE|100GE)\d+/\d+/\d+$`),
		regexp.MustCompile(`^(?:XGigabitEthernet|GigabitEthernet|Ethernet|MEth)\d+/\d+/\d+$`),
		regexp.MustCompile(`^Eth-Trunk\d+$`),
	}
	for _, pattern := range patterns {
		if pattern.MatchString(port) {
			return true
		}
	}
	return false
}

func ParseRoutingTables(data string) []RoutingTable {
	data = normalizeCLIOutput(data)
	lines := strings.Split(data, "\n")
	inRoutingTable := false
	inHeader := false
	var currentDestination string
	var currentPrefixLength string
	var currentMask string
	routes := make([]RoutingTable, 0)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "display ip routing-table"):
			inRoutingTable = true
			inHeader = false
			continue
		case strings.Contains(lower, "routing tables:"):
			inRoutingTable = true
			inHeader = false
			continue
		case inRoutingTable && strings.HasPrefix(lower, "display ") && !strings.Contains(lower, "display ip routing-table"):
			inRoutingTable = false
			inHeader = false
			continue
		case inRoutingTable && strings.HasPrefix(line, "<"):
			continue
		}
		if strings.Contains(lower, "destination/mask") || (strings.Contains(lower, "destination") && strings.Contains(lower, "nexthop")) {
			inRoutingTable = true
			inHeader = true
			continue
		}
		if !inRoutingTable {
			continue
		}
		if !inHeader || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "=") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if !looksLikeRoutingEntry(fields, currentDestination) {
			continue
		}
		route := buildRoutingTableEntry(fields, currentDestination, currentPrefixLength, currentMask)
		if route.Destination == "" || route.NextHop == "" {
			continue
		}
		if route.Destination != "" {
			currentDestination = route.Destination
			currentPrefixLength = route.PrefixLength
			currentMask = route.Mask
		}
		if route.ManagementGateway == "" && isCandidateManagementNextHop(route.NextHop) {
			route.ManagementGateway = route.NextHop
		}
		routes = append(routes, route)
	}
	return routes
}

func ParseRoutingTable(data string) RoutingTable {
	routes := ParseRoutingTables(data)
	localNextHops := collectLocalRouteNextHops(strings.Split(normalizeCLIOutput(data), "\n"))
	var fallback RoutingTable
	for _, route := range routes {
		if isDefaultRoute(route) && isUsableManagementRoute(route, localNextHops) {
			return route
		}
		if fallback.ManagementGateway == "" && isUsableManagementRoute(route, localNextHops) {
			fallback = route
		}
	}
	return fallback
}

func buildRoutingTableEntry(fields []string, currentDestination, currentPrefixLength, currentMask string) RoutingTable {
	if len(fields) < 5 {
		return RoutingTable{}
	}

	nextHop := fields[len(fields)-2]
	iface := fields[len(fields)-1]
	destination := fields[0]

	if isRoutingContinuationLine(fields) && currentDestination != "" {
		return buildRoutingContinuationEntry(fields, currentDestination, currentPrefixLength, currentMask, nextHop, iface)
	}

	route := RoutingTable{
		Destination: destination,
		Protocol:    fields[1],
		Preference:  fields[2],
		Cost:        fields[3],
		NextHop:     nextHop,
		Interface:   iface,
	}
	if strings.Contains(route.Destination, "/") {
		parts := strings.SplitN(route.Destination, "/", 2)
		route.Destination = parts[0]
		route.PrefixLength = parts[1]
	} else if len(fields) >= 7 && strings.Count(route.Destination, ".") == 3 && strings.Count(fields[1], ".") == 3 {
		route.Mask = fields[1]
		route.Protocol = fields[2]
		route.Preference = fields[3]
		route.Cost = fields[4]
		route.NextHop = nextHop
		route.Interface = iface
	}
	return route
}

func looksLikeRoutingEntry(fields []string, currentDestination string) bool {
	if len(fields) < 5 {
		return false
	}
	first := strings.TrimSpace(fields[0])
	if first == "" {
		return false
	}
	if strings.Contains(first, "/") {
		parts := strings.SplitN(first, "/", 2)
		return isIPv4Like(parts[0]) && parts[1] != ""
	}
	if isIPv4Like(first) {
		return true
	}
	return currentDestination != "" && isRoutingProtocol(first)
}

func isRoutingContinuationLine(fields []string) bool {
	if len(fields) < 5 {
		return false
	}
	if strings.Contains(fields[0], "/") || strings.Count(fields[0], ".") == 3 {
		return false
	}
	return isRoutingProtocol(fields[0])
}

func buildRoutingContinuationEntry(fields []string, currentDestination, currentPrefixLength, currentMask, nextHop, iface string) RoutingTable {
	route := RoutingTable{
		Destination:  currentDestination,
		Mask:         currentMask,
		PrefixLength: currentPrefixLength,
		Protocol:     fields[0],
		Preference:   fields[1],
		Cost:         fields[2],
		NextHop:      nextHop,
		Interface:    iface,
	}
	return route
}

func isRoutingProtocol(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "direct", "static", "ospf", "ospfase", "rip", "bgp", "isis", "unr", "relay":
		return true
	default:
		return false
	}
}

func isIPv4Like(raw string) bool {
	return strings.Count(raw, ".") == 3
}

func isHuaweiBriefStatus(fields []string) bool {
	if len(fields) < 5 {
		return false
	}
	protocol := strings.ToLower(fields[0])
	return protocol == "up" || strings.Contains(protocol, "down")
}

func collectLocalRouteNextHops(lines []string) map[string]bool {
	result := make(map[string]bool)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.Contains(fields[0], "/") {
			continue
		}
		if !strings.EqualFold(fields[1], "Direct") {
			continue
		}
		parts := strings.SplitN(fields[0], "/", 2)
		if len(parts) != 2 || parts[1] == "32" {
			continue
		}
		nextHop := fields[len(fields)-2]
		if nextHop != "" && nextHop != "127.0.0.1" {
			result[nextHop] = true
		}
	}
	return result
}

func isDefaultRoute(route RoutingTable) bool {
	if route.Destination != "0.0.0.0" {
		return false
	}
	return route.PrefixLength == "0" || route.Mask == "0.0.0.0" || route.Mask == ""
}

func isCandidateManagementNextHop(nextHop string) bool {
	if nextHop == "" || nextHop == "0.0.0.0" || nextHop == "127.0.0.1" {
		return false
	}
	return true
}

func isUsableManagementRoute(route RoutingTable, localNextHops map[string]bool) bool {
	if !isCandidateManagementNextHop(route.NextHop) {
		return false
	}
	if localNextHops[route.NextHop] {
		return false
	}
	return !strings.EqualFold(route.Protocol, "Direct")
}

func normalizeSwitchPortName(port string) string {
	port = strings.TrimSpace(port)
	port = strings.ReplaceAll(port, " ", "")
	return port
}

func normalizeH3CPortName(port string) string {
	port = normalizeSwitchPortName(port)
	if port == "" {
		return ""
	}

	aliasPatterns := []struct {
		pattern *regexp.Regexp
		target  string
	}{
		{regexp.MustCompile(`^XGE(\d+/\d+/\d+)$`), `Ten-GigabitEthernet$1`},
		{regexp.MustCompile(`^10GE(\d+/\d+/\d+)$`), `Ten-GigabitEthernet$1`},
		{regexp.MustCompile(`^FGE(\d+/\d+/\d+)$`), `FortyGigE$1`},
		{regexp.MustCompile(`^GE(\d+/\d+/\d+)$`), `GigabitEthernet$1`},
		{regexp.MustCompile(`^MGE(\d+/\d+/\d+)$`), `M-GigabitEthernet$1`},
		{regexp.MustCompile(`^40GE(\d+/\d+/\d+)$`), `FortyGigE$1`},
		{regexp.MustCompile(`^100GE(\d+/\d+/\d+)$`), `HundredGigE$1`},
		{regexp.MustCompile(`^25GE(\d+/\d+/\d+)$`), `Twenty-FiveGigE$1`},
		{regexp.MustCompile(`^BAGG(\d+)$`), `Bridge-Aggregation$1`},
	}
	for _, item := range aliasPatterns {
		if item.pattern.MatchString(port) {
			return item.pattern.ReplaceAllString(port, item.target)
		}
	}
	return port
}

func isH3CInterfacePort(port string) bool {
	port = normalizeH3CPortName(port)
	lower := strings.ToLower(port)
	switch {
	case strings.HasPrefix(lower, "m-gigabitethernet"),
		strings.HasPrefix(lower, "gigabitethernet"),
		strings.HasPrefix(lower, "ten-gigabitethernet"),
		strings.HasPrefix(lower, "fortygige"),
		strings.HasPrefix(lower, "hundredgige"),
		strings.HasPrefix(lower, "twenty-fivegige"),
		strings.HasPrefix(lower, "bridge-aggregation"):
		return true
	default:
		return false
	}
}

func h3cPortKey(port string) string {
	port = normalizeH3CPortName(port)
	if port == "" {
		return ""
	}
	match := regexp.MustCompile(`(\d+/\d+/\d+)$`).FindStringSubmatch(port)
	if len(match) == 2 {
		return match[1]
	}
	return port
}

func hasTransceiverMetrics(info TransceiverInfo) bool {
	return info.Temperature != 0 || info.Voltage != 0 || info.Current != 0 || info.TxPower != 0 || info.RxPower != 0
}

func cleanH3CVLANList(raw string) string {
	raw = strings.ReplaceAll(raw, "\b", "")
	raw = strings.ReplaceAll(raw, "\r", "")
	raw = strings.ReplaceAll(raw, "\x00", "")
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "<"); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	if idx := strings.Index(raw, "["); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	raw = strings.TrimSpace(strings.TrimRight(raw, ","))
	return raw
}

func parseH3CInterfaceBrief(data string) []SwitchInterfaceInfo {
	data = normalizeCLIOutput(data)
	lines := strings.Split(data, "\n")
	var results []SwitchInterfaceInfo
	inBridgeSection := false
	inRouteSection := false

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "display "):
			inRouteSection = false
			inBridgeSection = false
			continue
		case strings.HasPrefix(lower, "brief information on interfaces in route mode"):
			inRouteSection = true
			inBridgeSection = false
			continue
		case strings.HasPrefix(lower, "brief information on interfaces in bridge mode"):
			inBridgeSection = true
			inRouteSection = false
			continue
		case strings.HasPrefix(lower, "link:") || strings.HasPrefix(lower, "protocol:") || strings.HasPrefix(lower, "speed:") || strings.HasPrefix(lower, "duplex:"):
			continue
		case strings.HasPrefix(lower, "interface "):
			continue
		case strings.HasPrefix(line, "<") || strings.HasPrefix(line, "["):
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rawPort := normalizeSwitchPortName(fields[0])
		if !isH3CInterfacePort(rawPort) {
			continue
		}
		port := normalizeH3CPortName(rawPort)

		info := SwitchInterfaceInfo{Port: rawPort, Description: port}
		if inBridgeSection {
			if len(fields) < 6 {
				continue
			}
			if len(fields) >= 2 {
				info.Status = strings.ToLower(fields[1])
			}
			if len(fields) >= 3 {
				info.Speed = fields[2]
			}
			if len(fields) >= 4 {
				info.Duplex = fields[3]
			}
			if len(fields) >= 5 {
				info.Mode = fields[4]
			}
			if len(fields) >= 6 {
				if pvid, err := strconv.ParseInt(fields[5], 10, 64); err == nil {
					info.VlanMapping = SwitchVlanMapping{
						Port: rawPort,
						PVID: pvid,
					}
					switch strings.ToUpper(info.Mode) {
					case "A":
						info.VlanMapping.LinkType = "access"
					case "T":
						info.VlanMapping.LinkType = "trunk"
					case "H":
						info.VlanMapping.LinkType = "hybrid"
					}
				}
			}
			if len(fields) > 6 {
				info.Description = strings.Join(fields[6:], " ")
			}
			results = append(results, info)
			continue
		}

		if inRouteSection {
			if len(fields) >= 2 {
				info.Status = strings.ToLower(fields[1])
			}
			if len(fields) > 5 {
				info.Description = strings.Join(fields[5:], " ")
			}
			results = append(results, info)
		}
	}

	return results
}

// ParseH3CTransceiverInfo 从 H3C display transceiver diagnosis 输出中提取光模块信息。
// H3C 解析模块：处理 H3C 端口、光模块和 VLAN 输出的兼容格式。
func ParseH3CTransceiverInfo(data string) map[string]TransceiverInfo {
	data = normalizeCLIOutput(data)
	result := make(map[string]TransceiverInfo)
	briefPortByKey := make(map[string]string)
	for _, info := range parseH3CInterfaceBrief(data) {
		if info.Port == "" {
			continue
		}
		briefPortByKey[h3cPortKey(info.Port)] = info.Port
	}
	lines := strings.Split(data, "\n")
	var cur *TransceiverInfo
	pendingCurrentMetrics := false

	flush := func() {
		if cur != nil && cur.Port != "" {
			if rawPort, ok := briefPortByKey[h3cPortKey(cur.Port)]; ok {
				cur.Port = rawPort
			}
			result[cur.Port] = *cur
			cur = nil
		}
		pendingCurrentMetrics = false
	}
	for _, val := range lines {
		line := strings.TrimSpace(val)
		if line == "" {
			continue
		}
		switch {
		case strings.Contains(line, "transceiver diagnostic information"):
			flush()
			port := strings.TrimSpace(strings.TrimSuffix(strings.SplitN(line, "transceiver diagnostic information", 2)[0], ":"))
			cur = &TransceiverInfo{Port: normalizeH3CPortName(port)}
			continue
		case strings.Contains(line, "Diagnostic parameters for transceiver at interface"):
			flush()
			parts := strings.SplitN(line, "interface", 2)
			if len(parts) == 2 {
				port := strings.TrimSpace(strings.TrimSuffix(parts[1], ":"))
				cur = &TransceiverInfo{Port: normalizeH3CPortName(port)}
				continue
			}
		}
		if cur == nil {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "current diagnostic parameters") {
			pendingCurrentMetrics = true
			values := metricFloatPattern.FindAllString(line, -1)
			if len(values) >= 5 {
				if v, err := strconv.ParseFloat(values[0], 64); err == nil {
					cur.Temperature = v
				}
				if v, err := strconv.ParseFloat(values[1], 64); err == nil {
					cur.Voltage = v
				}
				if v, err := strconv.ParseFloat(values[2], 64); err == nil {
					cur.Current = v
				}
				if v, err := strconv.ParseFloat(values[3], 64); err == nil {
					cur.RxPower = v
				}
				if v, err := strconv.ParseFloat(values[4], 64); err == nil {
					cur.TxPower = v
				}
				pendingCurrentMetrics = false
			}
			continue
		}
		if pendingCurrentMetrics {
			if strings.Contains(lower, "alarm thresholds") || strings.Contains(lower, "warning thresholds") {
				pendingCurrentMetrics = false
				continue
			}
			if strings.HasPrefix(line, "---") {
				continue
			}
			values := metricFloatPattern.FindAllString(line, -1)
			if len(values) >= 5 {
				if v, err := strconv.ParseFloat(values[0], 64); err == nil {
					cur.Temperature = v
				}
				if v, err := strconv.ParseFloat(values[1], 64); err == nil {
					cur.Voltage = v
				}
				if v, err := strconv.ParseFloat(values[2], 64); err == nil {
					cur.Current = v
				}
				if v, err := strconv.ParseFloat(values[3], 64); err == nil {
					cur.RxPower = v
				}
				if v, err := strconv.ParseFloat(values[4], 64); err == nil {
					cur.TxPower = v
				}
				pendingCurrentMetrics = false
			}
			continue
		}
		if strings.Contains(lower, "threshold") {
			continue
		}
		switch {
		case strings.Contains(lower, "temperature"):
			if v, ok := parseMetricFloat(line); ok {
				cur.Temperature = v
			}
		case strings.Contains(lower, "transceiver type"):
			if idx := strings.Index(line, ":"); idx >= 0 {
				cur.ModuleType = strings.TrimSpace(line[idx+1:])
			}
		case strings.Contains(lower, "wave length"), strings.Contains(lower, "wavelength"):
			if idx := strings.Index(line, ":"); idx >= 0 {
				cur.Wavelength = strings.TrimSpace(line[idx+1:])
			}
		case strings.Contains(lower, "voltage"):
			if v, ok := parseMetricFloat(line); ok {
				cur.Voltage = v
			}
		case strings.Contains(lower, "bias current"):
			if v, ok := parseMetricFloat(line); ok {
				cur.Current = v
			}
		case strings.Contains(lower, "tx power"):
			if v, ok := parseMetricFloat(line); ok {
				cur.TxPower = v
			}
		case strings.Contains(lower, "rx power"):
			if v, ok := parseMetricFloat(line); ok {
				cur.RxPower = v
			}
		}
	}
	flush()
	return result
}

// ParseH3CPortVlan 从 H3C display port 输出中提取 trunk/hybrid 端口的 VLAN 信息。
func ParseH3CPortVlan(data string) map[string]SwitchVlanMapping {
	data = normalizeCLIOutput(data)
	result := make(map[string]SwitchVlanMapping)
	lines := strings.Split(data, "\n")
	var currentLinkType string
	var currentPort string
	for _, val := range lines {
		line := strings.TrimSpace(val)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "display port"):
			continue
		case strings.HasPrefix(lower, "display "):
			currentPort = ""
			currentLinkType = ""
			continue
		case strings.HasPrefix(lower, "routing tables:"),
			strings.HasPrefix(lower, "destinations :"),
			strings.HasPrefix(lower, "destination/mask"):
			currentPort = ""
			currentLinkType = ""
			continue
		case strings.HasPrefix(line, "Interface") && strings.Contains(line, "PVID"):
			continue
		case strings.HasPrefix(line, "---"):
			continue
		case strings.Contains(lower, "hybrid") && strings.Contains(lower, "vlan passing"):
			currentLinkType = "hybrid"
			currentPort = ""
			continue
		case strings.Contains(lower, "trunk") && strings.Contains(lower, "vlan passing"):
			currentLinkType = "trunk"
			currentPort = ""
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 2 && isH3CInterfacePort(fields[0]) {
			if pvid, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				rawPort := normalizeSwitchPortName(fields[0])
				currentPort = normalizeH3CPortName(rawPort)
				trunkList := strings.TrimSpace(strings.Join(fields[2:], " "))
				trunkList = strings.TrimSpace(strings.TrimPrefix(trunkList, "Tagged:"))
				trunkList = strings.TrimSpace(strings.TrimPrefix(trunkList, "Untagged:"))
				trunkList = cleanH3CVLANList(trunkList)
				result[currentPort] = SwitchVlanMapping{
					Port:          rawPort,
					LinkType:      currentLinkType,
					PVID:          pvid,
					TrunkVLANList: trunkList,
				}
				continue
			}
		}
		if currentPort != "" {
			m := result[currentPort]
			trimmed := strings.TrimSpace(strings.TrimPrefix(line, "Tagged:"))
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "Untagged:"))
			trimmed = cleanH3CVLANList(trimmed)
			if trimmed != "" {
				if m.TrunkVLANList != "" {
					m.TrunkVLANList += " "
				}
				m.TrunkVLANList += trimmed
				result[currentPort] = m
			}
		}
	}
	return result
}

// ParseH3CInterfaceDetailInfo parses the per-interface output used by H3C
// models that do not support display port trunk.  That output contains the
// PVID, link type, tagged/untagged VLANs, and the interface description.
func ParseH3CInterfaceDetailInfo(data string) map[string]SwitchInterfaceInfo {
	data = normalizeCLIOutput(data)
	result := make(map[string]SwitchInterfaceInfo)
	lines := strings.Split(data, "\n")
	var current *SwitchInterfaceInfo
	inVLANList := false

	flush := func() {
		if current == nil || current.Port == "" {
			return
		}
		if current.VlanMapping.Port == "" {
			current.VlanMapping.Port = current.Port
		}
		result[normalizeH3CPortName(current.Port)] = *current
		current = nil
		inVLANList = false
	}
	appendVLANs := func(raw string) {
		if current == nil {
			return
		}
		raw = cleanH3CVLANList(raw)
		if raw == "" || strings.EqualFold(raw, "none") {
			return
		}
		if current.VlanMapping.TrunkVLANList != "" {
			current.VlanMapping.TrunkVLANList += " "
		}
		current.VlanMapping.TrunkVLANList += raw
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "display interface ") {
			continue
		}
		if isH3CInterfacePort(line) {
			flush()
			current = &SwitchInterfaceInfo{Port: normalizeSwitchPortName(line)}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(lower, "current state:"):
			current.Status = strings.ToLower(strings.TrimSpace(line[len("Current state:"):]))
			inVLANList = false
		case strings.HasPrefix(lower, "description:"):
			current.Description = strings.TrimSpace(line[len("Description:"):])
			current.VlanMapping.PortDescription = current.Description
			inVLANList = false
		case strings.HasPrefix(lower, "pvid:"):
			if pvid, err := strconv.ParseInt(strings.TrimSpace(line[len("PVID:"):]), 10, 64); err == nil {
				current.VlanMapping.PVID = pvid
			}
			inVLANList = false
		case strings.HasPrefix(lower, "port link-type:"):
			current.VlanMapping.LinkType = strings.ToLower(strings.TrimSpace(line[len("Port link-type:"):]))
			inVLANList = false
		case strings.HasPrefix(lower, "tagged vlans:"):
			appendVLANs(strings.TrimSpace(line[len("Tagged VLANs:"):]))
			inVLANList = true
		case strings.HasPrefix(lower, "untagged vlans:"):
			appendVLANs(strings.TrimSpace(line[len("Untagged VLANs:"):]))
			inVLANList = true
		default:
			if inVLANList && !strings.Contains(line, ":") {
				appendVLANs(line)
			} else {
				inVLANList = false
			}
		}
	}
	flush()
	return result
}

// ParseH3CInterfaceInfo 从 H3C display interface brief 和 display transceiver diagnosis interface 输出中提取端口信息。
func ParseH3CInterfaceInfo(data string) []SwitchInterfaceInfo {
	briefInterfaces := parseH3CInterfaceBrief(data)
	transceiverMap := ParseH3CTransceiverInfo(data)
	vlanMap := ParseH3CPortVlan(data)
	var results []SwitchInterfaceInfo
	if len(briefInterfaces) == 0 && len(transceiverMap) == 0 && len(vlanMap) == 0 {
		return results
	}

	transceiverByKey := make(map[string]TransceiverInfo, len(transceiverMap))
	for port, info := range transceiverMap {
		transceiverByKey[h3cPortKey(port)] = info
	}

	if len(briefInterfaces) > 0 {
		for _, info := range briefInterfaces {
			if metrics, ok := transceiverByKey[h3cPortKey(info.Port)]; ok && hasTransceiverMetrics(metrics) {
				info.ModuleType = metrics.ModuleType
				info.Wavelength = metrics.Wavelength
				info.Temperature = metrics.Temperature
				info.Voltage = metrics.Voltage
				info.Current = metrics.Current
				info.TxPower = metrics.TxPower
				info.RxPower = metrics.RxPower
			}
			if vlanInfo, ok := vlanMap[normalizeH3CPortName(info.Port)]; ok {
				if info.VlanMapping.Port == "" {
					info.VlanMapping.Port = vlanInfo.Port
				}
				if info.VlanMapping.LinkType == "" {
					info.VlanMapping.LinkType = vlanInfo.LinkType
				}
				if info.VlanMapping.PVID == 0 {
					info.VlanMapping.PVID = vlanInfo.PVID
				}
				info.VlanMapping.TrunkVLANList = vlanInfo.TrunkVLANList
			}
			results = append(results, info)
		}
		return results
	}

	ports := make([]string, 0, len(transceiverMap))
	for port := range transceiverMap {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		metrics := transceiverMap[port]
		info := SwitchInterfaceInfo{Port: port, Description: port}
		if hasTransceiverMetrics(metrics) {
			info.ModuleType = metrics.ModuleType
			info.Wavelength = metrics.Wavelength
			info.Temperature = metrics.Temperature
			info.Voltage = metrics.Voltage
			info.Current = metrics.Current
			info.TxPower = metrics.TxPower
			info.RxPower = metrics.RxPower
		}
		if vlanInfo, ok := vlanMap[normalizeH3CPortName(port)]; ok {
			info.VlanMapping = vlanInfo
		}
		results = append(results, info)
	}

	return results
}

// GetSwitchMaker 归一化厂商名称（老实现同名函数，用于选择命令集与解析器）。
func GetSwitchMaker(szMaker string) string {
	var csname string
	lowerMaker := strings.ToLower(szMaker)
	switch {
	case strings.Contains(lowerMaker, "huawei") || strings.Contains(szMaker, "华为"):
		csname = "huawei"
	case strings.Contains(lowerMaker, "cisco") || strings.Contains(szMaker, "思科"):
		csname = "cisco"
	case strings.Contains(lowerMaker, "ruijie") || strings.Contains(szMaker, "锐捷"):
		csname = "ruijie"
	case strings.Contains(lowerMaker, "h3c") || strings.Contains(szMaker, "华三"):
		csname = "h3c"
	case strings.Contains(lowerMaker, "centec") || strings.Contains(szMaker, "盛科"):
		csname = "centec"
	}
	return csname
}
