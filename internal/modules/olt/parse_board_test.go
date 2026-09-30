package olt

import "testing"

// TestClassifyOLTBoard 覆盖板卡型号 → (业务标签, 角色) 的映射（BoardTypeMap 逐条来自老实现）。
func TestClassifyOLTBoard(t *testing.T) {
	cases := []struct {
		name      string
		wantLabel string
		wantRole  string
	}{
		{"H901SCUN", "主控板(SCU)", "control"},
		{"H901GPBD", "GPON业务板", "user"},
		{"H901XGBC", "XG-PON业务板", "user"},
		{"H901GICF", "上行接口板", "gic"},
		{"H901PRTE", "电源板(PRT)", ""},
		{"H901UNKNOWN", "", ""},
	}
	for _, item := range cases {
		label, role := classifyOLTBoard(item.name)
		if label != item.wantLabel || role != item.wantRole {
			t.Fatalf("classifyOLTBoard(%s) = (%s,%s), want (%s,%s)",
				item.name, label, role, item.wantLabel, item.wantRole)
		}
	}
	if !shouldInspectOLTBoardDetail("user") || !shouldInspectOLTBoardDetail("gic") {
		t.Fatal("业务板/接口板应下钻")
	}
	if shouldInspectOLTBoardDetail("control") || shouldInspectOLTBoardDetail("") {
		t.Fatal("主控板/未知型号不应下钻")
	}
}

// TestParseTemperatureAndVersion 覆盖温度与版本回显解析（含 uptime 中文化）。
func TestParseTemperatureAndVersion(t *testing.T) {
	overview := parseOLTTemperatureOverview("\r\n  SlotID : 0    Temperature : 36 C\r\n  SlotID : 1    Temperature : 41.5 C\r\n")
	if overview["0"] != "36" || overview["1"] != "41.5" {
		t.Fatalf("温度概览 = %v", overview)
	}
	// MA5603T 真机温度槽位可能带 F/S 前缀（`SlotID : 0/6`）：应归一化为纯槽号 "6"（与板卡 Id 对齐）。
	if ov560 := parseOLTTemperatureOverview("\r\n  SlotID : 4    Temperature : 41 C\r\n  SlotID : 0/6  Temperature : 38 C\r\n"); ov560["4"] != "41" || ov560["6"] != "38" {
		t.Fatalf("MA5603T 温度概览(F/S 键) = %v, want {4:41, 6:38}", ov560)
	}
	if got := parseOLTTemperatureDetail("\r\n  Temperature : 47 C\r\n"); got != "47" {
		t.Fatalf("单板温度 = %q", got)
	}
	// MA5603T 实测格式：`The temperature of the board:  45C( 113F)`
	if got := parseOLTTemperatureDetail("\r\nThe temperature of the board:  45C( 113F)\r\n"); got != "45" {
		t.Fatalf("MA5603T 单板温度 = %q, want 45", got)
	}

	version, patch, frame, uptime := parseOLTVersionMessage(
		"  VERSION : MA5800V100R019C10\r\n  PATCH   : SPC200\r\n  PRODUCT : MA5800-X7\r\n" +
			"  Uptime is 12 day(s), 3 hour(s), 4 minute(s), 5 second(s)\r\n")
	if version != "MA5800V100R019C10" || patch != "SPC200" || frame != "MA5800-X7" {
		t.Fatalf("版本解析 = %q/%q/%q", version, patch, frame)
	}
	if uptime != "12天3小时4分5秒" {
		t.Fatalf("uptime = %q", uptime)
	}

	// MA5603T 实测：完整版本文本回显为大小写混合的 `Software Version:`，
	// 且同屏含 PCB/Basic(BIOS)/Logic/MAB 多行 Version —— 只应取 Software 行，不能误抓 BIOS 的 "216"。
	ver560, _, _, _ := parseOLTVersionMessage(
		"  Main Board: H801SCUN\r\n" +
			"  ---------------------------------------\r\n" +
			"  PCB  Version: H801SCUN  VER B\r\n" +
			"  Base     BIOS Version: 216\r\n" +
			"  Extended BIOS Version: 222\r\n" +
			"  Software      Version: MA5600V800R011C00\r\n" +
			"  Logic         Version: (U48)115\r\n" +
			"  MAB           Version: 0002\r\n")
	if ver560 != "MA5600V800R011C00" {
		t.Fatalf("MA5603T 软件版本 = %q, want MA5600V800R011C00", ver560)
	}
}

// TestFindOLTControlBoardSlot 已移除：版本/机框/运行时间统一走裸 `display version`（见 collect.go），
// `findOLTControlBoardSlot` 与 `display version 0/<主控槽>` 路径已废弃。

// TestParseBoardSlotDetail 覆盖单板明细：板名/状态/端口/端口 ONT 统计 + 缓存补齐。
func TestParseBoardSlotDetail(t *testing.T) {
	slotmsg := "\r\n" +
		"  Board Name    : H901GPBD\r\n" +
		"  Board Status  : Normal\r\n" +
		"  0      GPON       Online\r\n" +
		"  1      GPON       Online\r\n" +
		"  2      GE         0/1/2  10  1000M  full  0  0  0  up\r\n" +
		"  In port 0/1/0 the total of ONTs are: 8, online: 5\r\n"
	board := parseOLTBoardSlotDetail("1", slotmsg, map[string]string{"1": "41"}, nil)
	if board.Name != "H901GPBD" || board.Status != "Normal" || board.Type != "user" {
		t.Fatalf("板卡头部解析 = %+v", board)
	}
	if board.Temperature != "41" || board.PortNum != 3 || board.PonType != "GE" {
		// 老口径：PortNum 统计含 PON 与 GE 的端口行；PonType 取"最后见到的端口类型"，
		// 混合板卡（PON + GE）因此会落到 GE —— 这是老实现的原样行为，新实现不改。
		t.Fatalf("板卡端口统计 = %+v", board)
	}
	if len(board.PonInfos) != 2 || board.PonInfos[0].OntOnline != 5 || board.PonInfos[0].OntTotal != 8 {
		t.Fatalf("PON 口解析 = %+v", board.PonInfos)
	}
	if board.PonInfos[0].PortStatus != "Online" {
		t.Fatalf("PON 口状态 = %q", board.PonInfos[0].PortStatus)
	}
	// GE 行的列序假设（fields[3]=NativeVLAN、[5]=Speed、[6]=Duplex、[9]=Link）沿用老实现，
	// 真实列序需按 docs/09 §5 的 P0-2「真机只读录制」核对。
	if len(board.GeInfos) != 1 || board.GeInfos[0].NativeVLAN != "10" ||
		board.GeInfos[0].Speed != "full" || board.GeInfos[0].Link != "up" {
		t.Fatalf("GE 口解析 = %+v", board.GeInfos)
	}
	if board.Desc != "3口GPON业务板" {
		t.Fatalf("Desc = %q", board.Desc)
	}

	// 缓存补齐：本次回显没有端口时用缓存里的端口/描述
	cached := OltBoardInfo{PortNum: 2, PonType: "GPON", Desc: "2口GPON业务板", Temperature: "39"}
	merged := parseOLTBoardSlotDetail("1", "  Board Name : H901GPBD\r\n  Board Status : Normal\r\n",
		map[string]string{}, &cached)
	if merged.PortNum != 2 || merged.PonType != "GPON" || merged.Desc != "2口GPON业务板" || merged.Temperature != "39" {
		t.Fatalf("缓存补齐失败: %+v", merged)
	}
}

// TestParseBoardOverview 覆盖总览解析：角色判定、离线不下钻、在线业务板下钻。
func TestParseBoardOverview(t *testing.T) {
	boardmsg := "\r\n" +
		"  SlotID  BoardName  Status\r\n" +
		"  0       H901SCUN   Normal\r\n" +
		"  1       H901GPBD   Normal\r\n" +
		"  2       H901GPBD   Offline\r\n"
	detailCalls := 0
	readDetail := func(boardID string, needTemperature bool) (string, string) {
		detailCalls++
		return "45", "  Board Name : H901GPBD\r\n  0  GPON  Online\r\n"
	}
	boards, frameTotal, frameCount := parseOLTBoardOverview(boardmsg, map[string]string{}, nil, true, readDetail)
	if frameTotal != 3 || frameCount != 3 || len(boards) != 3 {
		t.Fatalf("总览计数 = %d/%d/%d", frameTotal, frameCount, len(boards))
	}
	if detailCalls != 2 { // 只对在线业务板下钻（温度 + 明细各一次）
		t.Fatalf("下钻次数 = %d, want 2（仅在线业务板）", detailCalls)
	}
	if boards[0].Type != "control" || boards[0].PortNum != 0 {
		t.Fatalf("主控板不应下钻: %+v", boards[0])
	}
	if boards[1].PortNum != 1 || boards[1].Temperature != "45" || !boards[1].Online {
		t.Fatalf("在线业务板 = %+v", boards[1])
	}
	if boards[2].Online || boards[2].PortNum != 0 {
		t.Fatalf("离线板卡不应下钻: %+v", boards[2])
	}
	if frameDescForTotal(5) == "" {
		t.Fatal("5 槽位机框应有描述文案")
	}
	if frameDescForTotal(3) != "" {
		t.Fatalf("未知机框不应有描述文案: %q", frameDescForTotal(3))
	}
}

// TestBoardNeedsDrilldown 覆盖并行下钻判定的唯一真源：在线业务/接口板 + 深采集 + 缓存无端口明细 → 下钻。
func TestBoardNeedsDrilldown(t *testing.T) {
	userOnline := OltBoardInfo{Id: "1", Type: "user", Online: true}
	if !boardNeedsDrilldown(userOnline, OltBoardInfo{}, false, true) {
		t.Fatal("在线业务板 + 深采集 + 无缓存 → 应下钻")
	}
	if boardNeedsDrilldown(userOnline, OltBoardInfo{}, false, false) {
		t.Fatal("非深采集 → 不应下钻")
	}
	offline := OltBoardInfo{Id: "1", Type: "user", Online: false}
	if boardNeedsDrilldown(offline, OltBoardInfo{}, false, true) {
		t.Fatal("离线板 → 不应下钻")
	}
	control := OltBoardInfo{Id: "0", Type: "control", Online: true}
	if boardNeedsDrilldown(control, OltBoardInfo{}, false, true) {
		t.Fatal("主控板 → 不应下钻")
	}
	// 缓存有端口明细（方案 A 提速）→ 不再下钻
	cachedWithPorts := OltBoardInfo{Id: "1", PonInfos: []PonInfos{{Id: "0"}}}
	if boardNeedsDrilldown(userOnline, cachedWithPorts, true, true) {
		t.Fatal("缓存已有端口明细 → 不应下钻（复用缓存提速）")
	}
}

// TestCollectDrillTargets 覆盖并行下钻收集：只收集需下钻的在线业务板。
func TestCollectDrillTargets(t *testing.T) {
	boardmsg := "\r\n" +
		"  0       H901SCUN   Normal\r\n" +
		"  1       H901GPBD   Normal\r\n" +
		"  2       H901GPBD   Offline\r\n"
	targets := collectDrillTargets(boardmsg, map[string]string{"1": "45"}, nil, true)
	if len(targets) != 1 {
		t.Fatalf("下钻目标数 = %d, want 1（仅在线业务板）", len(targets))
	}
	if targets[0].boardinfo.Id != "1" || targets[0].needTemp {
		t.Fatalf("目标 = %+v（温度概览已给，不应再单板取温）", targets[0])
	}
	// 温度概览缺该板时 → needTemp=true
	targets = collectDrillTargets(boardmsg, map[string]string{}, nil, true)
	if len(targets) != 1 || !targets[0].needTemp {
		t.Fatalf("缺温度时 needTemp 应为 true：%+v", targets)
	}
	// 深采集关闭 → 无下钻目标
	if got := collectDrillTargets(boardmsg, map[string]string{}, nil, false); len(got) != 0 {
		t.Fatalf("非深采集应无下钻目标：%+v", got)
	}
}

// TestApplyDrillResults 覆盖并行下钻回填：清掉缓存端口 → 用本次真机端口填充，失败槽位保留原值。
func TestApplyDrillResults(t *testing.T) {
	boards := []OltBoardInfo{
		{Id: "1", Name: "H901GPBD", Type: "user", Online: true},                                  // 待回填
		{Id: "2", Name: "H901GPBD", Type: "user", Online: true, PonInfos: []PonInfos{{Id: "9"}}}, // 失败，保留
	}
	results := map[string]drillResult{
		"1": {temperature: "50", slotmsg: "  Board Name : H901GPBD\r\n  0  GPON  Online\r\n  In port 0/1/0 the total of ONTs are: 8, online: 5\r\n"},
		// "2" 缺失 → 走失败保留分支
	}
	applyDrillResults(&boards, results)
	if boards[0].Temperature != "50" || len(boards[0].PonInfos) != 1 || boards[0].PonInfos[0].OntTotal != 8 {
		t.Fatalf("回填失败: %+v", boards[0])
	}
	if boards[0].Desc != "1口GPON业务板" {
		t.Fatalf("Desc = %q", boards[0].Desc)
	}
	if len(boards[1].PonInfos) != 1 || boards[1].PonInfos[0].Id != "9" {
		t.Fatalf("失败槽位不应被清空: %+v", boards[1])
	}
}

// TestParseBoardOverviewFSSlot 覆盖华为 MA5600/MA5603T 真机 `display board 0` 的 **F/S 首列**（如
// `0/4`、`0/6`）回显：此前解析用纯数字判定（isNumericLine）会把这类行整体当非法而跳过，导致
// 0 块板卡 + 找不到主控槽（版本采集兜底失效），正是「真机 shenbo(172.20.247.88) 连接成功但
// 版本未知、板卡 0 块」的根因。修复后应解析出板卡、归一化槽位号并命中主控槽。
func TestParseBoardOverviewFSSlot(t *testing.T) {
	boardmsg := "\r\n" +
		"  F/S     BoardName     Status\r\n" +
		"  -------------------------------------\r\n" +
		"  0/4     H805GPBD      Normal\r\n" +
		"  0/6     H801SCUN      Active_normal\r\n" +
		"  0/8     H801GICF      Normal\r\n" +
		"  0/18    PRTE          Normal\r\n"
	tpmap := map[string]string{"4": "41", "6": "38", "8": "35", "18": "30"}

	boards, frameTotal, frameCount := parseOLTBoardOverview(boardmsg, tpmap, nil, true, nil)
	if frameTotal != 4 || frameCount != 4 || len(boards) != 4 {
		t.Fatalf("F/S 总览计数 = %d/%d/%d, want 4/4/4", frameTotal, frameCount, len(boards))
	}
	// 槽位号应为"去掉 F/ 后的纯槽号"（与温度键/下钻/display board 0/<slot> 同口径）。
	if boards[0].Id != "4" || boards[0].Name != "H805GPBD" || boards[0].Type != "user" || boards[0].Temperature != "41" {
		t.Fatalf("F/S 板0 解析 = %+v", boards[0])
	}
	if boards[1].Id != "6" || boards[1].Name != "H801SCUN" || boards[1].Type != "control" || !boards[1].Online {
		t.Fatalf("F/S 板1(主控)解析 = %+v", boards[1])
	}
	if boards[2].Id != "8" || boards[2].Type != "gic" {
		t.Fatalf("F/S 板2(上行)解析 = %+v", boards[2])
	}
	// 电源板（PRTE）未知型号不应下钻，但应计入板卡数。
	if boards[3].Id != "18" || boards[3].Type != "" {
		t.Fatalf("F/S 板3(电源)解析 = %+v", boards[3])
	}

	// 下钻目标只收集在线业务/接口板（主控 6、电源 18 不下钻）。
	targets := collectDrillTargets(boardmsg, tpmap, nil, true)
	if len(targets) != 2 {
		t.Fatalf("F/S 下钻目标数 = %d, want 2（业务板+上行板）: %+v", len(targets), targets)
	}
	if targets[0].boardinfo.Id != "4" || targets[1].boardinfo.Id != "8" {
		t.Fatalf("F/S 下钻目标槽位 = %q/%q, want 4/8", targets[0].boardinfo.Id, targets[1].boardinfo.Id)
	}
}
