package olt

import "testing"

// TestParseConfiguredONTTotals 覆盖"已保存配置"口径的 ONT 统计与合并。
func TestParseConfiguredONTTotals(t *testing.T) {
	content := "interface gpon 0/1\r\n" +
		" ont add 0 0 sn-auth 1 omci ont-lineprofile-id 10\r\n" +
		" ont add 1 0 sn-auth 2 omci ont-lineprofile-id 10\r\n" +
		" ont add 1 1 sn-auth 3 omci ont-lineprofile-id 10\r\n" +
		"#\r\n" +
		"interface epon 0/2\r\n" +
		" ont add 0 0 mac-auth 1\r\n"
	totals := parseOLTConfiguredONTTotals(content)
	// 老口径：键是配置里的 `interface <type> 0/x` 段 + `ont add <ontId>`，即 "0/x/<ontId>"。
	if totals["0/1/0"] != 1 || totals["0/1/1"] != 2 || totals["0/2/0"] != 1 {
		t.Fatalf("配置统计 = %v", totals)
	}
	boards := []OltBoardInfo{{
		Id: "1",
		PonInfos: []PonInfos{
			{Id: "0", FSP: "0/1/0", OntOnline: 5, OntTotal: 8},
			{Id: "1", FSP: "0/1/1", OntOnline: 1, OntTotal: 1},
		},
	}}
	online, total := applyOLTConfiguredONTTotals(boards, totals)
	if online != 6 || total != 10 {
		t.Fatalf("汇总 = %d/%d, want 6/10", online, total)
	}
	if boards[0].PonInfos[1].OntTotal != 2 { // 配置里 2 > 在线口径 1，取配置口径
		t.Fatalf("配置口径未覆盖总数: %+v", boards[0].PonInfos[1])
	}
}

// TestParseCurrentConfigurationText 覆盖当前配置文本的裁剪与结束判定。
func TestParseCurrentConfigurationText(t *testing.T) {
	raw := "\r\n  Command:\r\n  display current-configuration\r\n  !\r\n  hostname OLT-TEST\r\n  return\r\n  MA5800-X7>"
	if !hasOLTCurrentConfigurationReturn(raw) {
		t.Fatal("应识别 return 结束行")
	}
	normalized := normalizeOLTCurrentConfiguration(raw)
	// 只裁掉头部的 Command:/命令回显行与整体首尾空白，正文各行的缩进原样保留（老实现同口径）。
	if normalized != "!\n  hostname OLT-TEST\n  return\n  MA5800-X7>" {
		t.Fatalf("裁剪结果 = %q", normalized)
	}
	if !hasOLTInputPrompt("{ <cr>|vlan: }:") {
		t.Fatal("应识别交互式提示")
	}
}

// TestPaginateBoundsMatchesLegacy 覆盖分页边界（与老实现 tr069handler 的同名函数逐行一致）。
func TestPaginateBoundsMatchesLegacy(t *testing.T) {
	cases := []struct {
		page, limit, count   int
		wantPage, start, end int
	}{
		{1, 50, 3, 1, 0, 3},
		{2, 1, 3, 2, 1, 2},
		{9, 2, 5, 3, 4, 5},
		{1, 50, 0, 0, 0, 0},
	}
	for _, item := range cases {
		page, start, end := paginateBounds(item.page, item.limit, item.count)
		if page != item.wantPage || start != item.start || end != item.end {
			t.Fatalf("paginateBounds(%d,%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
				item.page, item.limit, item.count, page, start, end, item.wantPage, item.start, item.end)
		}
	}
}

// TestMatchesONTListFilter 覆盖 ONT 列表过滤（关键字/在线/板卡）。
func TestMatchesONTListFilter(t *testing.T) {
	item := OntListResponse{PonType: "GPON", FSP: "0/1/0", OntId: "3", RunState: "online",
		SerialNumber: "48575443AABBCCDD", Desc: "office-ont"}
	if !matchesONTListFilter(item, "", "", "") {
		t.Fatal("空条件应命中")
	}
	if !matchesONTListFilter(item, "AABBCC", "", "") {
		t.Fatal("应按 SN 命中")
	}
	if matchesONTListFilter(item, "office", "offline", "") {
		t.Fatal("在线状态不符不应命中")
	}
	if !matchesONTListFilter(item, "", "online", "1") {
		t.Fatal("板卡 1 应命中")
	}
	if matchesONTListFilter(item, "", "", "2") {
		t.Fatal("板卡 2 不应命中")
	}
}
