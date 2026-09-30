package olt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"itmsapiproject/internal/oltconn"
)

// 设备连接层：把老实现的 connectOltTelnet(...) + readOLTCommandUntil(...) 流程接到
// internal/oltconn（Telnet + SSH 双通道），并按 docs/09 §0 施工政策补齐老实现缺的能力：
//
//  1. **通道**：老实现只有 Telnet（默认 23）。新实现按 Type 选通道（1 RPC-API 不适用 /
//     2 Telnet / 3 SSH），未指定时端口 22 → SSH、其余 → Telnet（见 oltconn.Params.Kind）；
//  2. **凭据**：connData 未带账号口令时回落到模块库 tbOltUser（按 IP 定位）；
//     口令一律「密文优先、明文兜底」解密（老库存量明文与迁移后密文都能用）；
//  3. **注册校验**：操作设备前 requireRegistration()，与 switch 模块同口径；
//  4. **会话串行**：同一 (IP/Port/Username) 同时只允许一个会话（老实现 lockOLTSession 的等价物，
//     避免两路请求在同一台设备上交叉读写回显）。
const (
	// oltConnectTimeout 是建链/登录阶段的超时（老实现 TelnetClient.Timeout = 10s）。
	oltConnectTimeout = 10 * time.Second
	// oltReadTimeout 是老实现 readOLTCommandUntil 的单轮读取超时（含其后的静默判定窗口）。
	// 新实现的 runCommand 已改用「回到 config 提示符」判定命令结束（oltconn.RunCommandUntilSignal），
	// 不再依赖本静默窗口；仅此处保留常量作注释/历史依据。
	oltReadTimeout = 200 * time.Millisecond
	// oltCommandMaxWait 是 runCommand 读取一条命令回显的总兜底上限。真机 `display board 0`、
	// `display version` 等一次约 1-2s，多页或大输出命令需更久，取 8s 留足余量。
	oltCommandMaxWait = 8 * time.Second
	// oltMaxIdlePrompts 是老实现 collectOLTCurrentConfiguration 的静默提示上限。
	oltMaxIdlePrompts = 10
)

// oltDevice 是一次已登录的设备会话（用完必须 Close，否则连接与锁都不会释放）。
type oltDevice struct {
	module  *Module
	session oltconn.Session
	ip      string
	onClose func()
}

// lockSession 取某台设备的会话锁（键为 IP:Port:Username，与老实现一致）。
// write=true 时持写锁（独占：同一台设备同一凭据同时只允许一个会话）；
// write=false 时持读锁（只读下钻可并发，彼此互不干扰，均阻塞写会话）。
func (m *Module) lockSession(cp ConnectParameters, write bool) func() {
	key := strings.Join([]string{strings.TrimSpace(cp.IP), strings.TrimSpace(cp.Port), strings.TrimSpace(cp.Username)}, ":")
	m.sessionLocks.Lock()
	lock := m.sessionLocks.locks[key]
	if lock == nil {
		lock = &sync.RWMutex{}
		m.sessionLocks.locks[key] = lock
	}
	m.sessionLocks.Unlock()

	if write {
		lock.Lock()
		return lock.Unlock
	}
	lock.RLock()
	return lock.RUnlock
}

// decodeConnectParameters 解析 connData（连接参数 JSON）。
//
// 与直接 json.Unmarshal 的差异：新口径下 **Type=0 是合法值（TELNET）**，无法再用"Type==0"表示"未提供"，
// 因此显式探测 connData 里有没有 Type 字段：没有则置 oltconn.TypeAuto，由通道层按端口推断
// （22 → SSH，其余 → TELNET）；库中凭据回落时再优先采用库里配置的连接方式。
func decodeConnectParameters(connData string) (ConnectParameters, error) {
	var cp ConnectParameters
	if err := json.Unmarshal([]byte(connData), &cp); err != nil {
		return cp, err
	}
	if !jsonFieldPresent(connData, "Type") {
		cp.Type = oltconn.TypeAuto
	}
	return cp, nil
}

// jsonFieldPresent 判断 JSON 对象里是否存在指定字段（大小写不敏感，兼容老壳的写法差异）。
func jsonFieldPresent(payload, field string) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		return false
	}
	for key := range probe {
		if strings.EqualFold(key, field) {
			return true
		}
	}
	return false
}

// connectDevice 建立并登录一条设备会话（等价老实现 connectOltTelnet，但支持 SSH）。
func (m *Module) connectDevice(cp ConnectParameters) (*oltDevice, error) {
	resolved, err := m.resolveConnectParams(cp)
	if err != nil {
		return nil, err
	}
	if err := m.requireRegistration(); err != nil {
		return nil, err
	}
	unlock := m.lockSession(resolved, true)
	session, err := oltconn.Connect(oltconn.Params{
		Type:     resolved.Type,
		IP:       resolved.IP,
		Port:     oltPort(resolved),
		Username: resolved.Username,
		Password: resolved.Password,
		Timeout:  oltConnectTimeout,
	})
	if err != nil {
		unlock()
		return nil, err
	}
	// 进入配置模式（enable → scroll → "" → config，直到 `(config)` 提示符）。
	// 老实现 connectOltTelnet 的 Connect() 尾部就是这么做的；缺少它时华为 OLT 停留在用户模式
	// `MA5603T>`，`display current-configuration` 会得到 "Unknown command"，采集永不结束
	// （线上「刷新配置显示采集中… 再无响应」的根因）。
	if err := oltconn.EnterConfigMode(session); err != nil {
		_ = session.Close()
		unlock()
		return nil, err
	}
	// 尽力关闭分页（老实现依赖逐轮 200ms 读窗口 + 5s 兜底；新实现直接关分页，
	// 关不掉时由 oltconn.ReadPaged 自动翻页兜底，行为更稳且更快）。
	oltconn.DisablePaging(session)
	return &oltDevice{module: m, session: session, ip: resolved.IP, onClose: unlock}, nil
}

// connectDeviceReadOnly 建立一条**只读**设备会话（供下钻等并行只读命令使用，方案 B）：
// 只持会话读锁，因此同一台设备的多条只读下钻会话可以并行，但会阻塞写会话（保存/配置类）。
// 注意：仅限纯只读命令使用；凡是会改变设备状态或读取主会话独占资源的场景一律用 connectDevice。
func (m *Module) connectDeviceReadOnly(cp ConnectParameters) (*oltDevice, error) {
	resolved, err := m.resolveConnectParams(cp)
	if err != nil {
		return nil, err
	}
	if err := m.requireRegistration(); err != nil {
		return nil, err
	}
	unlock := m.lockSession(resolved, false)
	session, err := oltconn.Connect(oltconn.Params{
		Type:     resolved.Type,
		IP:       resolved.IP,
		Port:     oltPort(resolved),
		Username: resolved.Username,
		Password: resolved.Password,
		Timeout:  oltConnectTimeout,
	})
	if err != nil {
		unlock()
		return nil, err
	}
	// 进入配置模式（与 connectDevice 同一口径，见其注释）。只读下钻也需要特权/配置模式。
	if err := oltconn.EnterConfigMode(session); err != nil {
		_ = session.Close()
		unlock()
		return nil, err
	}
	oltconn.DisablePaging(session)
	return &oltDevice{module: m, session: session, ip: resolved.IP, onClose: unlock}, nil
}

// resolveConnectParams 解析连接参数：补全库中凭据、解密口令、校验必填项。
//
// 与老实现的差异（已记入 docs/09 §5.2 偏差表）：老实现完全信任 connData，
// 账号口令为空时直接拿空口令去登录；新实现在缺账号/口令时按 IP 从 tbOltUser 补全，
// 减少"前端没带全参就必然登录失败"的无谓 RTT。
func (m *Module) resolveConnectParams(cp ConnectParameters) (ConnectParameters, error) {
	cp.IP = strings.TrimSpace(cp.IP)
	cp.Username = strings.TrimSpace(cp.Username)
	cp.Password = strings.TrimSpace(cp.Password)
	cp.Port = strings.TrimSpace(cp.Port)
	if cp.IP == "" {
		return cp, fmt.Errorf("invalid request: OLT IP is required")
	}
	if (cp.Username == "" || cp.Password == "" || cp.Type == oltconn.TypeAuto || cp.Port == "") && m.store != nil {
		if user, err := m.store.FindOltUserByIP(cp.IP, cp.Username); err == nil {
			if cp.Username == "" {
				cp.Username = strings.TrimSpace(user.Username)
			}
			if cp.Password == "" {
				cp.Password = strings.TrimSpace(user.Password)
			}
			if cp.Port == "" {
				cp.Port = strings.TrimSpace(user.Port)
			}
			if cp.Type == oltconn.TypeAuto {
				// 调用方没指定连接方式：用库中该设备的配置（新口径 0/1，老数据可能是 2/3）。
				cp.Type = user.Type
			}
		}
	}
	cp.Username = m.decryptOltSecret(cp.Username)
	cp.Password = m.decryptOltSecret(cp.Password)
	if cp.Username == "" {
		return cp, fmt.Errorf("invalid request: OLT username is required")
	}
	return cp, nil
}

// oltPort 返回实际连接端口：Port 文本可解析时用它，否则按连接方式取默认（SSH 22 / TELNET 23）。
// 连接方式口径：1/3 = SSH，0/2 = TELNET（见 oltconn.Params.Kind）。
func oltPort(cp ConnectParameters) int {
	if port, err := strconv.Atoi(strings.TrimSpace(cp.Port)); err == nil && port > 0 {
		return port
	}
	if cp.Type == 1 || cp.Type == 3 {
		return 22
	}
	return 23
}

// runCommand 发送命令并读取回显，直到设备的 config 提示符（`MA5603T(config)#`）出现。
//
// 不再用 ReadPaged(oltReadTimeout) 的静默窗口：真机 MA5603T 回显分批下发、段间停顿常超 200ms，
// 120ms 静默窗口会提前把命令读断（实测 `display board 0` 只拿到 7 字节 "display"）。
// 改以「回到 `#` 提示符」为命令结束（见 RunCommandUntilSignal），读回内容必然完整。
func (d *oltDevice) runCommand(command string) (string, error) {
	if d == nil || d.session == nil {
		return "", fmt.Errorf("OLT connection is not initialized")
	}
	if err := d.session.Send(command); err != nil {
		return "", err
	}
	// 补一个空行：MA5600 交互提示需要触发回显继续（与老实现一致）。
	if err := d.session.Send(""); err != nil {
		return "", err
	}
	return oltconn.RunCommandUntilSignal(d.session, oltCommandMaxWait)
}

// Close 关闭会话并释放会话锁（幂等）。
func (d *oltDevice) Close() error {
	if d == nil {
		return nil
	}
	var err error
	if d.session != nil {
		err = d.session.Close()
		d.session = nil
	}
	if d.onClose != nil {
		d.onClose()
		d.onClose = nil
	}
	return err
}
