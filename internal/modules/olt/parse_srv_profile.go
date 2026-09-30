package olt

import (
	"strconv"
	"strings"
)

// 本文件搬运 ONT 业务模板 / TR069 管理模板的配置解析（/switchProject/switchmain/olthandler.go
// 2659 行起「ONT业务模板」、2960 行起「TR069管理模板」，第三批迁入）。
//
// 解析输入都是「已保存配置」的 `[global-config]` 段（oltConfigurationSection），
// 与 parseOLTLineProfiles 同一读路径（表优先、文件兜底、未缓存时连设备刷新）。

// parseOLTSrvProfiles 从「已保存配置」的 `[global-config]` 段解析全部 ONT 业务模板。
//
// 行结构（与老实现 parseOLTSrvProfiles 逐字一致的规则）：
//   - 模板行：`ont-srvprofile <type> profile-id <id> profile-name <name>`；
//   - 模板行之后、`commit` 之前是同模板的子命令：`ont-port pots <N> eth <M>`（电话口/网口数量）、
//     逐条 `port vlan eth <eth> translation <vlan>`（网口 → 业务 VLAN 映射）；
//   - `commit` 结束当前模板，`#` 结束整个区段。
//
// 返回结构 OntSrvProfileInfo（Id/Name/Type/Pots/Eths + SrvVlan[]，字段与老前端
// chunk-ont_tr069Tem.js 渲染层级一致）。Id 只要能转成正整数即纳入（老实现仅 `tmpid > 0`）。
func parseOLTSrvProfiles(configuration string) []OntSrvProfileInfo {
	lines := oltConfigurationSection(oltConfigurationLines(configuration), "global-config")
	profiles := make([]OntSrvProfileInfo, 0)
	for index := 0; index < len(lines); {
		line := strings.TrimSpace(lines[index])
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.EqualFold(fields[0], "ont-srvprofile") ||
			oltCommandArgument(line, "profile-id") == "" {
			index++
			continue
		}
		profile := OntSrvProfileInfo{
			Id:      oltCommandArgument(line, "profile-id"),
			Name:    strings.Trim(oltCommandArgument(line, "profile-name"), `"`),
			Type:    fields[1],
			SrvVlan: make([]SrvVlan, 0),
		}
		index++
		for index < len(lines) {
			entry := strings.TrimSpace(lines[index])
			if strings.EqualFold(entry, "commit") {
				index++
				break
			}
			entryFields := strings.Fields(entry)
			if len(entryFields) >= 2 && strings.EqualFold(entryFields[0], "ont-port") {
				// 网口数量（eth）/ 电话口数量（pots）按关键字取：老实现把 eth→Eths、pots→Pots，
				// 老壳表单曾把两者错位（网口数量存到 Pots、电话口数量存到 Eths），这里按语义修正。
				if pots := oltCommandArgument(entry, "pots"); pots != "" {
					profile.Pots = pots
				}
				if eths := oltCommandArgument(entry, "eth"); eths != "" {
					profile.Eths = eths
				}
			}
			if len(entryFields) >= 5 && strings.EqualFold(entryFields[0], "port") &&
				strings.EqualFold(entryFields[1], "vlan") {
				// `port vlan eth <eth> translation <vlan>`：VLAN 取 translation 关键字后值，
				// eth 编号取 translation 前一个字段（下标 3；下标 2 是关键字 "eth"）。
				if vlan := oltCommandArgument(entry, "translation"); vlan != "" && len(entryFields) >= 4 {
					profile.SrvVlan = append(profile.SrvVlan, SrvVlan{Eth: entryFields[3], Vlan: vlan})
				}
			}
			index++
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

// parseOLTTR069Profiles 从「已保存配置」的 `[global-config]` 段解析全部 TR069 管理模板。
//
// 行结构：`ont tr069-server-profile add profile-id <id> profile-name <name> url <url>
// user <user> <pass> auth-realm auth`。解析规则与老实现 parseOLTTR069Profiles 一致：
//   - 必须以 `ont tr069-server-profile add` 开头且同时带 profile-id / url / user（否则跳过）；
//   - 只回 Id/Name/AcsUrl/AcsUserName，**不回密码**（设备配置里的口令不回显，且老契约就不回）。
func parseOLTTR069Profiles(configuration string) []Tr069ProfileInfo {
	profiles := make([]Tr069ProfileInfo, 0)
	for _, rawLine := range oltConfigurationSection(oltConfigurationLines(configuration), "global-config") {
		line := strings.TrimSpace(rawLine)
		fields := strings.Fields(line)
		if len(fields) < 7 || !strings.EqualFold(fields[0], "ont") ||
			!strings.EqualFold(fields[1], "tr069-server-profile") || !strings.EqualFold(fields[2], "add") {
			continue
		}
		id := oltCommandArgument(line, "profile-id")
		if id == "" || oltCommandArgument(line, "url") == "" || oltCommandArgument(line, "user") == "" {
			continue
		}
		profiles = append(profiles, Tr069ProfileInfo{
			Id:          id,
			Name:        strings.Trim(oltCommandArgument(line, "profile-name"), `"`),
			AcsUrl:      strings.Trim(oltCommandArgument(line, "url"), `"`),
			AcsUserName: strings.Trim(oltCommandArgument(line, "user"), `"`),
		})
	}
	return profiles
}

// srvProfileIDIsValid 判断业务模板 Id 是否为合法闭区间（1-8192）。
func srvProfileIDIsValid(id int) bool { return id >= 1 && id <= 8192 }

// tr069ProfileIDIsValid 判断 TR069 模板 Id 是否为合法闭区间（1-32）。
func tr069ProfileIDIsValid(id int) bool { return id >= 1 && id <= 32 }

// clampSrvPortCount 把电话口/网口数量收敛到合法区间（Pots 0-32、Eths 0-24；老实现也是 clamp）。
func clampSrvPortCount(value int, max int) int {
	if value < 0 {
		return 0
	}
	if value > max {
		return max
	}
	return value
}

// srvVlanList 把业务 VLAN 输入拆成独立 VLAN 号列表：兼容逗号与换行/斜杠分隔
// （老壳表单把换行转逗号、老实现按 `/` 分段后取第一段；这里统一按分隔符全拆，
// 每一个业务 VLAN 对应一个网口，修正老实现「只取第一段」丢弃其余段的缺陷）。
func srvVlanList(vlan string) []string {
	out := make([]string, 0)
	for _, field := range strings.FieldsFunc(vlan, func(r rune) bool {
		return r == ',' || r == '/' || r == '\n' || r == '\r'
	}) {
		value := strings.TrimSpace(field)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

// srvVlanIsValid 判断单个业务 VLAN 号是否合法（1-4094，与线路模板同一口径）。
func srvVlanIsValid(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n >= 1 && n <= 4094
}
