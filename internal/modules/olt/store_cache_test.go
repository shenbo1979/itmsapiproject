package olt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveAndLoadDeviceSnapshot 覆盖"文件数据入库表"的快照往返：
// currentconfiguration.json 的标量字段 + 嵌套板卡/PON 口/GE 口拆表后再组装回来。
func TestSaveAndLoadDeviceSnapshot(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	cfg := OltCurrentConfig{
		ConnectTime: "2026-09-01 15:40:20", OLTRunTime: "31天21小时57分43秒", Version: "MA5600V800R011C00",
		Patch: "SPC100 SPH102", Frame: "MA5603T", FrameTotal: 13, FrameCount: 5,
		SlotidRefreshTime: "2026-09-01 15:40:21", FrameDesc: "支持13块板卡", OntOnlines: 2, OntTotal: 2,
		OltBoardInfo: []OltBoardInfo{{
			Id: "4", Name: "H805GPBD", Status: "Normal", Online: true, Type: "user", Desc: "8口GPON业务板",
			Temperature: "43", PortNum: 9, PonType: "GE",
			PonInfos: []PonInfos{{Id: "0", FSP: "0/4/0", Type: "GPON", OntTotal: 0, OntOnline: 0, PortStatus: "Offline"}},
			GeInfos:  []GeInfos{{Id: "8", FSP: "0/4/8", Type: "GE", NativeVLAN: "10", Speed: "full", Duplex: "0", Link: "up"}},
		}},
	}
	if err := module.store.SaveDeviceSnapshot("192.0.2.60", cfg, "/home/olt/192.0.2.60/currentconfiguration.json", "2026-09-01 15:40:22", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveDeviceSnapshot: %v", err)
	}

	loaded, ok, err := module.store.LoadDeviceSnapshot("192.0.2.60")
	if err != nil || !ok {
		t.Fatalf("LoadDeviceSnapshot ok=%v err=%v", ok, err)
	}
	if loaded.Version != cfg.Version || loaded.OLTRunTime != cfg.OLTRunTime || loaded.FrameTotal != 13 || loaded.OntTotal != 2 {
		t.Fatalf("快照标量不一致: %+v", loaded)
	}
	if len(loaded.OltBoardInfo) != 1 {
		t.Fatalf("板卡数 = %d, want 1", len(loaded.OltBoardInfo))
	}
	board := loaded.OltBoardInfo[0]
	if board.Id != "4" || !board.Online || board.Temperature != "43" || board.PortNum != 9 {
		t.Fatalf("板卡字段不一致: %+v", board)
	}
	if len(board.PonInfos) != 1 || board.PonInfos[0].FSP != "0/4/0" || board.PonInfos[0].PortStatus != "Offline" {
		t.Fatalf("PON 口不一致: %+v", board.PonInfos)
	}
	if len(board.GeInfos) != 1 || board.GeInfos[0].NativeVLAN != "10" || board.GeInfos[0].Link != "up" {
		t.Fatalf("GE 口不一致: %+v", board.GeInfos)
	}

	// 再写一份（仅 1 块板卡、无端口）→ 应整份覆盖，不残留旧的 PON/GE 行
	if err := module.store.SaveDeviceSnapshot("192.0.2.60", OltCurrentConfig{
		Version: "V2", OltBoardInfo: []OltBoardInfo{{Id: "4", Name: "H805GPBD", Online: true}},
	}, "", "", "2026-09-22 11:00:00"); err != nil {
		t.Fatalf("二次保存失败: %v", err)
	}
	reloaded, ok, err := module.store.LoadDeviceSnapshot("192.0.2.60")
	if err != nil || !ok {
		t.Fatalf("二次读取失败: ok=%v err=%v", ok, err)
	}
	if reloaded.Version != "V2" || len(reloaded.OltBoardInfo[0].PonInfos) != 0 || len(reloaded.OltBoardInfo[0].GeInfos) != 0 {
		t.Fatalf("整份覆盖未生效（残留旧端口）: %+v", reloaded.OltBoardInfo[0])
	}
}

// TestSaveAndLoadOntCache 覆盖 ONT 缓存的写入/读取/槽位列表（老 JSON 的 IP → OntIP 列）。
func TestSaveAndLoadOntCache(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	items := []OntListResponse{
		{PonType: "gpon", FSP: "0/4/5", OntId: "0", Code: "5A4E585464BD7A98", SerialNumber: "ZNXT64BD7A98",
			ControlFlag: "active", RunState: "online", ConfigState: "normal", MatchState: "match", ProtectSide: "no",
			AuthType: "sn-auth", Protocol: "omci", LineProfile: "10", SrvProfile: "10",
			Desc: "ONT_NO_DESCRIPTION", Mac: "AC:EE:64:BD:7A:98", IP: "192.168.46.33"},
		{PonType: "gpon", FSP: "0/4/5", OntId: "1", RunState: "online", ConfigState: "failed", AuthType: "sn-auth"},
	}
	if err := module.store.SaveOntCache("192.0.2.61", "4", items, "/legacy/4.txt", "2026-09-01 15:40:22", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveOntCache: %v", err)
	}
	// 覆盖式：同槽位再写 1 条 → 只剩 1 条（不残留）
	if err := module.store.SaveOntCache("192.0.2.61", "4", items[:1], "", "", "2026-09-22 11:00:00"); err != nil {
		t.Fatalf("二次 SaveOntCache: %v", err)
	}
	got, err := module.store.ListOntCache("192.0.2.61", "4")
	if err != nil {
		t.Fatalf("ListOntCache: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("覆盖后条数 = %d, want 1", len(got))
	}
	if got[0].IP != "192.168.46.33" || got[0].SerialNumber != "ZNXT64BD7A98" || got[0].ConfigState != "normal" {
		t.Fatalf("字段未按老 JSON 还原: %+v", got[0])
	}

	// 多槽位：槽位列表按数字升序（槽位 10 的 FSP 用 0/10/x，与真实数据一致）
	slotTen := []OntListResponse{
		{PonType: "gpon", FSP: "0/10/5", OntId: "0", SerialNumber: "SN10", RunState: "online"},
		{PonType: "gpon", FSP: "0/10/5", OntId: "1", SerialNumber: "SN11", RunState: "offline"},
	}
	if err := module.store.SaveOntCache("192.0.2.61", "10", slotTen, "", "", "2026-09-22 11:05:00"); err != nil {
		t.Fatalf("SaveOntCache(10): %v", err)
	}
	slots, err := module.store.ListOntCacheSlots("192.0.2.61")
	if err != nil {
		t.Fatalf("ListOntCacheSlots: %v", err)
	}
	if strings.Join(slots, ",") != "4,10" {
		t.Fatalf("槽位列表 = %v, want [4 10]", slots)
	}
	all, err := module.store.ListOntCache("192.0.2.61", "")
	if err != nil {
		t.Fatalf("ListOntCache(全部): %v", err)
	}
	if len(all) != 3 { // 槽位 4 一条 + 槽位 10 两条
		t.Fatalf("全部槽位条数 = %d, want 3", len(all))
	}
}

// TestSaveAndLoadConfigTextAndAutoReg 覆盖文本配置缓存与自动注册配置（口令由调用方加密）。
func TestSaveAndLoadConfigTextAndAutoReg(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	// 自动注册口令要按"密文入库"，这里给出平台层密钥文件（与线上一致）。
	keyPath := filepath.Join(t.TempDir(), "aeskey.key")
	if err := os.WriteFile(keyPath, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatalf("写密钥失败: %v", err)
	}
	module.ctx.CredentialKeyPath = keyPath
	if err := module.store.SaveConfigText("192.0.2.62", ConfigKindSaved, "hostname OLT-A\nreturn", "/legacy/saved.txt", "2026-09-01 15:43:00", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText(saved): %v", err)
	}
	if err := module.store.SaveConfigText("192.0.2.62", ConfigKindCurrent, "interface gpon 0/4\n ont add 0 0 sn-auth X", "", "", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText(current): %v", err)
	}
	saved, ok, err := module.store.LoadConfigText("192.0.2.62", ConfigKindSaved)
	if err != nil || !ok || !strings.Contains(saved, "hostname OLT-A") {
		t.Fatalf("读已保存配置失败: ok=%v err=%v content=%q", ok, err, saved)
	}
	current, ok, err := module.store.LoadConfigText("192.0.2.62", ConfigKindCurrent)
	if err != nil || !ok || !strings.Contains(current, "interface gpon 0/4") {
		t.Fatalf("读当前配置失败: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := module.store.LoadConfigText("192.0.2.62", "unknown-kind"); ok {
		t.Fatal("未知 Kind 不应返回数据")
	}

	// 自动注册配置：口令入库加密（读取时由调用方解密），并支持查询启用自动注册的设备
	encrypted := module.encryptOltSecret("LegacyPass1")
	if err := module.store.SaveAutoRegConfig(OltAutoRegConfig{
		HostIP: "192.0.2.62", Type: 2, Port: "23", Username: "oltuser", Password: encrypted,
		Registration: 1, Relocation: 0, TemplateId: 7, ScanInterval: 30,
	}, "/legacy/ontautomaticcfg.json", "2026-09-01 15:44:00", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveAutoRegConfig: %v", err)
	}
	cfg, ok, err := module.store.LoadAutoRegConfig("192.0.2.62")
	if err != nil || !ok {
		t.Fatalf("LoadAutoRegConfig ok=%v err=%v", ok, err)
	}
	if cfg.Registration != 1 || cfg.TemplateId != 7 || cfg.ScanInterval != 30 || cfg.Username != "oltuser" {
		t.Fatalf("自动注册配置字段不一致: %+v", cfg)
	}
	if cfg.Password == "LegacyPass1" {
		t.Fatal("口令列未加密")
	}
	if plain := module.decryptOltSecret(cfg.Password); plain != "LegacyPass1" {
		t.Fatalf("口令无法还原: %q", plain)
	}
	hosts, err := module.store.ListAutoRegHosts()
	if err != nil || len(hosts) != 1 || hosts[0] != "192.0.2.62" {
		t.Fatalf("ListAutoRegHosts = %v err=%v", hosts, err)
	}
}

// TestImportFileCacheFromLegacyFiles 覆盖"文件 → 表"导入：
// 按老系统的目录/文件形态造数据（老目录只读），导入后表里能读到，且重复导入幂等、老文件不被改写。
func TestImportFileCacheFromLegacyFiles(t *testing.T) {
	module, _, legacyDir := newDeviceTestModule(t)
	const host = "192.0.2.63"
	deviceDir := filepath.Join(legacyDir, host)
	if err := os.MkdirAll(filepath.Join(deviceDir, "slot_ont_file"), 0755); err != nil {
		t.Fatalf("建老目录失败: %v", err)
	}
	currentConfig := `{
	  "ConnectTime": "2026-09-01 15:40:20", "OLTRunTime": "31天21小时57分43秒",
	  "Version": "MA5600V800R011C00", "Patch": "SPC100", "Frame": "MA5603T",
	  "FrameTotal": 13, "FrameCount": 5, "SlotidRefreshTime": "2026-09-01 15:40:21",
	  "OntOnlines": 2, "OntTotal": 2,
	  "OltBoardInfo": [{
	    "Id": "4", "Name": "H805GPBD", "Status": "Normal", "Online": true, "Type": "user",
	    "Desc": "8口GPON业务板", "Temperature": "43", "PortNum": 1, "PonType": "GPON",
	    "PonInfos": [{"Id": "0", "FSP": "0/4/0", "Type": "GPON", "OntTotal": 2, "OntOnline": 2, "PortStatus": "Online"}]
	  }]
	}`
	writeFile := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(deviceDir, rel)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("写 %s 失败: %v", rel, err)
		}
		return path
	}
	writeFile("currentconfiguration.json", currentConfig)
	writeFile("slot_ont_file/4.txt",
		`{"PonType":"gpon","FSP":"0/4/5","OntId":"0","Code":"5A4E585464BD7A98","SerialNumber":"ZNXT64BD7A98","ControlFlag":"active","RunState":"online","ConfigState":"normal","MatchState":"match","ProtectSide":"no","AuthType":"sn-auth","Protocol":"omci","LineProfile":"10","SrvProfile":"10","Desc":"ONT_NO_DESCRIPTION","Mac":"AC:EE:64:BD:7A:98","IP":"192.168.46.33"}`+"\n"+
			`{"PonType":"gpon","FSP":"0/4/5","OntId":"1","SerialNumber":"CIOT0963B1F0","RunState":"online","ConfigState":"failed","AuthType":"sn-auth"}`+"\n")
	savedPath := writeFile("savedconfiguration.txt", "hostname OLT-LEGACY\nreturn")
	writeFile("olt_cur_cfg.txt", "interface gpon 0/4\n ont add 0 0 sn-auth X omci ont-lineprofile-id 10\n#\n")
	writeFile("ontautomaticcfg.json", `{"Type":2,"IP":"`+host+`","Port":"23","Username":"oltuser","Password":"LegacyPass1","Registration":1,"Relocation":0,"TemplateId":0,"ScanInterval":30}`)

	savedBefore, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("读取老文件失败: %v", err)
	}

	result, err := module.importFileCache()
	if err != nil {
		t.Fatalf("importFileCache: %v", err)
	}
	if result.Devices != 1 || result.OntItems != 2 || result.ConfigTexts != 2 || result.AutoRegConfs != 1 {
		t.Fatalf("导入统计不符: %+v", result)
	}

	// 表里能读到
	snapshot, ok, err := module.store.LoadDeviceSnapshot(host)
	if err != nil || !ok || snapshot.Version != "MA5600V800R011C00" {
		t.Fatalf("快照未入库: ok=%v err=%v %+v", ok, err, snapshot)
	}
	if len(snapshot.OltBoardInfo) != 1 || len(snapshot.OltBoardInfo[0].PonInfos) != 1 {
		t.Fatalf("板卡/端口未入库: %+v", snapshot.OltBoardInfo)
	}
	onts, err := module.store.ListOntCache(host, "4")
	if err != nil || len(onts) != 2 {
		t.Fatalf("ONT 缓存未入库: %d err=%v", len(onts), err)
	}
	if onts[0].IP != "192.168.46.33" {
		t.Fatalf("ONT 管理地址（老 JSON 的 IP）未保留: %+v", onts[0])
	}
	savedText, ok, err := module.store.LoadConfigText(host, ConfigKindSaved)
	if err != nil || !ok || !strings.Contains(savedText, "hostname OLT-LEGACY") {
		t.Fatalf("已保存配置未入库: ok=%v err=%v", ok, err)
	}
	currentText, ok, _ := module.store.LoadConfigText(host, ConfigKindCurrent)
	if !ok || !strings.Contains(currentText, "ont add 0 0 sn-auth X") {
		t.Fatalf("当前配置未入库: %q", currentText)
	}
	autoReg, ok, err := module.store.LoadAutoRegConfig(host)
	if err != nil || !ok || autoReg.Registration != 1 || autoReg.ScanInterval != 30 {
		t.Fatalf("自动注册配置未入库: ok=%v err=%v %+v", ok, err, autoReg)
	}
	if plain := module.decryptOltSecret(autoReg.Password); plain != "LegacyPass1" {
		t.Fatalf("自动注册口令未按密文入库/无法还原: %q", plain)
	}

	// 幂等：重复导入不产生重复行，且老文件未被改写（只读边界）
	if _, err := module.importFileCache(); err != nil {
		t.Fatalf("二次导入失败: %v", err)
	}
	ontsAgain, _ := module.store.ListOntCache(host, "4")
	if len(ontsAgain) != 2 {
		t.Fatalf("重复导入产生重复行: %d", len(ontsAgain))
	}
	savedAfter, err := os.ReadFile(savedPath)
	if err != nil || string(savedAfter) != string(savedBefore) {
		t.Fatal("导入改写了老目录文件（违反只读边界）")
	}

	// 表非空时 importFileCacheIfEmpty 不再重扫
	result2, err := module.importFileCacheIfEmpty()
	if err != nil {
		t.Fatalf("importFileCacheIfEmpty: %v", err)
	}
	if result2.Devices != 0 {
		t.Fatalf("表非空时不应再导入: %+v", result2)
	}
}
