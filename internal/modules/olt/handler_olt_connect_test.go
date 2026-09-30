package olt

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOltConnectCollectsBoardAndCaches 覆盖 handleOltConnect Act=1 → Act=0 的完整往返：
// 采集（display version/temperature/board + 在线业务板下钻）→ 解析 → 落缓存 → 再读缓存。
func TestOltConnectCollectsBoardAndCaches(t *testing.T) {
	module, fileDir, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	form := url.Values{}
	form.Set("Act", "1")
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"`+port+`","Username":"oltuser","Password":"secret"}`)
	code, payload := postOltForm(t, module.handleOltConnect, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 失败: http=%d payload=%v", code, payload)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		t.Fatalf("Act=1 未返回 data: %v", payload)
	}
	if got := data["Version"]; got != "MA5800V100R019C10" {
		t.Fatalf("Version = %v", got)
	}
	if got := data["OLTRunTime"]; got != "128天5小时20分7秒" {
		t.Fatalf("OLTRunTime = %v", got)
	}
	if got := data["FrameTotal"].(float64); got != 3 {
		t.Fatalf("FrameTotal = %v, want 3（display board 0 的三行）", got)
	}
	if got := data["OntOnlines"].(float64); got != 6 {
		t.Fatalf("OntOnlines = %v, want 6", got)
	}
	if got := data["OntTotal"].(float64); got != 10 {
		t.Fatalf("OntTotal = %v, want 10", got)
	}
	boards, _ := data["OltBoardInfo"].([]any)
	if len(boards) != 3 {
		t.Fatalf("板卡数 = %d, want 3（%v）", len(boards), boards)
	}
	controlBoard := boards[0].(map[string]any)
	if got := controlBoard["Type"]; got != "control" {
		t.Fatalf("主控板 Type = %v, want control", got)
	}
	onlineBoard := boards[1].(map[string]any)
	if got := onlineBoard["Type"]; got != "user" {
		t.Fatalf("在线业务板 Type = %v, want user", got)
	}
	if got := onlineBoard["Name"]; got != "H901GPBD" {
		t.Fatalf("在线业务板 Name = %v", got)
	}
	if got := onlineBoard["Temperature"]; got != "41" {
		t.Fatalf("在线业务板 Temperature = %v, want 41（取概览温度，不再单独查）", got)
	}
	if got := onlineBoard["PortNum"].(float64); got != 3 {
		t.Fatalf("在线业务板 PortNum = %v, want 3", got)
	}
	if got := onlineBoard["Desc"]; got != "3口GPON业务板" {
		t.Fatalf("在线业务板 Desc = %v", got)
	}
	ports, _ := onlineBoard["PonInfos"].([]any)
	if len(ports) != 3 {
		t.Fatalf("在线业务板 PON 口 = %d, want 3", len(ports))
	}
	firstPort := ports[0].(map[string]any)
	if firstPort["OntOnline"].(float64) != 5 || firstPort["OntTotal"].(float64) != 8 {
		t.Fatalf("PON 口 0 统计 = %v/%v, want 5/8", firstPort["OntOnline"], firstPort["OntTotal"])
	}
	if got := firstPort["PortStatus"]; got != "Online" {
		t.Fatalf("PON 口 0 状态 = %v, want Online", got)
	}
	if got := firstPort["FSP"]; got != "0/1/0" {
		t.Fatalf("PON 口 0 FSP = %v, want 0/1/0", got)
	}
	offlineBoard := boards[2].(map[string]any)
	if got := offlineBoard["PortNum"].(float64); got != 0 {
		t.Fatalf("离线板卡 PortNum = %v, want 0（老口径：离线不下钻）", got)
	}

	cachePath := fileDir + "/" + host + "/currentconfiguration.json"
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("缓存文件未生成(%s): %v", cachePath, err)
	}
	for _, command := range server.Commands() {
		switch {
		case command == "",
			command == "enable",
			command == "scroll",
			command == "config",
			strings.HasPrefix(command, "display "),
			strings.HasPrefix(command, "screen-length"),
			strings.HasPrefix(command, "terminal length"):
		default:
			t.Fatalf("采集过程发出了非只读命令: %q", command)
		}
	}

	// Act=0 读缓存：先关掉假设备，确保返回缓存而不是又连了一次设备；
	// Message 保留老实现的 "sueccess" 笔误（不改老前端判断）。
	server.Close()
	_, cached := postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"0"},
		"connData": {`{"IP":"` + host + `"}`},
	})
	if cached["code"].(float64) != 0 || cached["message"] != "sueccess" {
		t.Fatalf("Act=0 响应 = %v", cached)
	}
	cachedData, _ := cached["data"].(map[string]any)
	if cachedData == nil || cachedData["Version"] != "MA5800V100R019C10" {
		t.Fatalf("Act=0 未读到缓存: %v", cached)
	}
}

// TestOltConnectWritesSnapshotToDB 覆盖采集结果落表：
// Act=1 后表里有设备快照（板卡/PON 口），且删掉模块缓存文件后 Act=0 仍能从表里读到（表优先）。
func TestOltConnectWritesSnapshotToDB(t *testing.T) {
	module, fileDir, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	_, collected := postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"1"},
		"connData": {oltConnData(host, port)},
	})
	if collected["code"].(float64) != 0 {
		t.Fatalf("Act=1 采集失败: %v", collected)
	}

	// 表里应有：快照 1 行 + 板卡 3 行 + PON 口 3 行（在线业务板下钻得到）
	snapshot, ok, err := module.store.LoadDeviceSnapshot(host)
	if err != nil || !ok {
		t.Fatalf("采集结果未入库: ok=%v err=%v", ok, err)
	}
	if snapshot.Version != "MA5800V100R019C10" || len(snapshot.OltBoardInfo) != 3 {
		t.Fatalf("快照内容不符: version=%s boards=%d", snapshot.Version, len(snapshot.OltBoardInfo))
	}
	ponCount := 0
	for _, board := range snapshot.OltBoardInfo {
		ponCount += len(board.PonInfos)
	}
	if ponCount != 3 {
		t.Fatalf("PON 口入库数 = %d, want 3", ponCount)
	}

	// 删掉模块缓存文件（只留表）→ Act=0 仍应返回同一份数据（表优先）
	if err := os.Remove(filepath.Join(fileDir, host, "currentconfiguration.json")); err != nil {
		t.Fatalf("删除缓存文件失败: %v", err)
	}
	_, cached := postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"0"},
		"connData": {`{"IP":"` + host + `"}`},
	})
	if cached["code"].(float64) != 0 || cached["message"] != "sueccess" {
		t.Fatalf("Act=0 响应异常: %v", cached)
	}
	data, _ := cached["data"].(map[string]any)
	if data == nil || data["Version"] != "MA5800V100R019C10" {
		t.Fatalf("无文件时未从表读到快照: %v", cached)
	}
	if boards, _ := data["OltBoardInfo"].([]any); len(boards) != 3 {
		t.Fatalf("表优先读取的板卡数 = %d, want 3", len(boards))
	}
}

// TestHandleCurrentConfigurationPrefersTable 覆盖已保存配置的"表优先"读取。
func TestHandleCurrentConfigurationPrefersTable(t *testing.T) {
	module, fileDir, _ := newDeviceTestModule(t)
	const host = "192.0.2.95"
	if err := module.store.SaveConfigText(host, ConfigKindSaved, "hostname FROM-DB\nreturn", "", "", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText: %v", err)
	}
	// 文件里放一份"过期"内容：表优先时不应返回它
	if err := os.MkdirAll(filepath.Join(fileDir, host), 0755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fileDir, host, "savedconfiguration.txt"), []byte("hostname STALE-FILE"), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	_, payload := postOltForm(t, module.handleCurrentConfiguration, url.Values{
		"Act":      {"0"},
		"connData": {`{"IP":"` + host + `"}`},
	})
	if payload["code"].(float64) != 0 || payload["data"] != "hostname FROM-DB\nreturn" {
		t.Fatalf("未优先读表: %v", payload)
	}
}

// TestOltConnectRefreshSavedConfigOnReconnect 覆盖修复：重连（handleOltConnect Act=1）后自动在后台
// 重采「已保存配置」（Kind=savedconfiguration）并持久化，使 DBA/线路模板等「读已保存配置」栏目
// 能反映真机最新配置——此前 Act=1 只采版本/温度/板卡，重连不会刷新模板缓存（旧快照残留）。
//
// 断言点：Act=1 触发 refreshSavedConfigurationInBackground（后台 goroutine）后，表里 Kind=saved 的
// 配置文本最终包含 0-based 的 `tcont 1` 线路模板映射（新写法；旧 1-based tcont 2/3/4 不应出现在
// 模板配置里），即重连把模板缓存刷新到了最新。
func TestOltConnectRefreshesSavedConfigOnReconnect(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"1"},
		"connData": {oltConnData(host, port)},
	})

	// 后台刷新是异步的（refreshSavedConfigurationInBackground），轮询等待入库完成。
	// 每趟采集（建连 + EnterConfigMode + display current-configuration + read 超时）需数秒，
	// 故给足轮询上限（> 单趟采集耗时）。
	ok := waitFor(t, 10*time.Second, func() bool {
		content, found, err := module.store.LoadConfigText(host, ConfigKindSaved)
		return err == nil && found && strings.Contains(content, "tcont 1 dba-profile-id 10")
	})
	if !ok {
		content, found, _ := module.store.LoadConfigText(host, ConfigKindSaved)
		t.Fatalf("重连后未把已保存配置刷新到含 tcont 1 的最新缓存: found=%v content=%q", found, content)
	}

	// 读取侧（handleLineProfile Act=0）应解析出单 T-CONT 1 的线路模板（新写法）。
	_, payload := postOltForm(t, module.handleLineProfile, url.Values{
		"connData": {oltConnData(host, port)},
		"saveData": {`{"Act":0}`},
	})
	if payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 读取失败: %v", payload)
	}
	data, _ := payload["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("应解析出 1 条线路模板，got %v", data)
	}
	first := data[0].(map[string]any)
	if first["Id"].(string) != "10" || first["Name"] != "testlinepro12" {
		t.Fatalf("线路模板 = %v", first)
	}
	gem, _ := first["GemMapping"].([]any)
	if len(gem) != 1 {
		t.Fatalf("GemMapping 应为 1 条 TCONT（单 T-CONT 1），got %v", first["GemMapping"])
	}
	if gm := gem[0].(map[string]any); gm["TcontId"] != "1" {
		t.Fatalf("重连后 TCONT 应为 1（新写法），got %v", gm)
	}
}

// TestOltConnectAct0CarriesDeviceOnlineStatus 覆盖 Act=0「读缓存并连带设备连接/在线状态」：
// 台账里把该 IP 的 oltuser 标记为 Automatic（连接状态）后，Act=0 的响应 data 应带 Automatic=1；
// 同时用未标记的 IP 验证回落 0。确保前端只看缓存就能拿到设备在线状态，无需再连设备。
func TestOltConnectAct0CarriesDeviceOnlineStatus(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	// 台账先建一条 oltuser（Act=1 采集时 SetOltUserAutomatic 会把它置 1）。
	if err := module.store.InsertOltUser(OltUser{Username: "oltuser", IP: host, Comment: "test"}, "secret", 23); err != nil {
		t.Fatalf("InsertOltUser: %v", err)
	}

	// Act=1 采集（内部 SetOltUserAutomatic("oltuser") → Automatic=1）并落缓存。
	_, collected := postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"1"},
		"connData": {oltConnData(host, port)},
	})
	if collected["code"].(float64) != 0 {
		t.Fatalf("Act=1 采集失败: %v", collected)
	}

	// 断掉设备，Act=0 只读缓存：应连带返回 Automatic=1（设备在线/已连接）。
	server.Close()
	_, cached := postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"0"},
		"connData": {`{"IP":"` + host + `"}`},
	})
	if cached["code"].(float64) != 0 {
		t.Fatalf("Act=0 读缓存失败: %v", cached)
	}
	data, _ := cached["data"].(map[string]any)
	if data == nil {
		t.Fatalf("Act=0 未返回 data: %v", cached)
	}
	if got := data["Automatic"].(float64); got != 1 {
		t.Fatalf("已标记的台账用户 Automatic = %v, want 1", got)
	}

	// 台账里没有 Automatic=1 记录的 IP：Act=0 应回落 0（字段缺省时前端按未连接处理）。
	if err := module.store.InsertOltUser(OltUser{Username: "other", IP: "192.0.2.77", Comment: "未连接"}, "olt", 23); err != nil {
		t.Fatalf("InsertOltUser(other): %v", err)
	}
	cfg := OltCurrentConfig{Version: "v-reset", OltBoardInfo: []OltBoardInfo{{Id: "0"}}}
	module.fillDeviceOnlineStatus("192.0.2.77", &cfg)
	if cfg.Automatic != 0 {
		t.Fatalf("未标记设备 Automatic = %v, want 0", cfg.Automatic)
	}
}

// TestOltConnectReusesCachedBoardPorts 方案 A 的缓存复用回归：第二次 Act=1 强制刷新时，
// 在线业务板因缓存已有端口明细（PonInfos/GeInfos）而不再下发 `display board 0/<slot>` 下钻命令，
// 但端口明细仍由缓存补齐（不丢记录）；首次（无缓存）连接仍照常下钻全采。
func TestOltConnectReusesCachedBoardPorts(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	// 首次连接（无缓存）→ 在线业务板槽位 1 必须下钻 display board 0/1。
	before := len(server.Commands())
	_, payload := postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"1"},
		"connData": {oltConnData(host, port)},
	})
	if payload["code"].(float64) != 0 {
		t.Fatalf("首次 Act=1 失败: %v", payload)
	}
	if !containsCommand(server.Commands()[before:], "display board 0/1") {
		t.Fatalf("首次连接应下钻在线业务板（display board 0/1），命令=%v", server.Commands()[before:])
	}

	// 第二次 Act=1（refresh=1 强制采集）→ 已有缓存，在线业务板不再下钻。
	before = len(server.Commands())
	_, payload = postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"1"},
		"refresh":  {"1"},
		"connData": {oltConnData(host, port)},
	})
	if payload["code"].(float64) != 0 {
		t.Fatalf("再次 Act=1 失败: %v", payload)
	}
	if containsCommand(server.Commands()[before:], "display board 0/1") {
		t.Fatalf("缓存已有时不应再下钻 display board 0/1，命令=%v", server.Commands()[before:])
	}

	// 采集结果的板卡端口明细仍完整（复用缓存 + 实时温度/状态）。
	data, _ := payload["data"].(map[string]any)
	boards, _ := data["OltBoardInfo"].([]any)
	var onlineBoard map[string]any
	for _, b := range boards {
		if bm, ok := b.(map[string]any); ok && bm["Id"] == "1" && bm["Name"] == "H901GPBD" {
			onlineBoard = bm
		}
	}
	if onlineBoard == nil {
		t.Fatalf("响应缺少在线业务板(Id=1): %v", boards)
	}
	if ports, ok := onlineBoard["PonInfos"].([]any); !ok || len(ports) != 3 {
		t.Fatalf("复用缓存后 PON 口应仍为 3, got %v", onlineBoard["PonInfos"])
	}
}

// TestOltConnectCollectsMA5603T 覆盖 MA5603T 真机的版本采集兼容（端到端）：
// 主控槽 6（非 0）、大小写混合 `Software Version:`、裸 `display version` 先弹交互提示、
// 补回车后回显完整版本文本（含 PRODUCT 机框与 Uptime 运行时长）。
// 关键验证点：**只发**裸 `display version`（RunCommandUntilSignal 自动补回车推交互提示）；
// 不再发 `display version 0/<主控槽>`——因为该命令只有主控板软件版本、没有机框/运行时间。
func TestOltConnectCollectsMA5603T(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT5603(t)
	host, port := server.HostPort()

	_, payload := postOltForm(t, module.handleOltConnect, url.Values{
		"Act":      {"1"},
		"connData": {oltConnData(host, port)},
	})
	if payload["code"].(float64) != 0 {
		t.Fatalf("MA5603T Act=1 采集失败: %v", payload)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		t.Fatalf("Act=1 未返回 data: %v", payload)
	}
	// 1) 版本取到 MA5600 软件版本（Software Version 行，非 BIOS 216）。
	if got := data["Version"]; got != "MA5600V800R011C00" {
		t.Fatalf("Version = %v, want MA5600V800R011C00", got)
	}
	// 2) 机框（PRODUCT 行 → Frame 字段）与运行时长（Uptime）来自完整版本文本。
	//    老实现口径：Product 字段恒为空（老快照逐字如此），机框展示走 Frame。
	if got := data["Frame"]; got != "MA5603T" {
		t.Fatalf("Frame = %v, want MA5603T（PRODUCT 行 → 机框）", got)
	}
	if got := data["OLTRunTime"]; got != "42天7小时30分9秒" {
		t.Fatalf("OLTRunTime = %v, want 42天7小时30分9秒", got)
	}
	// 3) 主控槽 6，Type=control，且不下钻端口（PortNum=0）。
	boards, _ := data["OltBoardInfo"].([]any)
	var controlBoard, userBoard map[string]any
	for _, b := range boards {
		bm, _ := b.(map[string]any)
		if bm["Id"] == "6" {
			controlBoard = bm
		}
		if bm["Id"] == "4" {
			userBoard = bm
		}
	}
	if controlBoard == nil {
		t.Fatalf("缺少主控板(Id=6): %v", boards)
	}
	if got := controlBoard["Type"]; got != "control" {
		t.Fatalf("主控 Type = %v, want control", got)
	}
	if got := controlBoard["PortNum"].(float64); got != 0 {
		t.Fatalf("主控不应下钻端口: PortNum=%v", got)
	}
	// 4) 在线业务板（槽 4）正常下钻出 3 个 PON 口。
	if userBoard == nil {
		t.Fatalf("缺少在线业务板(Id=4): %v", boards)
	}
	if ports, _ := userBoard["PonInfos"].([]any); len(ports) != 3 {
		t.Fatalf("在线业务板 PON 口 = %v, want 3", userBoard["PonInfos"])
	}
	// 5) 命令面：发了裸 `display version`（且带一次补回车的空命令，见 versionPrompted）；
	//    **没有**发 `display version 0/6`。
	cmds := server.Commands()
	if !containsCommand(cmds, "display version") {
		t.Fatalf("未下发裸 display version，命令=%v", cmds)
	}
	if containsCommand(cmds, "display version 0/6") {
		t.Fatalf("不应再发 display version 0/6（机框/运行时间取不到），命令=%v", cmds)
	}
}

func containsCommand(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
