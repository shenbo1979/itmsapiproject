package olt

import (
	"fmt"
	"regexp"
	"strings"
)

// 本文件是 olthandler.go 中"纯解析"函数的逐字搬运（无 DB/文件/设备依赖，便于单测）：
//
//	parseOLTBoardSlotDetail / parseOLTPortONTStats / parseOLTTemperature* /
//	classifyOLTBoard(+BoardTypeMap) / mergeCached* / parseOLTConfiguredONTTotals
//
// 与老实现的等价改写（不改语义）：
//   - 老实现在逐行循环里用 regexp.MustCompile(`\s+`).Split(...)，这里用 strings.Fields(...)
//     （等价，且不再每行编译一次正则）；`^\d+\s+` 判定改为"首字段全为数字"；
//   - BoardTypeMap 从老工程 rpc_client.go 搬到本模块（它属于 OLT，放在交换机 RPC 文件里是历史错位）。

// boardTypeMap 与老实现 BoardTypeMap 逐条一致（板卡类型 → 可能的型号前缀）。
var boardTypeMap = map[string][]string{
	"主控板(SCU)":             {"SCUH", "SCUL", "SCUN", "SCUK", "SCUV"},
	"主控板(MCU)":             {"MCUD", "MCUE", "MCUD1"},
	"上行接口板":                {"GICF", "GICG", "GICK", "X1CA", "X2CA", "X2CS", "X2CK"},
	"通用接口板":                {"BIUA", "CITA", "CITD"},
	"电源板(PRT)":             {"PRTA", "PRTE", "PRTG"},
	"电源板(MPW)":             {"MPWC", "MPWD", "MPWE"},
	"SPU板":                 {"SPUA", "SPUB", "SPUF"},
	"以太网业务接入板":             {"ETHA", "ETHB"},
	"TDM业务处理板":             {"CSPA", "TOPA", "TOB"},
	"GPON业务板":              {"GPBC", "GPBD", "GPFD", "GPBH", "GPMD"},
	"XG-PON业务板":            {"XGBC", "XGBD"},
	"XGS-PON业务板":           {"XSBD"},
	"XG-PON&GPON Combo业务板": {"CGBD"},
	"EPON业务板":              {"EPBA", "EPBC", "EPBD", "EPSD", "EPFD"},
	"10G EPON业务板":          {"XEBD", "XEUD"},
	"P2P接口板":               {"OPFA", "OPGD", "OPGE"},
	"风扇监控板":                {"FCBB", "FCBC", "FCBH", "FCBI", "FMCA"},
	"环境监控板":                {"ESCA", "SPDA"},
	"扣板":                   {"CKMC", "CKMD", "M2XA", "O2CE", "CSSA", "NH1A", "EH1A", "BITSB", "SHUAB", "SHUI", "CKMA", "O2CS"},
}

// classifyOLTBoard 由板卡名推断"业务类型标签 + 角色"（角色决定是否继续下钻查端口）。
func classifyOLTBoard(boardName string) (boardTypeLabel, boardRole string) {
	filterName := strings.TrimSpace(boardName)
	if len(filterName) > 4 {
		filterName = filterName[4:]
	}
	for boardType, boardNames := range boardTypeMap {
		for _, name := range boardNames {
			if strings.Contains(name, filterName) {
				switch {
				case strings.Contains(boardType, "主控板"):
					return boardType, "control"
				case strings.Contains(boardType, "接口板"):
					return boardType, "gic"
				case strings.Contains(boardType, "业务板"):
					return boardType, "user"
				default:
					return boardType, ""
				}
			}
		}
	}
	return "", ""
}

// shouldInspectOLTBoardDetail 判断该板卡是否需要继续下钻（只有接口板/业务板才有端口明细）。
func shouldInspectOLTBoardDetail(boardRole string) bool {
	return boardRole == "user" || boardRole == "gic"
}

// isNumericLine 判断一行是否以数字字段开头（老实现用 `^\d+\s+` 正则）。
func isNumericLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	for _, char := range fields[0] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// oltBoardSlotID 把 `display board 0` 总览一行的**首字段**归一化为纯槽位号（Id）。
// 兼容华为真机两种排版：
//   - 纯槽位号："6"（假 OLT / 部分老格式）；
//   - F/S 格式："0/6" → "6"（Huawei MA5600/MA5603T 实测 `display board 0` 首列为 F/S，
//     形如 `0/4`、`0/6`、`0/8`）。若沿用纯数字判定会把这类行整体当非法而跳过，
//     导致 0 块板卡 + 找不到主控槽（版本采集兜底失效）。
//
// 返回的槽位号与温度概览键、`display board 0/<slot>` 用同一口径（纯槽号）。
func oltBoardSlotID(first string) (string, bool) {
	first = strings.TrimSpace(first)
	digitsOnly := func(s string) bool {
		if s == "" {
			return false
		}
		for _, char := range s {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	}
	if digitsOnly(first) {
		return first, true
	}
	if slash := strings.IndexByte(first, '/'); slash > 0 && slash < len(first)-1 {
		if digitsOnly(first[:slash]) && digitsOnly(first[slash+1:]) {
			return first[slash+1:], true
		}
	}
	return "", false
}

// parseOLTPortONTStats 从槽位回显里取某个 PON 口的在线/总 ONT 数（老实现同名函数）。
func parseOLTPortONTStats(slotmsg, slotID, portID string) (int, int) {
	pattern := regexp.MustCompile(fmt.Sprintf(`(?is)In port 0/\s*%s/%s\b[^\r\n]*`,
		regexp.QuoteMeta(strings.TrimSpace(slotID)), regexp.QuoteMeta(strings.TrimSpace(portID))))
	matched := pattern.FindString(slotmsg)
	if matched == "" {
		return 0, 0
	}
	onlineRE := regexp.MustCompile(`(?i)\bonline\s*:\s*(\d+)`)
	totalRE := regexp.MustCompile(`(?i)(?:the\s+)?total\s+of\s+onts\s+are\s*:\s*(\d+)`)

	var online, total int
	if groups := onlineRE.FindStringSubmatch(matched); len(groups) == 2 {
		online = atoiOrZero(groups[1])
	}
	if groups := totalRE.FindStringSubmatch(matched); len(groups) == 2 {
		total = atoiOrZero(groups[1])
	}
	return online, total
}

// parseOLTTemperatureOverview 解析 `display temperature 0` 概览（槽位号 → 温度值）。
func parseOLTTemperatureOverview(output string) map[string]string {
	result := make(map[string]string)
	if strings.TrimSpace(output) == "" {
		return result
	}
	re := regexp.MustCompile(`(?i)slotid\s*:\s*(\d+(?:/\d+)?)\s*.*?temperature\s*:\s*([+-]?\d+(?:\.\d+)?)\s*c`)
	for _, line := range splitOLTLines(output) {
		match := re.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		// 槽位兼容 F/S（`0/6`）与纯槽号（`6`）：与板卡总览（oltBoardSlotID）同一口径归一化为纯槽号，
		// 否则真机 `SlotID: 0/6` 时键会是 "0" 而非 "6"，与板卡 Id 对不上导致温度缺失。
		if slotID, ok := oltBoardSlotID(strings.TrimSpace(match[1])); ok {
			result[slotID] = strings.TrimSpace(match[2])
		}
	}
	return result
}

// parseOLTTemperatureDetail 解析单板温度（`display temperature 0/<slot>`）。
// 兼容两种实测格式：
//   - 标准格式 `Temperature : 41.5 C`（MA5800）
//   - MA5603T 格式 `The temperature of the board:  45C( 113F)`（温度数字紧跟单位 C，冒号前含
//     "of the board"）。取冒号后的首个数值，不强制要求 `\s*c` 后跟空格（避免 45C(...) 漏配）。
func parseOLTTemperatureDetail(output string) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	re := regexp.MustCompile(`(?i)temperature[^\r\n:]*:\s*([+-]?\d+(?:\.\d+)?)`)
	for _, line := range splitOLTLines(output) {
		match := re.FindStringSubmatch(line)
		if len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

// splitOLTLines 统一换行并逐行去空白（老实现反复 ReplaceAll("\r\n", "\n") 的等价写法）。
func splitOLTLines(output string) []string {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(line)
	}
	return lines
}

// atoiOrZero 解析纯数字（非法输入返回 0，与老实现忽略 strconv 错误的行为一致）。
func atoiOrZero(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	result := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0
		}
		result = result*10 + int(char-'0')
	}
	return result
}

// cloneOltBoardInfo 深拷贝板卡信息（切片字段单独复制，避免缓存被就地改写）。
func cloneOltBoardInfo(board OltBoardInfo) OltBoardInfo {
	cloned := board
	if len(board.PonInfos) > 0 {
		cloned.PonInfos = append([]PonInfos(nil), board.PonInfos...)
	}
	if len(board.GeInfos) > 0 {
		cloned.GeInfos = append([]GeInfos(nil), board.GeInfos...)
	}
	return cloned
}

// findOltBoardInfoByID 按槽位号在缓存里找板卡（老实现同名函数）。
func findOltBoardInfoByID(boardinfos []OltBoardInfo, slotID string) (OltBoardInfo, bool) {
	slotID = strings.TrimSpace(slotID)
	if slotID == "" {
		return OltBoardInfo{}, false
	}
	for _, boardinfo := range boardinfos {
		if strings.TrimSpace(boardinfo.Id) == slotID {
			return cloneOltBoardInfo(boardinfo), true
		}
	}
	return OltBoardInfo{}, false
}

// findOLTControlBoardSlot 已废弃：版本/机框/运行时间统一走裸 `display version`（见 collect.go），
// 不再需要从板卡概览找主控槽发 `display version 0/<主控槽>`。相关调用与测试已一并移除。

// mergeCachedOltBoardDetail 用缓存补齐本次采集缺的字段（老实现同名函数）。
func mergeCachedOltBoardDetail(current *OltBoardInfo, cached OltBoardInfo, boardTypeLabel string) {
	if current == nil {
		return
	}
	if current.Type == "" {
		current.Type = cached.Type
	}
	if current.Temperature == "" {
		current.Temperature = cached.Temperature
	}
	if current.PortNum == 0 {
		current.PortNum = cached.PortNum
	}
	if current.PonType == "" {
		current.PonType = cached.PonType
	}
	if len(current.PonInfos) == 0 && len(cached.PonInfos) > 0 {
		current.PonInfos = append([]PonInfos(nil), cached.PonInfos...)
	}
	if len(current.GeInfos) == 0 && len(cached.GeInfos) > 0 {
		current.GeInfos = append([]GeInfos(nil), cached.GeInfos...)
	}
	if current.Desc == "" {
		if cached.Desc != "" {
			current.Desc = cached.Desc
		} else if boardTypeLabel != "" {
			current.Desc = fmt.Sprintf("%d口%s", current.PortNum, boardTypeLabel)
		}
	}
}

// mergeCachedOltConfigMeta 用缓存补齐本次采集缺的设备元信息（老实现同名函数）。
func mergeCachedOltConfigMeta(current *OltCurrentConfig, cached OltCurrentConfig) {
	if current == nil {
		return
	}
	if current.ConnectTime == "" {
		current.ConnectTime = cached.ConnectTime
	}
	if current.OLTRunTime == "" {
		current.OLTRunTime = cached.OLTRunTime
	}
	if current.RebootTime == "" {
		current.RebootTime = cached.RebootTime
	}
	if current.Version == "" {
		current.Version = cached.Version
	}
	if current.Patch == "" {
		current.Patch = cached.Patch
	}
	if current.Product == "" {
		current.Product = cached.Product
	}
	if current.Frame == "" {
		current.Frame = cached.Frame
	}
	if current.FrameType == "" {
		current.FrameType = cached.FrameType
	}
	if current.DefaultRoute == "" {
		current.DefaultRoute = cached.DefaultRoute
	}
}
