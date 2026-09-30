package olt

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOLT 是一个最小 OLT Telnet 服务：登录提示 → 命令回显。
// 默认模拟 Huawei MA5800 风格（`VERSION :` 全大写、主控在槽 0）；model="ma5603" 时
// 模拟 MA5603T（Huawei MA5600 系）风格：主控槽非 0、裸 `display version` 先弹交互参数提示、
// 随后（补回车后）回显大小写混合 `Software Version:` + PRODUCT + Uptime 的完整版本文本。
// 仅用于在没有真机时验证「连接层 + 采集管道 + 解析 + handler 契约」。
const (
	fakeModelMA5800 = ""       // 默认
	fakeModelMA5603 = "ma5603" // MA5600/MA5603T 风格
)

type fakeOLT struct {
	listener net.Listener
	model    string
	mu       sync.Mutex
	commands []string
	closed   bool
}

// startFakeOLT 启动默认（MA5800 风格）假 OLT（随机端口），测试结束自动关闭。
func startFakeOLT(t *testing.T) *fakeOLT {
	t.Helper()
	return startFakeOLTModel(t, fakeModelMA5800)
}

// startFakeOLT5603 启动 MA5603T 风格假 OLT（主控槽 6 + Software Version 回显，见 fakeOLTResponse）。
// 用于端到端覆盖「裸 display version 弹交互提示 → RunCommandUntilSignal 补回车 → 完整版本文本」这条真机路径。
func startFakeOLT5603(t *testing.T) *fakeOLT {
	t.Helper()
	return startFakeOLTModel(t, fakeModelMA5603)
}

func startFakeOLTModel(t *testing.T, model string) *fakeOLT {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeOLT{listener: listener, model: model}
	go server.serve()
	t.Cleanup(server.Close)
	return server
}

func (f *fakeOLT) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeOLT) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	write := func(text string) {
		_, _ = conn.Write([]byte(text))
	}
	write("Username:")
	_, _ = reader.ReadString('\n')
	write("\r\nPassword:")
	_, _ = reader.ReadString('\n')
	write("\r\nMA5800-X7>\r\n")

	mode := ""               // "add" | "change" | "delete"
	configMode := false      // 是否已进入配置模式（enable → config）
	versionPrompted := false // MA5603T：裸 `display version` 是否已弹过交互参数提示
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		f.mu.Lock()
		f.commands = append(f.commands, command)
		f.mu.Unlock()
		write(fakeOLTResponse(command, f.model, &mode, &configMode, &versionPrompted))
	}
}

// Commands 返回收到的命令（供断言"只发了只读命令"）。
func (f *fakeOLT) Commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

// HostPort 返回监听地址（IPv4 主机 + 端口文本）。
func (f *fakeOLT) HostPort() (string, string) {
	host, port, err := net.SplitHostPort(f.listener.Addr().String())
	if err != nil {
		return "127.0.0.1", "0"
	}
	return host, port
}

func (f *fakeOLT) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	f.mu.Unlock()
	_ = f.listener.Close()
}

// fakeOLTResponse 按命令返回固定回显；mode 维护 terminal-user 新增/修改/删除交互状态，
// configMode 维护 enable → scroll → "" → config 的配置模式提升（与真机 connectOltTelnet 一致）。
// versionPrompted 记录 MA5603T 风格的裸 `display version` 是否已弹过交互参数提示：
// 首次弹提示、补回车（空行）后回显完整版本文本，模拟真机交互。
// model 用于在 MA5800（默认，`fakeModelMA5800`）与 MA5603T（`fakeModelMA5603`）两种回显风格间切换。
func fakeOLTResponse(command, model string, mode *string, configMode *bool, versionPrompted *bool) string {
	cfgPrompt := func() string {
		if configMode != nil && *configMode {
			return "\r\nMA5800-X7(config)#"
		}
		return "\r\nMA5800-X7>"
	}
	// MA5603T 风格：主控槽 6；裸 `display version` 先弹出交互参数提示
	// （`{ <cr>|backplane|frameid/slotid }:`），补回车后回显完整版本文本
	// （大小写混合 `Software Version:` + PRODUCT + Uptime，且同屏含 PCB/BIOS/Logic 等多行版本）。
	if model == fakeModelMA5603 {
		switch {
		case command == "display board 0":
			return "\r\n" +
				"  ------------------------------------------------------------------\r\n" +
				"  SlotID  BoardName  Status          SubType0 SubType1    Online/Offline\r\n" +
				"  ------------------------------------------------------------------\r\n" +
				"  4       H805GPBD   Normal\r\n" +
				"  6       H801SCUN   Normal\r\n" +
				"  8       H801GICF   Normal\r\n" + cfgPrompt()
		case command == "display board 0/4":
			return "\r\n  Board Name    : H805GPBD\r\n" +
				"  Board Status  : Normal\r\n" +
				"  0      GPON       Online\r\n" +
				"  1      GPON       Online\r\n" +
				"  2      GPON       Offline\r\n" +
				"  In port 0/4/0 the total of ONTs are: 8, online: 5\r\n" +
				"  In port 0/4/1 the total of ONTs are: 2, online: 1\r\n" + cfgPrompt()
		case command == "display board 0/8":
			return "\r\n  Board Name    : H801GICF\r\n" +
				"  Board Status  : Normal\r\n" +
				"  0      GE      --     1      --     auto       auto_full   --   --   online\r\n" +
				"  1      GE      --     1      --     auto_1000  auto_full   --   --   offline\r\n" + cfgPrompt()
		case command == "display temperature 0":
			return "\r\n" +
				"  SlotID : 4    Temperature : 41 C\r\n" +
				"  SlotID : 6    Temperature : 38 C\r\n" + cfgPrompt()
		case command == "display version":
			// 第一发：裸命令弹交互参数提示（不含版本号）——由 RunCommandUntilSignal 识别并补回车，
			// 第二发（补回车后的空命令）才回显完整版本文本（见下方 command == "" 分支）。
			if versionPrompted == nil || !*versionPrompted {
				if versionPrompted != nil {
					*versionPrompted = true
				}
				return "\r\n  { <cr>|backplane|frameid/slotid }:"
			}
			// 后续（已弹过提示且补回车）仍按完整版本回显，避免重复弹提示死循环。
			return fullMA5603VersionText() + cfgPrompt()
		case command == "":
			// 补回车后的回显：RunCommandUntilSignal 在参数提示时 Send("") 触发。
			// MA5603T 先弹提示、补回车后回显完整版本文本（模拟真机交互）。
			if model == fakeModelMA5603 && versionPrompted != nil && *versionPrompted {
				versionPrompted = nil // 只消费一次，避免干扰后续空命令
				return fullMA5603VersionText() + cfgPrompt()
			}
			return cfgPrompt()
		}
	}
	switch {
	case configMode != nil && command == "enable":
		// 提升到特权模式（#），尚未进配置模式。
		return "\r\nMA5800-X7#"
	case configMode != nil && command == "scroll":
		return "\r\n  { <cr>|number<U><10,512> }:\r\nMA5800-X7#"
	case configMode != nil && command == "config":
		*configMode = true
		return "\r\nMA5800-X7(config)#"
	case command == "":
		return cfgPrompt()
	case strings.HasPrefix(command, "screen-length"), strings.HasPrefix(command, "terminal length"):
		return cfgPrompt()
	case mode != nil && *mode != "" && command == "n":
		// 交互序列结束（新增/修改以"n"收尾；删除是 y 后跟 n）。
		*mode = ""
		return cfgPrompt()
	case mode != nil && command == "terminal user name":
		*mode = "add"
		return "\r\n  Command:\r\n  user name\r\n  User profile name:\r\n" + cfgPrompt()
	case mode != nil && command == "terminal user password":
		*mode = "change"
		return "\r\n  Save the configuration? (y/n)[n]:" + cfgPrompt()
	case mode != nil && command == "undo terminal user name":
		*mode = "delete"
		return "\r\n  Are you sure to delete the user? (y/n)[n]:\r\n" + cfgPrompt()
	case mode != nil && *mode == "add":
		// 用户名/两次口令回显都带上 "user profile name"，让新增判定命中。
		return "\r\n  User profile name:\r\n" + cfgPrompt()
	case mode != nil && (*mode == "change" || *mode == "delete") && command != "":
		// 修改/删除：确认口令或删除时命中 (y/n)。
		return "(y/n)" + cfgPrompt()
	case command == "display version":
		return "\r\n" +
			"  VERSION : MA5800V100R019C10\r\n" +
			"  PATCH   : SPC200 SPH208\r\n" +
			"  PRODUCT : MA5800-X7\r\n" +
			"  Uptime is 128 day(s), 5 hour(s), 20 minute(s), 7 second(s)\r\n" + cfgPrompt()
	case command == "display temperature 0":
		return "\r\n" +
			"  SlotID : 0    Temperature : 36 C\r\n" +
			"  SlotID : 1    Temperature : 41 C\r\n" + cfgPrompt()
	case command == "display temperature 0/1":
		return "\r\n  Temperature : 47 C\r\n" + cfgPrompt()
	case command == "display board 0":
		return "\r\n" +
			"  ------------------------------------------------------------------\r\n" +
			"  SlotID  BoardName  Status          SubType0 SubType1    Online/Offline\r\n" +
			"  ------------------------------------------------------------------\r\n" +
			"  0       H901SCUN   Normal\r\n" +
			"  1       H901GPBD   Normal\r\n" +
			"  2       H901GPBD   Offline\r\n" + cfgPrompt()
	case command == "display board 0/1":
		return "\r\n" +
			"  Board Name    : H901GPBD\r\n" +
			"  Board Status  : Normal\r\n" +
			"  ------------------------------------------------------------------\r\n" +
			"  Port   PortType   PortStatus\r\n" +
			"  ------------------------------------------------------------------\r\n" +
			"  0      GPON       Online\r\n" +
			"  1      GPON       Online\r\n" +
			"  2      GPON       Offline\r\n" +
			"  In port 0/1/0 the total of ONTs are: 8, online: 5\r\n" +
			"  In port 0/1/1 the total of ONTs are: 2, online: 1\r\n" + cfgPrompt()
	case command == "display current-configuration":
		return "\r\n" +
			"  Command:\r\n  display current-configuration\r\n" +
			"  !\r\n  hostname OLT-TEST\r\n" +
			"  terminal user name admin\r\n" +
			"  terminal user name olt_operator\r\n" +
			"  terminal user password admin\r\n" +
			"  terminal user name buildrun_new_password admin\r\n" +
			"  interface gpon 0/1\r\n" +
			"  ont add 0 0 sn-auth 48575443AABBCCDD omci ont-lineprofile-id 10\r\n" +
			"  ont add 1 0 sn-auth 48575443AABBCCD1 omci ont-lineprofile-id 10\r\n" +
			"  interface vlanif100\r\n" +
			"  ip address 192.168.50.1 255.255.255.0\r\n" +
			"  quit\r\n" +
			"  vlan 100 smart\r\n" +
			"  vlan 200 attrib q-in-q\r\n" +
			"  port vlan 100 0/3/0\r\n" +
			// DBA 模板（handleDBAProfile Act=0 读已保存配置解析）：覆盖全部 5 种 type。
			"  [global-config]\r\n" +
			"  dba-profile add profile-id 10 profile-name \"DBA Main\" type3 assure 20032 max 1000000\r\n" +
			"  dba-profile add profile-id 20 profile-name \"Fixed\" type1 fix 512\r\n" +
			"  dba-profile add profile-id 30 profile-name \"Assured\" type2 assure 1024\r\n" +
			"  dba-profile add profile-id 40 profile-name \"Maxed\" type4 max 4096\r\n" +
			"  dba-profile add profile-id 50 profile-name \"Full\" type5 fix 128 assure 256 max 512\r\n" +
			// ONT 线路模板（handleLineProfile Act=0 读已保存配置解析）：单 T-CONT 1、GEM 0-based
			// （与 handleLineProfile Act=1/解析器一致的新写法），用于断言“重连后模板缓存刷新到最新”。
			"  ont-lineprofile test profile-id 10 profile-name \"testlinepro12\"\r\n" +
			"  tcont 1 dba-profile-id 10\r\n" +
			"  gem add 0 eth tcont 1\r\n" +
			"  gem mapping 0 1 vlan 100\r\n" +
			"  gem add 1 eth tcont 1\r\n" +
			"  gem mapping 1 1 vlan 300\r\n" +
			"  commit\r\n" +
			"  #\r\n" +
			"  ip route-static 10.0.0.0 255.255.255.0 192.168.50.254\r\n" +
			"  #\r\n" +
			"  return\r\n" + cfgPrompt()
	case strings.HasPrefix(command, "dba-profile add"), strings.HasPrefix(command, "dba-profile delete"):
		// DBA 模板写操作（handleDBAProfile Act=1/2 下发命令后的回显）。
		return "\r\n  dba-profile success\r\n" + cfgPrompt()
	case strings.HasPrefix(command, "ont-lineprofile"), strings.HasPrefix(command, "undo ont-lineprofile"):
		// 线路模板写操作（handleLineProfile Act=1/2 下发命令后的回显）。
		return "\r\n  ont-lineprofile success\r\n" + cfgPrompt()
	case strings.HasPrefix(command, "ont-srvprofile"), strings.HasPrefix(command, "undo ont-srvprofile"),
		strings.HasPrefix(command, "ont-port"), strings.HasPrefix(command, "port vlan eth"):
		// ONT 业务模板写操作（handleSrvProfile Act=1/2 的 ont-srvprofile / ont-port / port vlan eth 命令回显）。
		return "\r\n  ont-srvprofile success\r\n" + cfgPrompt()
	case strings.HasPrefix(command, "ont tr069-server-profile"):
		// TR069 管理模板写操作（handleTr069Profile Act=1/2 下发命令后的回显）。
		return "\r\n  ont tr069-server-profile success\r\n" + cfgPrompt()
	case strings.HasPrefix(command, "commit"), strings.HasPrefix(command, "quit"), strings.HasPrefix(command, "tr069-management"),
		strings.HasPrefix(command, "tcont"), strings.HasPrefix(command, "gem add"), strings.HasPrefix(command, "gem mapping"):
		// 线路模板新增的逐句子命令（tr069 / tcont / gem add / gem mapping / commit / quit）。
		return "\r\n  ont-lineprofile success\r\n" + cfgPrompt()
	default:
		return cfgPrompt()
	}
}

// fullMA5603VersionText 返回 MA5603T 风格的完整版本文本（补回车后回显）：
// 大小写混合 `Software Version:`（区别于 MA5800 全大写 VERSION），且同屏含 PCB/BIOS/Logic
// 等多行版本；另含 PRODUCT（机框）与 Uptime（运行时长）——机框/运行时间只来自完整版本文本。
func fullMA5603VersionText() string {
	return "\r\n" +
		"  Main Board: H801SCUN\r\n" +
		"  ---------------------------------------\r\n" +
		"  PCB  Version: H801SCUN  VER B\r\n" +
		"  Base     BIOS Version: 216\r\n" +
		"  Extended BIOS Version: 222\r\n" +
		"  Software      Version: MA5600V800R011C00\r\n" +
		"  Logic         Version: (U48)115\r\n" +
		"  MAB           Version: 0002\r\n" +
		"  PRODUCT : MA5603T\r\n" +
		"  Uptime is 42 day(s), 7 hour(s), 30 minute(s), 9 second(s)"
}

// newDeviceTestModule 建一个"文件目录也隔离"的模块实例：
// 库、可写文件目录、老文件目录（只读兜底）全部指向临时目录，绝不触碰 /home/olt 与线上数据。
func newDeviceTestModule(t *testing.T) (*Module, string, string) {
	t.Helper()
	module := newTestModule(t)
	fileDir := t.TempDir()
	legacyDir := t.TempDir()
	t.Setenv(envFileDir, fileDir)
	t.Setenv(envLegacyFileDir, legacyDir)
	return module, fileDir, legacyDir
}

// waitFor 轮询等待条件成立（用于断言后台刷新等异步行为）。
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
