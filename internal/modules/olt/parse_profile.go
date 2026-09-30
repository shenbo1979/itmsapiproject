package olt

import (
	"strconv"
	"strings"
)

// 本文件搬运 DBA/线路模板的配置解析（/switchProject/switchmain/olthandler.go 2138 行起）。
//
// 共用工具 oltConfigurationLines / oltConfigurationSection / SubStringBetween 已在 parse_vlan.go，
// 这里只补充 oltCommandArgument 与 DBA 模板解析；线路模板（parseOLTLineProfiles）待后续批次迁入。

// oltCommandArgument 取某行命令里指定关键字后面的参数值（老实现同名函数逐字搬运）。
// 支持 `profile-name "含空格名称"` 的引号包裹形式；找不到返回空串。
func oltCommandArgument(line, keyword string) string {
	fields := strings.Fields(line)
	for index, field := range fields {
		if !strings.EqualFold(field, keyword) || index+1 >= len(fields) {
			continue
		}
		remainder := strings.TrimSpace(strings.SplitN(line, field, 2)[1])
		if strings.HasPrefix(remainder, `"`) {
			if end := strings.Index(remainder[1:], `"`); end >= 0 {
				return remainder[1 : end+1]
			}
		}
		return fields[index+1]
	}
	return ""
}

// oltDBACommandLines 把 `[global-config]` 段的行折叠为「一条 DBA 命令一行」的逻辑行，
// 兼容设备把一条 dba-profile 命令跨多行回显的情况（例如 `type3 assure 20480` 与
// `max 51200` 分属两行）。
//
// 折叠规则：
//   - 以 `dba-profile add` 开头的行开启（并终结前一条）一条新命令；
//   - 其后紧跟、且以 DBA 参数关键字（`type*` / `fix` / `assure` / `max`）打头的行，认为属于同一命令 → 并入前一逻辑行；
//   - 其余行（`ont-*`、空行、`#`、`[` 等）终结当前命令，单独保留不参与 DBA 解析。
func oltDBACommandLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	pending := ""
	flush := func() {
		if strings.TrimSpace(pending) != "" {
			out = append(out, pending)
		}
		pending = ""
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		fields := strings.Fields(line)
		if len(fields) == 0 {
			flush()
			continue
		}
		if strings.EqualFold(fields[0], "dba-profile") && len(fields) > 1 && strings.EqualFold(fields[1], "add") {
			flush()
			pending = line
			continue
		}
		if pending != "" && dbaParameterContinuation(line) {
			pending += " " + line
			continue
		}
		// 其余行（ont-* 等）本身不是 dba-profile 续行：先收束当前待定命令，
		// 再把这行原样作为独立逻辑行保留（不丢任何配置行）。
		flush()
		out = append(out, line)
	}
	flush()
	return out
}

// dbaParameterContinuation 判断一行是否是一条 dba-profile 命令的续行。
// 命中三类：
//   - 行首是 DBA 参数关键字（`type*` / `fix` / `assure` / `max`）——一整段分行的常规情况；
//   - 行首是纯数字——带宽度值被换行到下一行的情况（如 `... max` 后换行再 `51200`）。
func dbaParameterContinuation(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(fields[0])
	if strings.HasPrefix(first, "type") {
		return true
	}
	switch first {
	case "fix", "assure", "max":
		return true
	}
	// 数字打头：上一行结尾的关键字（如 `max`）把数值留到了本行。
	// 仅当确实在折叠一条 dba-profile 命令时才可能进到这里（见 oltDBACommandLines）。
	if _, err := strconv.Atoi(fields[0]); err == nil {
		return true
	}
	return false
}

// parseOLTDBAProfiles 从「已保存配置」的 `[global-config]` 段解析全部 DBA 模板。
//
// 与老实现（olthandler.go 2167 行）逐行一致的规则：
//   - 只认 `dba-profile add` 且带 `profile-id` 的行；设备跨行回显的命令先折叠成一行；
//   - profile-id 必须能转成整数且在 10..512 闭区间（越界跳过）；
//   - `Type` 取字段里以 "type" 前缀打头的那个（type1..type5）；
//   - `Fix` / `Assure` / `Max` 分别取 `fix` / `assure` / `max` 关键字后取值，解析失败留 0。
//
// 返回结构 DBAProfileInfo（字段与老实现 parseOLTDBAProfiles 产出的 map 键名一致）。
func parseOLTDBAProfiles(configuration string) []DBAProfileInfo {
	profiles := make([]DBAProfileInfo, 0)
	for _, logicalLine := range oltDBACommandLines(oltConfigurationSection(oltConfigurationLines(configuration), "global-config")) {
		line := strings.TrimSpace(logicalLine)
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.EqualFold(fields[0], "dba-profile") || !strings.EqualFold(fields[1], "add") {
			continue
		}
		id, err := strconv.Atoi(oltCommandArgument(line, "profile-id"))
		if err != nil || id < 10 || id > 512 {
			continue
		}
		profile := DBAProfileInfo{
			Id:     id,
			Name:   strings.Trim(oltCommandArgument(line, "profile-name"), `"`),
			Type:   "",
			Fix:    0,
			Assure: 0,
			Max:    0,
		}
		for _, field := range fields {
			if strings.HasPrefix(strings.ToLower(field), "type") {
				profile.Type = field
				break
			}
		}
		for _, item := range []struct{ keyword, field string }{{"fix", "Fix"}, {"assure", "Assure"}, {"max", "Max"}} {
			if value, err := strconv.Atoi(oltCommandArgument(line, item.keyword)); err == nil {
				switch item.field {
				case "Fix":
					profile.Fix = value
				case "Assure":
					profile.Assure = value
				case "Max":
					profile.Max = value
				}
			}
		}
		profiles = append(profiles, profile)
	}
	return profiles
}
