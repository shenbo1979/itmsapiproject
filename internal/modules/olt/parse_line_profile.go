package olt

import (
	"strconv"
	"strings"
)

// 本文件搬运「线路模板（ONT line profile）」的配置解析（/switchProject/switchmain/olthandler.go
// 2191 行起 parseOLTLineProfiles），与 parse_profile.go 的 DBA 模板解析同属「DBA/线路模板」栏目。
//
// 老实现用两套互相独立、结果略有差异的解析（Act=0 的两处：2406 行起用 SubStringBetween 的
// 大写单条写法；2191 行 parseOLTLineProfiles 是更简洁的结构化写法），本实现采用 parseOLTLineProfiles
// 这一份：它覆盖面一致（tcont / gem add / gem mapping 直到 commit）且对新实现更友好。
//
// 约定：只认 `[global-config]` 段；`ont-lineprofile <type> profile-id <id> profile-name <name>` 起一条，
// 之后的 `tcont <id> dba-profile-id <dba>` / `gem add <id> eth tcont <tid>` /
// `gem mapping <prot> <to> vlan <vlan>` 行直到 `commit` 归属该条；`commit` 末尾本身不并入。

// parseOLTLineProfiles 从「已保存配置」的 `[global-config]` 段解析全部 ONT 线路模板
// （含每个模板的 T-CONT / DBA 绑定与 GEM 端口映射），返回顺序与老实现 parseOLTLineProfiles 一致。
func parseOLTLineProfiles(configuration string) []OntLineProfile {
	profiles := make([]OntLineProfile, 0)
	lines := oltConfigurationSection(oltConfigurationLines(configuration), "global-config")
	for index := 0; index < len(lines); {
		line := strings.TrimSpace(lines[index])
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.EqualFold(fields[0], "ont-lineprofile") || oltCommandArgument(line, "profile-id") == "" {
			index++
			continue
		}
		profile := OntLineProfile{
			Id:         oltCommandArgument(line, "profile-id"),
			Name:       strings.Trim(oltCommandArgument(line, "profile-name"), `"`),
			Type:       fields[1],
			GemMapping: make([]GemMapping, 0),
		}
		profileID, err := strconv.Atoi(profile.Id)
		if err != nil || profileID < 10 {
			index++
			continue
		}
		mappings := make(map[string]*GemMapping)
		mappingOrder := make([]string, 0)
		gemTcont := make(map[string]string)
		gems := make(map[string]*GemInfo)
		gemOrder := make([]string, 0)
		index++
		for index < len(lines) {
			entry := strings.TrimSpace(lines[index])
			if strings.EqualFold(entry, "commit") {
				index++
				break
			}
			entryFields := strings.Fields(entry)
			if len(entryFields) >= 4 && strings.EqualFold(entryFields[0], "tcont") {
				tcontID := entryFields[1]
				if _, exists := mappings[tcontID]; !exists {
					mappings[tcontID] = &GemMapping{
						TcontId:      tcontID,
						DbaProfileId: oltCommandArgument(entry, "dba-profile-id"),
						GemInfo:      make([]GemInfo, 0),
					}
					mappingOrder = append(mappingOrder, tcontID)
				}
			}
			if len(entryFields) >= 6 && strings.EqualFold(entryFields[0], "gem") && strings.EqualFold(entryFields[1], "add") {
				gemID, tcontID := entryFields[2], entryFields[5]
				gemTcont[gemID] = tcontID
				if _, exists := gems[gemID]; !exists {
					gems[gemID] = &GemInfo{GemPort: gemID}
					gemOrder = append(gemOrder, gemID)
				}
			}
			if len(entryFields) >= 6 && strings.EqualFold(entryFields[0], "gem") && strings.EqualFold(entryFields[1], "mapping") {
				gemID := entryFields[2]
				if _, exists := gems[gemID]; !exists {
					gems[gemID] = &GemInfo{GemPort: gemID}
					gemOrder = append(gemOrder, gemID)
				}
				gem := gems[gemID]
				if gem.GemToId != "" {
					gem.GemToId += ","
					gem.GemVlan += ","
				}
				gem.GemToId += entryFields[3]
				gem.GemVlan += entryFields[len(entryFields)-1]
			}
			index++
		}
		for _, gemID := range gemOrder {
			if mapping, exists := mappings[gemTcont[gemID]]; exists {
				mapping.GemInfo = append(mapping.GemInfo, *gems[gemID])
			}
		}
		for _, tcontID := range mappingOrder {
			profile.GemMapping = append(profile.GemMapping, *mappings[tcontID])
		}
		profiles = append(profiles, profile)
	}
	return profiles
}
