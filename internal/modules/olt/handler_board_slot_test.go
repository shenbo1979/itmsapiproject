package olt

import (
	"net/url"
	"testing"
)

// oltConnData 造一份 connData（与老壳一致：Type/IP/Port/Username/Password）。
func oltConnData(host, port string) string {
	return `{"Type":2,"IP":"` + host + `","Port":"` + port + `","Username":"oltuser","Password":"secret"}`
}

// TestHandleBoardAndSlotInfoCacheThenSingleSlot 覆盖：
// 全量采集（refresh=1）→ 断掉设备后仍能命中缓存 → 单槽 refresh=1 走设备并合并回缓存。
func TestHandleBoardAndSlotInfoCacheThenSingleSlot(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	full := url.Values{"connData": {oltConnData(host, port)}, "saveData": {`{"SlotId":""}`}, "refresh": {"1"}}
	code, payload := postOltForm(t, module.handleBoardAndSlotInfo, full)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("全量采集失败: %v", payload)
	}
	if got := payload["OntOnlines"].(float64); got != 6 {
		t.Fatalf("OntOnlines = %v, want 6", got)
	}
	if boards, _ := payload["data"].([]any); len(boards) != 3 {
		t.Fatalf("全量板卡数 = %d, want 3", len(boards))
	}

	// 关掉设备后按 SlotId 查询：必须命中缓存（老实现的快路径）
	server.Close()
	single := url.Values{"connData": {oltConnData(host, port)}, "saveData": {`{"SlotId":"1"}`}}
	code, cached := postOltForm(t, module.handleBoardAndSlotInfo, single)
	if code != 200 || cached["code"].(float64) != 0 {
		t.Fatalf("单槽缓存命中失败（设备已关，说明又去连设备了）: %v", cached)
	}
	cachedBoards, _ := cached["data"].([]any)
	if len(cachedBoards) != 1 {
		t.Fatalf("单槽返回条数 = %d, want 1", len(cachedBoards))
	}
	if got := cachedBoards[0].(map[string]any)["Id"]; got != "1" {
		t.Fatalf("单槽 Id = %v, want 1", got)
	}
	if got := cached["SlotidRefreshTime"]; got == nil || got == "" {
		t.Fatalf("单槽响应缺少 SlotidRefreshTime: %v", cached)
	}

	// 单槽 refresh=1：走设备采集并合并回 currentconfiguration.json
	server2 := startFakeOLT(t)
	host2, port2 := server2.HostPort()
	refresh := url.Values{
		"connData": {oltConnData(host2, port2)},
		"saveData": {`{"SlotId":"1"}`},
		"refresh":  {"1"},
	}
	_, refreshed := postOltForm(t, module.handleBoardAndSlotInfo, refresh)
	if refreshed["code"].(float64) != 0 {
		t.Fatalf("单槽刷新失败: %v", refreshed)
	}
	refreshedBoards, _ := refreshed["data"].([]any)
	if len(refreshedBoards) != 1 {
		t.Fatalf("单槽刷新返回条数 = %d, want 1", len(refreshedBoards))
	}
	if got := refreshedBoards[0].(map[string]any)["PortNum"].(float64); got != 3 {
		t.Fatalf("单槽刷新 PortNum = %v, want 3", got)
	}
	// 单槽刷新是"合并"语义（老实现 fullRefresh=false）：缓存里仍是 3 块板卡，槽位 1 被更新。
	cachedCfg, ok := module.loadCurrentConfig(host2)
	if !ok || len(cachedCfg.OltBoardInfo) != 3 {
		t.Fatalf("单槽刷新后的缓存板卡数 = %d, want 3（合并而非替换）: %+v", len(cachedCfg.OltBoardInfo), cachedCfg)
	}
	if cachedCfg.OltBoardInfo[1].Id != "1" || cachedCfg.OltBoardInfo[1].PortNum != 3 {
		t.Fatalf("单槽刷新未合并回缓存: %+v", cachedCfg.OltBoardInfo[1])
	}
}
