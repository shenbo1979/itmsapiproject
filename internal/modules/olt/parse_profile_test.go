package olt

import "testing"

// dbaSampleConfig 覆盖全部 5 种 DBA 模板（type1-5 的 fix/assure/max），
// 并含一个越界（profile-id 999）与一个非法（非 dba-profile）行，验证解析跳过。
const dbaSampleConfig = `hostname OLT
[global-config]
 dba-profile add profile-id 10 profile-name "DBA Main" type3 assure 20032 max 1000000
 dba-profile add profile-id 20 profile-name "Fixed" type1 fix 512
 dba-profile add profile-id 30 profile-name "Assured" type2 assure 1024
 dba-profile add profile-id 40 profile-name "Maxed" type4 max 4096
 dba-profile add profile-id 50 profile-name "Full" type5 fix 128 assure 256 max 512
 dba-profile add profile-id 999 profile-name "TooBig" type3 max 128
ont-lineprofile gpon profile-id 100 profile-name "NotADBA"
#
`

// TestParseOLTDBAProfiles 覆盖 5 种类型解析、引号去除、越界与不相关行跳过。
func TestParseOLTDBAProfiles(t *testing.T) {
	got := parseOLTDBAProfiles(dbaSampleConfig)
	if len(got) != 5 {
		t.Fatalf("解析出 %d 条，期望 5（999 越界、ont-lineprofile 跳过）: %v", len(got), got)
	}
	byID := map[int]DBAProfileInfo{}
	for _, p := range got {
		byID[p.Id] = p
	}

	p10 := byID[10]
	if p10.Name != "DBA Main" || p10.Type != "type3" || p10.Assure != 20032 || p10.Max != 1000000 {
		t.Fatalf("id=10 = %+v", p10)
	}
	if p20 := byID[20]; p20.Type != "type1" || p20.Fix != 512 || p20.Assure != 0 {
		t.Fatalf("id=20 = %+v", p20)
	}
	if p30 := byID[30]; p30.Type != "type2" || p30.Assure != 1024 || p30.Max != 0 {
		t.Fatalf("id=30 = %+v", p30)
	}
	if p40 := byID[40]; p40.Type != "type4" || p40.Max != 4096 || p40.Fix != 0 || p40.Assure != 0 {
		t.Fatalf("id=40 = %+v", p40)
	}
	if p50 := byID[50]; p50.Type != "type5" || p50.Fix != 128 || p50.Assure != 256 || p50.Max != 512 {
		t.Fatalf("id=50 = %+v", p50)
	}
}

// TestParseOLTDBAProfilesSkipsGarbage 覆盖空输入、非 global-config 段、只有一行非 dtype 的情况。
func TestParseOLTDBAProfilesSkipsGarbage(t *testing.T) {
	if got := parseOLTDBAProfiles(""); len(got) != 0 {
		t.Fatalf("空配置应返回 0 条，got %d", len(got))
	}
	if got := parseOLTDBAProfiles("[vlan-config]\ndba-profile add profile-id 10 name x type1 fix 128\n#\n"); len(got) != 0 {
		t.Fatalf("非 global-config 段的 dba 行应跳过，got %d", len(got))
	}
	if got := parseOLTDBAProfiles("[global-config]\nsome-other line\n#\n"); len(got) != 0 {
		t.Fatalf("不含 dba-profile add 的行应跳过，got %d", len(got))
	}
}

// TestParseOLTDBAProfilesValueOnNextLine 复现业务方实际看到的换行形态：`max` 关键字留在首行，
// 数值 `51200` 单独换行到下一行（行首是裸数字）。此前续行规则只认行首关键字，导致 Max=0。
func TestParseOLTDBAProfilesValueOnNextLine(t *testing.T) {
	const cfg = `hostname OLT
[global-config]
 dba-profile add profile-id 11 profile-name "test11" type3 assure 20480 max
 51200
#`
	got := parseOLTDBAProfiles(cfg)
	if len(got) != 1 {
		t.Fatalf("解析出 %d 条，期望 1: %v", len(got), got)
	}
	p := got[0]
	if p.Id != 11 || p.Name != "test11" || p.Type != "type3" || p.Assure != 20480 || p.Max != 51200 {
		t.Fatalf("id=11（max 数值分行的续行） = %+v", p)
	}
}

// TestOLTCommandArgument 覆盖关键字取参与引号包裹两种形式。
func TestOLTCommandArgument(t *testing.T) {
	if got := oltCommandArgument("dba-profile add profile-id 10 profile-name DBA", "profile-id"); got != "10" {
		t.Fatalf("profile-id = %q", got)
	}
	if got := oltCommandArgument(`dba-profile add profile-id 10 profile-name "DBA Main"`, "profile-name"); got != "DBA Main" {
		t.Fatalf("profile-name = %q", got)
	}
	if got := oltCommandArgument("dba-profile add profile-id 10", "assure"); got != "" {
		t.Fatalf("缺失关键字应返回空串，got %q", got)
	}
}

// TestParseOLTDBAProfilesMultiline 覆盖设备把一条 dba-profile 命令跨多行回显的情况，
// 例如 `type3 assure 20480` 与 `max 51200` 分属两行，续行应并入同一条模板，带宽不丢失。
func TestParseOLTDBAProfilesMultiline(t *testing.T) {
	const cfg = `hostname OLT
[global-config]
 dba-profile add profile-id 11 profile-name "test11"
 type3 assure 20480 max 51200
 dba-profile add profile-id 12 profile-name "m"
 type4 max 65536
 dba-profile add profile-id 13 profile-name "no-sep" type5 fix 1000
 assure 2000
 max 3000
 dba-profile add profile-id 14 profile-name "plain" type1 fix 512
#`
	got := parseOLTDBAProfiles(cfg)
	if len(got) != 4 {
		t.Fatalf("解析出 %d 条，期望 4: %v", len(got), got)
	}
	byID := map[int]DBAProfileInfo{}
	for _, p := range got {
		byID[p.Id] = p
	}
	p11 := byID[11]
	if p11.Name != "test11" || p11.Type != "type3" || p11.Assure != 20480 || p11.Max != 51200 {
		t.Fatalf("id=11（首行不含参数、type3 续行补全） = %+v", p11)
	}
	if p12 := byID[12]; p12.Type != "type4" || p12.Max != 65536 || p12.Assure != 0 {
		t.Fatalf("id=12（type4 跨行 max） = %+v", p12)
	}
	if p13 := byID[13]; p13.Name != "no-sep" || p13.Fix != 1000 || p13.Assure != 2000 || p13.Max != 3000 {
		t.Fatalf("id=13（type5 三段跨行） = %+v", p13)
	}
	if p14 := byID[14]; p14.Type != "type1" || p14.Fix != 512 {
		t.Fatalf("id=14（单行不受影响） = %+v", p14)
	}
}

// TestOLTDBACommandLines 直接验证折叠逻辑：新命令开启前一条、参数续行并入、无关行终结。
func TestOLTDBACommandLines(t *testing.T) {
	in := []string{
		"dba-profile add profile-id 1 type3",
		"  assure 10",
		"  max 20",
		"ont-lineprofile gpon profile-id 9 name x",
		"dba-profile add profile-id 2",
		"type4 max 30",
		"",
	}
	out := oltDBACommandLines(in)
	want := []string{
		"dba-profile add profile-id 1 type3 assure 10 max 20",
		"ont-lineprofile gpon profile-id 9 name x",
		"dba-profile add profile-id 2 type4 max 30",
	}
	if len(out) != len(want) {
		t.Fatalf("折叠出 %d 行，期望 %d: %v", len(out), len(want), out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("第 %d 行 = %q，期望 %q（全部：%v）", i, out[i], want[i], out)
		}
	}
}
