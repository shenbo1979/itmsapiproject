package olt

import (
	"fmt"
	"regexp"
	"strings"
)

// 配置文本相关的解析：版本回显、已保存配置里的 ONT 统计（老实现在 collectOltCurrentConfig /
// loadOLTConfiguredONTTotals 内联或同名函数）。

// parseOLTVersionMessage 解析 `display version`（老实现在 processOltCurrentConfig 内联）。
// uptime 按老口径做中文化替换（"Uptime is 12 day(s)" → "12天"）。
//
// 兼容两种主流的 `display version` 回显格式：
//   - 老式 MA5800/MA5800-X7：`VERSION : MA5800V100R019C10`（全大写 VERSION）；
//   - MA5603T：`Software      Version: MA5600V800R011C00`（大小写混合的 `Version:`，且同一回显里
//     还含 `PCB/Basic(BIOS)/Logic/MAB Version:` 等多行版本，必须只取带 `software` 关键字的软件版本行，
//     否则会把 "216"(BIOS) 一类误当成主软件版本）。
func parseOLTVersionMessage(output string) (version, patch, frame, uptime string) {
	if strings.TrimSpace(output) == "" {
		return "", "", "", ""
	}
	// 先在全部行里找“带 software 的版本行”（MA5600 风格），找到即取，不再因后续版本行覆盖。
	softwareVersionFound := false
	for _, linestr := range splitOLTLines(output) {
		lower := strings.ToLower(linestr)
		if strings.Contains(lower, "software") &&
			strings.Contains(lower, "version") && strings.Contains(linestr, ":") {
			version = strings.TrimSpace(strings.Split(linestr, ":")[1])
			softwareVersionFound = true
			break
		}
	}
	for _, linestr := range splitOLTLines(output) {
		if linestr == "" {
			continue
		}
		lower := strings.ToLower(linestr)
		// 未命中 software 行时才回退到大写 VERSION 格式（MA5800 风格）。
		if !softwareVersionFound &&
			strings.Contains(linestr, "VERSION") && strings.Contains(linestr, ":") {
			version = strings.TrimSpace(strings.Split(linestr, ":")[1])
		}
		if strings.Contains(lower, "patch") && strings.Contains(linestr, ":") {
			patch = strings.TrimSpace(strings.Split(linestr, ":")[1])
		}
		if strings.Contains(lower, "product") && strings.Contains(linestr, ":") {
			frame = strings.TrimSpace(strings.Split(linestr, ":")[1])
		}
		if strings.Contains(lower, "uptime") {
			filter := strings.NewReplacer(
				"Uptime is", "",
				"day(s)", "天",
				"hour(s)", "小时",
				"minute(s)", "分",
				"second(s)", "秒",
				",", "",
				" ", "",
			)
			uptime = filter.Replace(linestr)
		}
	}
	return version, patch, frame, uptime
}

// parseOLTConfiguredONTTotals 从「已保存配置」统计每个 PON 口已配置的 ONT 数
// （配置里 ont add 的行数与"应有总数"更接近，老实现同名函数）。
func parseOLTConfiguredONTTotals(content string) map[string]int {
	counts := make(map[string]int)
	if strings.TrimSpace(content) == "" {
		return counts
	}
	interfaceRE := regexp.MustCompile(`(?i)^interface\s+(gpon|epon)\s+(\d+/\d+)\s*$`)
	ontAddRE := regexp.MustCompile(`(?i)^ont\s+add\s+(\d+)\s+\d+\b`)
	currentSlot := ""

	for _, line := range splitOLTLines(content) {
		if line == "" {
			continue
		}
		if matches := interfaceRE.FindStringSubmatch(line); len(matches) == 3 {
			currentSlot = strings.TrimSpace(matches[2])
			continue
		}
		if strings.HasPrefix(line, "#") {
			currentSlot = ""
			continue
		}
		if currentSlot == "" {
			continue
		}
		if matches := ontAddRE.FindStringSubmatch(line); len(matches) == 2 {
			fsp := fmt.Sprintf("%s/%s", currentSlot, strings.TrimSpace(matches[1]))
			counts[fsp]++
		}
	}
	return counts
}

// applyOLTConfiguredONTTotals 用配置口径修正各 PON 口总数，并汇总在线/总数（老实现同名函数）。
func applyOLTConfiguredONTTotals(boardinfos []OltBoardInfo, configuredTotals map[string]int) (int, int) {
	var totalOnline, totalONT int
	for boardIndex := range boardinfos {
		for portIndex := range boardinfos[boardIndex].PonInfos {
			port := &boardinfos[boardIndex].PonInfos[portIndex]
			fsp := strings.TrimSpace(port.FSP)
			if configuredTotal, ok := configuredTotals[fsp]; ok && configuredTotal > port.OntTotal {
				port.OntTotal = configuredTotal
			}
			totalOnline += port.OntOnline
			totalONT += port.OntTotal
		}
	}
	return totalOnline, totalONT
}

// hasOLTCurrentConfigurationReturn 判断当前配置回显是否已结束（老实现同名函数）。
func hasOLTCurrentConfigurationReturn(output string) bool {
	for _, line := range splitOLTLines(output) {
		if strings.EqualFold(line, "return") {
			return true
		}
	}
	return false
}

// hasOLTInputPrompt 判断是否停在交互式提示（MA5600 的 `{ <cr>|... }:`，老实现同名函数）。
func hasOLTInputPrompt(output string) bool {
	trimmed := strings.TrimSpace(output)
	return strings.Contains(trimmed, "<cr>") || strings.HasSuffix(trimmed, "}:")
}

// normalizeOLTCurrentConfiguration 去掉回显头尾（Command:/display current-configuration 行），
// 与老实现逐行一致。
func normalizeOLTCurrentConfiguration(output string) string {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	lines := strings.Split(output, "\n")
	commandHeader := -1
	for index, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "command:") {
			commandHeader = index
		}
	}
	if commandHeader >= 0 {
		lines = lines[commandHeader+1:]
	}
	for index, line := range lines {
		if strings.Contains(strings.ToLower(line), "display current-configuration") {
			lines = lines[index+1:]
			break
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
