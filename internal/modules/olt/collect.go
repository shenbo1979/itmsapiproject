package olt

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"itmsapiproject/internal/oltconn"
)

// 设备采集流程：老实现 connectOltTelnet + readOLTCommandUntil + collectOltCurrentConfig
// / collectSingleOltBoardInfo / collectOLTCurrentConfiguration 的等价搬运（全部只读命令）。
const (
	// oltConfigReadTimeout 是读 `display current-configuration` 的单轮读超时（老实现 2s）。
	oltConfigReadTimeout = 2 * time.Second
	// oltConfigCollectTimeout 是采集 `display current-configuration` 全文的**总兜底超时**。
	// 真机 MA5603T 的该命令输出很大、分段下发且段间停顿常超 280ms，`idlePrompts` 会因
	// 不断有新数据而反复清零、永远到不了 >oltMaxIdlePrompts 的收尾条件；若设备卡在某个
	// 交互/参数提示上（未回到 `(config)#`），`return` 结束行也永远不出现——这两个场景
	// 都会让老实现无限空转、占用会话写锁并拖死后续所有写入。绝对值上限保证采集必然收敛，
	// 超时即报错返回，由调用方（defer client.Close）释放会话锁与连接。
	oltConfigCollectTimeout = 30 * time.Second
	// oltTimeLayout 是老实现到处使用的本地时间格式。
	oltTimeLayout = "2006-01-02 15:04:05"
	// oltMaxDrillConcurrency 是失败槽位在同一主会话上串行重试的轮数（方案 B 最终形态）。
	// 并行只读会话实测在本设备（MA5603T）劣化：设备把多路 Telnet 登录串行化，多会话登录开销
	// 盖过下钻并行收益（K=4:19-23s、K=1:~14s，均劣于纯串行基线 ~9s），故收敛为主会话串行下钻，
	// 无额外登录。此常量仅作失败槽位同一连接上的兜底重试轮数。
	oltMaxDrillConcurrency = 1
)

// collectSavedConfiguration 采集 `display current-configuration` 全文（老实现同名函数）。
//
// 结束条件与老实现逐条一致：出现独立行 `return` 即结束；遇分页提示发空格翻页；
// 空回显或交互提示（`{ <cr>|... }:`）时补发空行，连续超过 10 次判定为未正常结束。
// 另增**总兜底超时** oltConfigCollectTimeout（30s）：真机该命令输出大且分段间停顿常超
// 280ms，`idlePrompts` 会被持续到达的数据反复清零、`return` 结束行又可能在设备未回到
// 提示符时永不出现——此上限保证采集必然收敛，超时即报错（见 collectSavedConfiguration 主循环）。
func (d *oltDevice) collectSavedConfiguration() (string, error) {
	return d.collectSavedConfigurationWithin(oltConfigCollectTimeout)
}

// collectSavedConfigurationWithin 是 collectSavedConfiguration 的实现；timeout 为该次采集的
// 总兜底上限（生产入口传 oltConfigCollectTimeout）。拆出 timeout 参数便于单测用很短的上限
// 快速验证「设备持续吐数据却永不结束」时会按总超时收敛而不死循环。
func (d *oltDevice) collectSavedConfigurationWithin(timeout time.Duration) (string, error) {
	if d == nil || d.session == nil {
		return "", fmt.Errorf("OLT connection is not initialized")
	}
	if err := d.session.Send("display current-configuration"); err != nil {
		return "", err
	}
	var output strings.Builder
	idlePrompts := 0
	collectDeadline := time.Now().Add(timeout)
	for {
		if time.Now().After(collectDeadline) {
			return "", fmt.Errorf("OLT current configuration collection timed out after %s", timeout)
		}
		chunk, err := d.session.Read(oltConfigReadTimeout)
		if err != nil {
			return "", err
		}
		output.WriteString(chunk)
		if hasOLTCurrentConfigurationReturn(output.String()) {
			break
		}
		if oltconn.HasPaging(chunk) {
			if err := d.session.Send(" "); err != nil {
				return "", err
			}
			idlePrompts = 0
			continue
		}
		if strings.TrimSpace(chunk) == "" || hasOLTInputPrompt(chunk) {
			idlePrompts++
			if idlePrompts > oltMaxIdlePrompts {
				return "", fmt.Errorf("OLT current configuration did not finish with return")
			}
			if err := d.session.Send(""); err != nil {
				return "", err
			}
			continue
		}
		idlePrompts = 0
	}
	configuration := normalizeOLTCurrentConfiguration(output.String())
	if configuration == "" {
		return "", fmt.Errorf("OLT returned an empty current configuration")
	}
	return configuration, nil
}

// refreshSavedConfiguration 连接设备刷新「已保存配置」并写入本模块文件目录（老实现同名函数）。
func (m *Module) refreshSavedConfiguration(cp ConnectParameters) (string, error) {
	if strings.TrimSpace(cp.IP) == "" {
		return "", fmt.Errorf("OLT IP is required")
	}
	device, err := m.connectDevice(cp)
	if err != nil {
		return "", err
	}
	defer device.Close()

	return m.persistSavedConfiguration(device, cp.IP)
}

// refreshSavedConfigurationInBackground 在独立会话上后台采集并持久化「已保存配置」，供写操作（如 ONT 线路模板新增/删除）
// 在写命令成功、已向客户端返回 success 之后调用，避免把一次可能长达 30s 的全量采集塞进请求响应内导致前端超时。
//
// 复用 tryStartConfigRefresh/finishConfigRefresh 的「同一台 OLT 同时只跑一个后台刷新」守护（与
// refreshCurrentConfigInBackground 同一口径），失败仅记日志，不影响本次写操作已返回的成功结果；
// 后续 Act=0 读取侧“表优先、文件兜底”会在缓存缺失/过旧时自然重采，保证一致性。
func (m *Module) refreshSavedConfigurationInBackground(cp ConnectParameters) {
	if !m.tryStartConfigRefresh(cp.IP) {
		m.logf("线路模板写后跳过后台刷新(%s)：已有刷新在进行", cp.IP)
		return
	}
	go func(cp ConnectParameters) {
		defer m.finishConfigRefresh(cp.IP)
		if _, err := m.refreshSavedConfiguration(cp); err != nil {
			m.logf("后台刷新已保存配置失败(%s): %v", cp.IP, err)
		}
	}(cp)
}

// persistSavedConfiguration 在已连接的会话上采集「已保存配置」全文并持久化（文件 + 入库）。
// 供两种场景复用：
//   - refreshSavedConfiguration：独立会话上采集（只读/兜底）。
//   - 终端用户写操作（Act=1/2/3）：在写命令完成后的同一会话上直接采集，
//     省去「先关会话、再重连登录、再采集」的这一趟整连接开销（P1 提速）。
//
// 返回采集到的配置文本（调用方如需直接使用）。
func (m *Module) persistSavedConfiguration(d *oltDevice, ip string) (string, error) {
	configuration, err := d.collectSavedConfiguration()
	if err != nil {
		return "", err
	}
	if err := m.writeSavedConfiguration(ip, configuration); err != nil {
		return "", err
	}
	// 同步入库（读取侧表优先、文件兜底）。
	if saveErr := m.store.SaveConfigText(ip, ConfigKindSaved, configuration, "", "", time.Now().Format(oltTimeLayout)); saveErr != nil {
		m.logf("保存 OLT 已保存配置入库失败(%s): %v", ip, saveErr)
	}
	return configuration, nil
}

// loadConfiguredONTTotals 读取"已保存配置"并统计各 PON 口已配置 ONT 数（老实现同名函数）。
func (m *Module) loadConfiguredONTTotals(ip string) map[string]int {
	content, _ := readFirstExistingFile([]string{filepath.Join(m.deviceDir(ip), "olt_cur_cfg.txt")})
	if strings.TrimSpace(content) == "" {
		content = m.loadSavedConfiguration(ip)
	}
	if strings.TrimSpace(content) == "" {
		return map[string]int{}
	}
	return parseOLTConfiguredONTTotals(content)
}

// collectSingleBoardInfo 采集单块板卡（`display temperature 0` + `display board 0/<slot>`），
// 等价老实现 collectSingleOltBoardInfo。
func (m *Module) collectSingleBoardInfo(cp ConnectParameters, slotID string, cached *OltBoardInfo) (OltBoardInfo, error) {
	device, err := m.connectDevice(cp)
	if err != nil {
		return OltBoardInfo{}, err
	}
	defer device.Close()

	tpmsg, _ := device.runCommand("display temperature 0")
	tpmap := parseOLTTemperatureOverview(tpmsg)

	slotmsg, sloterr := device.runCommand(fmt.Sprintf("display board 0/%s", strings.TrimSpace(slotID)))
	if sloterr != nil {
		return OltBoardInfo{}, fmt.Errorf("操作失败,获取板卡信息失败")
	}
	return parseOLTBoardSlotDetail(slotID, slotmsg, tpmap, cached), nil
}

// collectCurrentConfig 采集 OLT 当前配置（板卡 + 版本 + 温度 + ONT 统计）并写入模块缓存，
// 等价老实现 collectOltCurrentConfig。deepInspect=false 时只取概览（后台刷新用）。
func (m *Module) collectCurrentConfig(cp ConnectParameters, trace *oltTrace, cached *OltCurrentConfig, deepInspect bool) (OltCurrentConfig, error) {
	stageStart := time.Now()
	device, err := m.connectDevice(cp)
	trace.add("connect_telnet", time.Since(stageStart))
	if err != nil {
		return OltCurrentConfig{}, err
	}
	defer device.Close()

	var oltconfig OltCurrentConfig
	if cached != nil {
		mergeCachedOltConfigMeta(&oltconfig, *cached)
	}

	tpmap := make(map[string]string)
	if deepInspect || cached == nil {
		stageStart = time.Now()
		tpmsg, _ := device.runCommand("display temperature 0")
		trace.add("display_temperature_overview", time.Since(stageStart))
		tpmap = parseOLTTemperatureOverview(tpmsg)
	}

	stageStart = time.Now()
	boardmsg, boarderr := device.runCommand("display board 0")
	trace.add("display_board_overview", time.Since(stageStart))
	if boarderr != nil {
		return OltCurrentConfig{}, fmt.Errorf("操作失败,获取板卡信息失败")
	}

	cachedBoardByID := make(map[string]OltBoardInfo)
	if cached != nil {
		for _, boardinfo := range cached.OltBoardInfo {
			cachedBoardByID[boardinfo.Id] = boardinfo
		}
	}
	// 方案 B（深采集下钻）：先解析总览收集需下钻的板卡清单，再在主会话上串行下钻并回填。
	// readDetail 传 nil 让 parseOLTBoardOverview 只做概览解析（需下钻板走"无明细"占位，稍后回填）。
	//
	// 不再开独立并行只读会话：真机（MA5603T）会把多路 Telnet 登录**串行化**，每条只读会话
	// 都要重付约 3s 登录握手，多并发登录开销反而盖过下钻的并行收益（实测 K=4:19-23s、K=1:~14s，
	// 均劣于纯串行基线 ~9s）。故收敛为"复用主会话"：主会话已连好、无额外登录，slot-by-slot
	// 串行发下钻命令，是最稳妥且最省时的路线。主会话为独占写会话，串行下钻不会走进读写锁自死锁。
	boardinfos, frameTotal, frameCount := parseOLTBoardOverview(boardmsg, tpmap, cachedBoardByID, deepInspect, nil)
	oltconfig.FrameTotal = frameTotal
	oltconfig.FrameCount = frameCount
	oltconfig.FrameDesc = frameDescForTotal(frameTotal)
	if deepInspect {
		targets := collectDrillTargets(boardmsg, tpmap, cachedBoardByID, true)
		if len(targets) > 0 {
			results := m.drillDownOnMainSession(device, targets, trace)
			applyDrillResults(&boardinfos, results)
		}
	}
	if len(boardinfos) > 0 {
		oltconfig.OltBoardInfo = boardinfos
	}

	// 版本/机框/运行时长采集。统一使用裸 `display version`，**不再发 `display version 0/<主控槽>`**。
	//
	// 背景：
	//   - `display version 0/<主控槽>` 只回显**主控板软件版本**（Software Version 行），
	//     不含机框（PRODUCT / Product）与运行时长（Uptime）——「OLT状态」页的机框与运行时间因此取不到。
	//   - 裸 `display version` 才带完整系统信息（软件版本 + PRODUCT + Uptime），是机框/运行时间的唯一来源。
	//   - MA5603T 在 config 模式下裸命令会先弹交互参数提示（`{ <cr>|backplane|frameid/slotid }:`），
	//     老实现因此规避裸命令；新实现 runCommand 走 oltconn.RunCommandUntilSignal：
	//     命中 `{ <cr>|... }:` 参数提示自动补一个回车（pushContinuation），提示闭合后设备回显
	//     完整版本文本并回到 `#` 提示符，命令结束判定依然成立——所以不再需要避开裸命令。
	//
	// 采集顺序仍置于板卡/温度之后：若提示未被识别，也会在采集末尾发，不再污染后续只读命令。
	// 无论版本是否取到，ConnectTime 都更新为本连接采集时间（表示“最近一次连接采集”）。
	oltconfig.ConnectTime = time.Now().Format(oltTimeLayout)
	applyOLTVersion := func(versionmsg string) {
		version, patch, frame, uptime := parseOLTVersionMessage(versionmsg)
		if version != "" {
			oltconfig.Version = version
		}
		if patch != "" {
			oltconfig.Patch = patch
		}
		if frame != "" {
			oltconfig.Frame = frame
		}
		if uptime != "" {
			oltconfig.OLTRunTime = uptime
		}
	}
	stageStart = time.Now()
	versionmsg, _ := device.runCommand("display version")
	trace.add("display_version", time.Since(stageStart))
	if strings.TrimSpace(versionmsg) != "" {
		applyOLTVersion(versionmsg)
	}

	configuredTotals := m.loadConfiguredONTTotals(cp.IP)
	oltconfig.OntOnlines, oltconfig.OntTotal = applyOLTConfiguredONTTotals(boardinfos, configuredTotals)
	oltconfig.SlotidRefreshTime = time.Now().Format(oltTimeLayout)

	stageStart = time.Now()
	if writeErr := m.saveCurrentConfig(cp.IP, oltconfig); writeErr != nil {
		m.logf("保存 OLT 当前信息文件失败(%s): %v", cp.IP, writeErr)
	}
	// 同步入库（2026-09-22 起文件数据入库表：采集结果写 tbOltDeviceSnapshot/Board/PonPort/GePort，
	// 读取侧"表优先、文件兜底"，见 filecache.go 与 docs/02 §4.2）。
	if saveErr := m.store.SaveDeviceSnapshot(cp.IP, oltconfig, "", "", time.Now().Format(oltTimeLayout)); saveErr != nil {
		m.logf("保存 OLT 设备快照入库失败(%s): %v", cp.IP, saveErr)
	}
	trace.add("save_current_config", time.Since(stageStart))
	return oltconfig, nil
}

// drillResult 是单块板卡并行下钻的产出（并行下钻回填 boardinfos 时用）。
type drillResult struct {
	temperature string
	slotmsg     string
	elapsed     time.Duration
}

// drillDownOnMainSession 在主会话上串行下钻全部待下钻槽位（方案 B 最终形态）。
//
// 演进背景：早期"并行只读会话池"（drillDownParallel）实测在本设备（MA5603T）劣化——设备把多路
// Telnet 登录串行化，每条只读会话都重付约 3s 登录，且并发登录触发失败后还要串行重试，多会话开销
// 反而盖过下钻的并行收益（K=4:19-23s、K=1:~14s，均劣于纯串行基线 ~9s）。因此改为主会话复用：
// 主会话已连好、零额外登录，slot-by-slot 串行发下钻命令，即 OLT 会话本来的串行交互模式。
//
// 并发语义：主会话是独占写会话（lockSession write），本函数在同一 goroutine 内串行跑完，
// 不另开只读会话，故不存在 RWMutex 自我死锁风险；返回时 device 保持存活，由调用方统一 Close。
//
// 失败处理：单个槽位下钻失败（runCommand 出错）只记空结果、不中断整批；对未解析出任何端口的
// 槽位在同一连接上重试（至多 oltMaxDrillConcurrency 轮），极端情况才保留 0 口，其余都正常回填。
func (m *Module) drillDownOnMainSession(device *oltDevice, targets []drillTarget, trace *oltTrace) map[string]drillResult {
	results := make(map[string]drillResult)
	if device == nil || device.session == nil {
		return results
	}
	ordered := orderedBySlot(targets)
	for _, target := range ordered {
		res := m.drillTargetOnSession(device, target, trace)
		results[target.boardinfo.Id] = res
	}
	// 串行补齐：对未解析出任何端口的槽位在同一会话上重试一轮（无新会话、无额外登录）。
	// 用"是否解析出端口"而非"slotmsg 是否为空"作失败信号——握手残留也会产生非空但无有效
	// 端口行的回显。待下钻板卡均为 PON/GPON/GE 口板（必有端口），解析出 0 口即可判失败。
	for pass := 0; pass < oltMaxDrillConcurrency; pass++ {
		retried := false
		for _, target := range ordered {
			if res, ok := results[target.boardinfo.Id]; ok && drillResultHasPorts(res) {
				continue // 已成功
			}
			results[target.boardinfo.Id] = m.drillTargetOnSession(device, target, trace)
			retried = true
		}
		if !retried {
			break
		}
	}
	return results
}

// drillResultHasPorts 报告一次下钻结果是否解析出至少 1 个端口。
// 判定口径与 applyDrillResults 回填一致（fillOLTBoardPorts 产出的 PortNum>0）。
func drillResultHasPorts(res drillResult) bool {
	if res.slotmsg == "" {
		return false
	}
	probe := OltBoardInfo{}
	fillOLTBoardPorts(&probe, res.slotmsg)
	return probe.PortNum > 0
}

// drillTargetOnSession 用一条**已连好**的会话下钻单块目标板（可选温度 + 端口）。
// 不负责建连/断开，由调用方传入会话；主会话串行下钻复用它逐槽位发命令，避免每槽位重付 Telnet 登录。
func (m *Module) drillTargetOnSession(device *oltDevice, target drillTarget, trace *oltTrace) drillResult {
	temperature := ""
	if target.needTemp {
		stageStart := time.Now()
		if msg, runErr := device.runCommand(fmt.Sprintf("display temperature 0/%s", target.boardinfo.Id)); runErr == nil {
			temperature = parseOLTTemperatureDetail(msg)
		}
		trace.add("display_temperature_slot", time.Since(stageStart))
	}
	stageStart := time.Now()
	slotmsg, _ := device.runCommand(fmt.Sprintf("display board 0/%s", target.boardinfo.Id))
	slotElapsed := time.Since(stageStart)
	trace.add("display_board_slot", slotElapsed)
	return drillResult{temperature: temperature, slotmsg: slotmsg, elapsed: slotElapsed}
}

// orderedBySlot 按槽位号升序排列待下钻目标，使串行重试顺序稳定可预期。
func orderedBySlot(targets []drillTarget) []drillTarget {
	out := append([]drillTarget(nil), targets...)
	sort.SliceStable(out, func(i, j int) bool {
		return slotIDLess(out[i].boardinfo.Id, out[j].boardinfo.Id)
	})
	return out
}

// slotIDLess 数值优先的槽位号比较（"4" < "10"）。
func slotIDLess(a, b string) bool {
	ai, aErr := strconv.Atoi(a)
	bi, bErr := strconv.Atoi(b)
	if aErr == nil && bErr == nil {
		return ai < bi
	}
	return a < b
}

// applyDrillResults 把并行下钻的端口/温度结果回填到 boardinfos 对应槽位。
// 口径与 parseOLTBoardSlotDetail 一致：先清零可能被缓存合并进来的端口，再用本次真机回显填充端口并
// 补 Desc，保证并行路径与旧串行路径产出的端口明细一致（本次真机优先、不重复）。
// 空 slotmsg 表示该槽位采集失败，保留概览/缓存原始值。
func applyDrillResults(boardinfos *[]OltBoardInfo, results map[string]drillResult) {
	if boardinfos == nil || len(results) == 0 {
		return
	}
	for i := range *boardinfos {
		board := &(*boardinfos)[i]
		res, ok := results[board.Id]
		if !ok || res.slotmsg == "" {
			continue
		}
		if res.temperature != "" {
			board.Temperature = res.temperature
		}
		board.PonInfos = nil
		board.GeInfos = nil
		board.PortNum = 0
		board.PonType = ""
		fillOLTBoardPorts(board, res.slotmsg)
		boardtype, _ := classifyOLTBoard(board.Name)
		if boardtype != "" {
			board.Desc = fmt.Sprintf("%d口%s", board.PortNum, boardtype)
		} else if board.Desc == "" {
			board.Desc = fmt.Sprintf("%d口%s", board.PortNum, board.Name)
		}
	}
}
