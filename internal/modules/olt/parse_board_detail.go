package olt

import (
	"fmt"
	"strings"
)

// 板卡明细解析（parse_board.go 的续篇）：单板端口、板卡总览。
//
// 与老实现的等价改写：老实现在 parseOLTBoardSlotDetail 与 collectOltCurrentConfig 里
// **各写了一遍完全相同的端口解析循环**，这里合并为 fillOLTBoardPorts（语义不变）。

// oltBoardDetailReader 按需下钻单板：真机实现发 `display temperature 0/<slot>`（仅在概览缺温度时）
// 与 `display board 0/<slot>`；单测可注入假实现。返回空串表示读失败（老实现忽略错误继续）。
type oltBoardDetailReader func(boardID string, needTemperature bool) (temperature, slotmsg string)

// fillOLTBoardPorts 解析单板回显里的 PON/GE 端口列表，并写回 PortNum/PonType。
func fillOLTBoardPorts(boardinfo *OltBoardInfo, slotmsg string) {
	if boardinfo == nil {
		return
	}
	var portnum int
	var pontype string
	for _, linestr := range splitOLTLines(slotmsg) {
		if !isNumericLine(linestr) {
			continue
		}
		fields := strings.Fields(linestr)
		if len(fields) < 2 {
			continue
		}
		slotType := strings.TrimSpace(fields[1])
		if strings.Contains(slotType, "PON") || strings.Contains(slotType, "GE") {
			portnum++
			pontype = slotType
		}
		if strings.Contains(slotType, "PON") {
			fsp := fmt.Sprintf("0/%s/%s", boardinfo.Id, fields[0])
			portstatus := "Offline"
			tmpstatus := fields[len(fields)-1]
			if strings.Contains(tmpstatus, "line") {
				portstatus = tmpstatus
			}
			portonts, porttotal := parseOLTPortONTStats(slotmsg, boardinfo.Id, fields[0])
			boardinfo.PonInfos = append(boardinfo.PonInfos, PonInfos{
				Id:         fields[0],
				FSP:        fsp,
				Type:       slotType,
				OntTotal:   porttotal,
				OntOnline:  portonts,
				PortStatus: portstatus,
			})
		}
		if strings.Contains(slotType, "GE") && len(fields) >= 10 {
			fsp := fmt.Sprintf("0/%s/%s", boardinfo.Id, fields[0])
			boardinfo.GeInfos = append(boardinfo.GeInfos, GeInfos{
				Id:         fields[0],
				FSP:        fsp,
				Type:       fields[1],
				NativeVLAN: fields[3],
				Speed:      fields[5],
				Duplex:     fields[6],
				Link:       fields[9],
			})
		}
	}
	boardinfo.PortNum = portnum
	boardinfo.PonType = pontype
}

// parseOLTBoardSlotDetail 解析单块板卡（`display board 0/<slot>`）的明细（老实现同名函数）。
func parseOLTBoardSlotDetail(slotID, slotmsg string, temperatureOverview map[string]string, cached *OltBoardInfo) OltBoardInfo {
	boardinfo := OltBoardInfo{
		Id:     strings.TrimSpace(slotID),
		Online: true,
	}
	lines := splitOLTLines(slotmsg)
	for _, linestr := range lines {
		if strings.Contains(linestr, "Board Name") && strings.Contains(linestr, ":") {
			boardinfo.Name = strings.TrimSpace(strings.SplitN(linestr, ":", 2)[1])
		}
		if strings.Contains(linestr, "Board Status") && strings.Contains(linestr, ":") {
			boardinfo.Status = strings.TrimSpace(strings.SplitN(linestr, ":", 2)[1])
		}
	}

	boardtype, boardRole := classifyOLTBoard(boardinfo.Name)
	boardinfo.Type = boardRole
	if tpval, ok := temperatureOverview[boardinfo.Id]; ok {
		boardinfo.Temperature = tpval
	}

	// 顺序修正（修掉老实现的一个 BUG）：老实现在解析端口**之前**调用 mergeCachedOltBoardDetail，
	// 此时 current.PonInfos 还是空的，于是把缓存里的 PON 口整段拷进来，随后端口循环再 append 一遍，
	// 结果同一块板卡的 PON 口会被列出两遍。这里改为"先解析本次回显、再补缓存缺失字段"，
	// 端口以本次真机数据为准，缓存只用于补齐缺失项（语义不变，仅去掉重复）。
	fillOLTBoardPorts(&boardinfo, slotmsg)
	if cached != nil {
		mergeCachedOltBoardDetail(&boardinfo, *cached, boardtype)
	}

	if boardtype != "" {
		boardinfo.Desc = fmt.Sprintf("%d口%s", boardinfo.PortNum, boardtype)
	}
	return boardinfo
}

// boardNeedsDrilldown 判断某块已在线板卡是否需要继续下钻（`display board 0/<slot>` + 可选温度）：
// 只有业务板/接口板（user/gic）且已开启深采集、且缓存里没有该板端口明细分块时，才需要下钻。
// 该判定是 parseOLTBoardOverview（串行下钻）与 collectDrillTargets（并行下钻）共用的唯一真源，
// 避免两处逻辑漂移。portDetailCached 语义与老实现一致：缓存里有 PonInfos 或 GeInfos 即视为已有明细。
func boardNeedsDrilldown(boardinfo OltBoardInfo, cached OltBoardInfo, hasCachedBoard, deepInspect bool) bool {
	portDetailCached := hasCachedBoard &&
		(len(cached.PonInfos) > 0 || len(cached.GeInfos) > 0)
	return boardinfo.Online && shouldInspectOLTBoardDetail(boardinfo.Type) && deepInspect && !portDetailCached
}

// drillTarget 是待下钻的一块板：保留解析总览时的快照（boardinfo/cached/需否温度），
// 供并行下钻阶段用独立只读会话采集后回填。
type drillTarget struct {
	boardinfo OltBoardInfo
	cached    OltBoardInfo
	hasCached bool
	needTemp  bool
	boardType string // boardTypeLabel，用于 Desc 拼写
}

// collectDrillTargets 从 `display board 0` 总览里收集需要下钻的板卡清单（只判定、不发命令，
// 与 parseOLTBoardOverview 的深采集判定用同一个 boardNeedsDrilldown）。串行下钻路径用不到它，
// 只有并行下钻（collectCurrentConfig 的 deepInspect 分支）才会调用。
func collectDrillTargets(boardmsg string, tpmap map[string]string, cachedByID map[string]OltBoardInfo, deepInspect bool) []drillTarget {
	var targets []drillTarget
	for _, linestr := range splitOLTLines(boardmsg) {
		fields := strings.Fields(linestr)
		if len(fields) == 0 {
			continue
		}
		slotID, ok := oltBoardSlotID(fields[0])
		if !ok {
			continue
		}
		if len(fields) < 3 {
			continue
		}
		statusstr := strings.ToLower(fields[len(fields)-1])
		boardinfo := OltBoardInfo{
			Id:     slotID,
			Name:   fields[1],
			Status: fields[2],
			Online: strings.Contains(statusstr, "online") || strings.Contains(statusstr, "normal"),
		}
		boardType, boardRole := classifyOLTBoard(boardinfo.Name)
		boardinfo.Type = boardRole
		if tpval, ok := tpmap[boardinfo.Id]; ok {
			boardinfo.Temperature = tpval
		}
		cachedBoard, hasCachedBoard := cachedByID[boardinfo.Id]
		if !boardNeedsDrilldown(boardinfo, cachedBoard, hasCachedBoard, deepInspect) {
			continue
		}
		needTemp := boardinfo.Temperature == ""
		targets = append(targets, drillTarget{
			boardinfo: boardinfo,
			cached:    cachedBoard,
			hasCached: hasCachedBoard,
			needTemp:  needTemp,
			boardType: boardType,
		})
	}
	return targets
}

// 返回板卡列表与 FrameTotal（行数）/ FrameCount（有效行数，用于机框文案）。
func parseOLTBoardOverview(boardmsg string, tpmap map[string]string, cachedByID map[string]OltBoardInfo,
	deepInspect bool, readDetail oltBoardDetailReader) ([]OltBoardInfo, int, int) {
	var boardinfos []OltBoardInfo
	var frameTotal, frameCount int
	for _, linestr := range splitOLTLines(boardmsg) {
		fields := strings.Fields(linestr)
		if len(fields) == 0 {
			continue
		}
		slotID, ok := oltBoardSlotID(fields[0])
		if !ok {
			continue
		}
		frameTotal++
		if len(fields) < 3 {
			continue
		}
		frameCount++
		statusstr := strings.ToLower(fields[len(fields)-1])
		onlineStatus := strings.Contains(statusstr, "online") || strings.Contains(statusstr, "normal")
		boardinfo := OltBoardInfo{
			Id:     slotID,
			Name:   fields[1],
			Status: fields[2],
			Online: onlineStatus,
		}
		boardtype, boardRole := classifyOLTBoard(boardinfo.Name)
		boardinfo.Type = boardRole
		if tpval, ok := tpmap[boardinfo.Id]; ok {
			boardinfo.Temperature = tpval
		}
		cachedBoard, hasCachedBoard := cachedByID[boardinfo.Id]
		// 提速（方案 A）：缓存里该板已有端口明细分块时，直接复用缓存端口，不再对每块板
		// 发 `display board 0/<slot>`——只刷新温度/状态，能显著缩短连接/刷新耗时。
		// 反之（新设备首次 / 缓存无该板端口明细）照常下钻全采，保证新设备拿到全部记录。
		drillDetail := boardNeedsDrilldown(boardinfo, cachedBoard, hasCachedBoard, deepInspect)
		// 不下钻（离线 / 非业务或接口板 / 非深采集 / 缓存已有端口明细分块）：用缓存补齐缺失字段后直接返回。
		if !drillDetail {
			if hasCachedBoard {
				mergeCachedOltBoardDetail(&boardinfo, cachedBoard, boardtype)
			}
			if boardtype != "" && boardinfo.Desc == "" {
				boardinfo.Desc = fmt.Sprintf("%d口%s", boardinfo.PortNum, boardtype)
			}
			boardinfos = append(boardinfos, boardinfo)
			continue
		}
		if boardinfo.Temperature == "" && readDetail != nil {
			temperature, _ := readDetail(boardinfo.Id, true)
			boardinfo.Temperature = temperature
		}
		var slotmsg string
		if readDetail != nil {
			_, slotmsg = readDetail(boardinfo.Id, false)
		}
		// 同 parseOLTBoardSlotDetail：先解析本次端口再补缓存，避免 PON 口重复（老实现 BUG）。
		fillOLTBoardPorts(&boardinfo, slotmsg)
		if hasCachedBoard {
			mergeCachedOltBoardDetail(&boardinfo, cachedBoard, boardtype)
		}
		if boardtype != "" {
			boardinfo.Desc = fmt.Sprintf("%d口%s", boardinfo.PortNum, boardtype)
		}
		boardinfos = append(boardinfos, boardinfo)
	}
	return boardinfos, frameTotal, frameCount
}

// frameDescForTotal 返回机框规格描述（老实现 switch FrameTotal 的四个机型文案，逐字保持）。
func frameDescForTotal(frameTotal int) string {
	switch frameTotal {
	case 5:
		return fmt.Sprintf("支持%d块板卡,0-1 插槽为业务板,2/3 为主控,4/5 为电源板", frameTotal)
	case 13:
		return fmt.Sprintf("支持%d块板卡,0-5 插槽为业务板,6/7 为主控,8/9 为上联板,10/11 为电源板,12为通用接口板", frameTotal)
	case 21:
		return fmt.Sprintf("支持%d块板卡,1-6/9-16 插槽为业务板,7/8 为主控,17/18 为上联板,19/20 为电源板,0为通用接口板", frameTotal)
	case 23:
		return fmt.Sprintf("支持%d块板卡,1-8/11-18 插槽为业务板,9/10 为主控,19/20 为上联板,21/22 为电源板,0为通用接口板", frameTotal)
	default:
		return ""
	}
}
