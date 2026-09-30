package olt

import "testing"

// srvProfileSampleConfig 覆盖三条完整 ONT 业务模板的解析：含 ont-port 电话口/网口数量、
// 逐条 `port vlan eth … translation …`（网口→业务VLAN 映射）、一条仅 5 字段的畸形行
// （`ont-srvprofile gpon profile-id 9` 无 profile-name，len<6 跳过）与一段 TR069 管理模板行
// （验证两种解析互不干扰与段隔离）。
const srvProfileSampleConfig = `hostname OLT
[global-config]
 ont-srvprofile gpon profile-id 10 profile-name "SrvA"
 ont-port pots 2 eth 4
 port vlan eth 1 translation 100
 port vlan eth 2 translation 200,300
 commit
 ont-srvprofile epon profile-id 20 profile-name "SrvB"
 ont-port pots 0 eth 1
 port vlan eth 1 translation 400
 commit
 ont-srvprofile gpon profile-id 9
 commit
 some-other line
 ont tr069-server-profile add profile-id 1 profile-name "ACS-A" url http://acs.example.com user acsuser secret auth-realm auth
 ont tr069-server-profile add profile-id 2 profile-name "ACS-B" url https://acs2.example.com user admin secret2 auth-realm auth
#
`

// TestParseOLTSrvProfiles 覆盖：引号去除、Type/Pots/Eths 取值、SrvVlan 层级（网口/业务VLAN）、
// 无 profile-name 的畸形行跳过，以及「ont-port pots/eth 语义」：老壳表单曾把网口数量存到 Pots、
// 电话口数量存到 Eths，这里按语义校验解析端 Eths=网口、Pots=电话口。
func TestParseOLTSrvProfiles(t *testing.T) {
	got := parseOLTSrvProfiles(srvProfileSampleConfig)
	if len(got) != 2 {
		t.Fatalf("解析出 %d 个业务模板，期望 2（无 profile-name 的畸形行跳过）: %v", len(got), got)
	}
	first := got[0]
	if first.Id != "10" || first.Name != "SrvA" || first.Type != "gpon" {
		t.Fatalf("first = %+v", first)
	}
	// 语义修正：ont-port pots 2 eth 4 → Pots=2（电话口）、Eths=4（网口）。
	if first.Pots != "2" || first.Eths != "4" {
		t.Fatalf("first Pots/Eths = %q/%q，期望 2/4", first.Pots, first.Eths)
	}
	// SrvVlan：每条 `port vlan eth … translation …` 一行一条（Vlan 可为逗号合并的多个值）。
	if len(first.SrvVlan) != 2 {
		t.Fatalf("first.SrvVlan 应 2 条（eth1→100、eth2→200,300），got %+v", first.SrvVlan)
	}
	if first.SrvVlan[0].Eth != "1" || first.SrvVlan[0].Vlan != "100" {
		t.Fatalf("SrvVlan[0] = %+v", first.SrvVlan[0])
	}
	if first.SrvVlan[1].Eth != "2" || first.SrvVlan[1].Vlan != "200,300" {
		t.Fatalf("SrvVlan[1] = %+v", first.SrvVlan[1])
	}
	second := got[1]
	if second.Id != "20" || second.Name != "SrvB" || second.Type != "epon" {
		t.Fatalf("second = %+v", second)
	}
	// pots 0 / eth 1 → 只发 eth 分支（仅网口数量）。
	if second.Pots != "0" || second.Eths != "1" {
		t.Fatalf("second Pots/Eths = %q/%q，期望 0/1", second.Pots, second.Eths)
	}
	if len(second.SrvVlan) != 1 || second.SrvVlan[0].Vlan != "400" {
		t.Fatalf("second.SrvVlan = %+v", second.SrvVlan)
	}
}

// TestParseOLTTr069Profiles 覆盖：TR069 管理模板解析（Id/Name/AcsUrl/AcsUserName，不回密码）、
// 字段关键字取值与无关行跳过。
func TestParseOLTTr069Profiles(t *testing.T) {
	got := parseOLTTR069Profiles(srvProfileSampleConfig)
	if len(got) != 2 {
		t.Fatalf("解析出 %d 条 TR069 模板，期望 2: %v", len(got), got)
	}
	first := got[0]
	if first.Id != "1" || first.Name != "ACS-A" || first.AcsUrl != "http://acs.example.com" || first.AcsUserName != "acsuser" {
		t.Fatalf("first = %+v", first)
	}
	second := got[1]
	if second.Id != "2" || second.Name != "ACS-B" || second.AcsUrl != "https://acs2.example.com" || second.AcsUserName != "admin" {
		t.Fatalf("second = %+v", second)
	}
}

// TestSrvVlanList 覆盖业务 VLAN 输入拆分：逗号、换行、斜杠混合分隔，空段/空值过滤。
func TestSrvVlanList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"100,101", []string{"100", "101"}},
		{"100\n101\n102", []string{"100", "101", "102"}},
		{"100/200,300", []string{"100", "200", "300"}},
		{" 100 , \n101,", []string{"100", "101"}},
		{"", nil},
	}
	for _, c := range cases {
		got := srvVlanList(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("srvVlanList(%q) = %v，期望 %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("srvVlanList(%q)[%d] = %q，期望 %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestSrvProfileIDIsValid 覆盖业务模板/TR069 模板 ID 闭区间校验（修正老实现恒假 bug）。
func TestSrvProfileIDIsValid(t *testing.T) {
	if srvProfileIDIsValid(0) || srvProfileIDIsValid(8193) || !srvProfileIDIsValid(1) || !srvProfileIDIsValid(8192) {
		t.Fatalf("srvProfileIDIsValid 闭区间错误")
	}
	if tr069ProfileIDIsValid(0) || tr069ProfileIDIsValid(33) || !tr069ProfileIDIsValid(1) || !tr069ProfileIDIsValid(32) {
		t.Fatalf("tr069ProfileIDIsValid 闭区间错误")
	}
}
