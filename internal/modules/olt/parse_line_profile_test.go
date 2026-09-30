package olt

import "testing"

// lineProfileSampleConfig 覆盖一条完整线路模板的解析：含 TCONT、DBA 绑定、两个 GEM 端口
// （一个带多映射、一个带多 VLAN）、一个越界 profile-id 与一段无关行（验证跳过与段隔离）。
const lineProfileSampleConfig = `hostname OLT
[global-config]
 ont-lineprofile gpon profile-id 10 profile-name "LineA"
 tcont 1 dba-profile-id 10
 gem add 0 eth tcont 1
 gem mapping 0 1 vlan 100
 gem mapping 0 2 vlan 200,300
 gem add 1 eth tcont 1
 gem mapping 1 1 vlan 400
 commit
 ont-lineprofile gpon profile-id 9 profile-name "TooSmall"
 commit
 some-other line
#
`

// TestParseOLTLineProfiles 覆盖：引号去除、TcontId/DbaProfileId/GemInfo 层级、多 GEM 顺序、越界与无关行跳过。
func TestParseOLTLineProfiles(t *testing.T) {
	got := parseOLTLineProfiles(lineProfileSampleConfig)
	if len(got) != 1 {
		t.Fatalf("解析出 %d 个模板，期望 1（id=9 越界、无关行跳过）: %v", len(got), got)
	}
	p := got[0]
	if p.Id != "10" || p.Name != "LineA" || p.Type != "gpon" {
		t.Fatalf("profile = %+v", p)
	}
	if len(p.GemMapping) != 1 {
		t.Fatalf("GemMapping 应为 1 条 TCONT，got %+v", p.GemMapping)
	}
	gm := p.GemMapping[0]
	if gm.TcontId != "1" || gm.DbaProfileId != "10" {
		t.Fatalf("GemMapping = %+v", gm)
	}
	if len(gm.GemInfo) != 2 {
		t.Fatalf("GemInfo 应为 2 个 GEM 端口，got %+v", gm.GemInfo)
	}
	g0 := gm.GemInfo[0]
	if g0.GemPort != "0" || g0.GemToId != "1,2" || g0.GemVlan != "100,200,300" {
		t.Fatalf("gem0 = %+v", g0)
	}
	g1 := gm.GemInfo[1]
	if g1.GemPort != "1" || g1.GemToId != "1" || g1.GemVlan != "400" {
		t.Fatalf("gem1 = %+v", g1)
	}
}

// TestParseOLTLineProfilesEmpty 覆盖空输入与不含 ont-lineprofile 的 global-config 段。
func TestParseOLTLineProfilesEmpty(t *testing.T) {
	if got := parseOLTLineProfiles(""); len(got) != 0 {
		t.Fatalf("空配置应返回 0 条，got %d", len(got))
	}
	if got := parseOLTLineProfiles("[global-config]\nsome-other line\n#\n"); len(got) != 0 {
		t.Fatalf("不含 ont-lineprofile 的行应返回 0 条，got %d", len(got))
	}
}

// TestParseOLTLineProfilesNoGemMapping 覆盖只有模板头、没有 tcont/gem 子树的情况（GemMapping 为空）。
func TestParseOLTLineProfilesNoGemMapping(t *testing.T) {
	const cfg = `hostname OLT
[global-config]
 ont-lineprofile gpon profile-id 11 profile-name "NoMap"
 commit
#
`
	got := parseOLTLineProfiles(cfg)
	if len(got) != 1 {
		t.Fatalf("解析出 %d 条，期望 1: %v", len(got), got)
	}
	if p := got[0]; p.Id != "11" || p.Name != "NoMap" || len(p.GemMapping) != 0 {
		t.Fatalf("profile = %+v", p)
	}
}
