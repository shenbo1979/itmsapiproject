// 真机实测（手动，不随常规回归跑）：
//
//	连真实 MA5603T 走 handleBoardAndSlotInfo 的生产链路
//	（HTTP form → decodeConnectParameters → connectDevice → EnterConfigMode → runCommand
//	 → oltconn.RunCommandUntilSignal → 解析板卡 → 响应 JSON），
//	验证「回到 config 提示符（末尾 #）」判定命令结束的修复在真机上成立。
//
// 门控：默认 skip。设置以下环境变量后才会连真机：
//
//	TXAPI_LIVE_OLT_IP     设备 IP（必填，如 172.20.247.88）
//	TXAPI_LIVE_OLT_PORT   端口（可选，默认 23）
//	TXAPI_LIVE_OLT_USER   账号（可选，默认 shenbo）
//	TXAPI_LIVE_OLT_PASS   口令（可选，默认用仓库已知测试密文；模块按平台 AES key 解密后登录）
//
// 运行：
//
//	cd internal/modules/olt && TXAPI_LIVE_OLT_IP=172.20.247.88 go test -run TestLiveOLTBoardAndSlot -v -count=1 .
package olt

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// TestLiveOLTBoardAndSlotInfo 连真实 OLT 做全量板卡采集，验证提示符判定读回不回断、不卡死。
func TestLiveOLTBoardAndSlotInfo(t *testing.T) {
	ip := strings.TrimSpace(os.Getenv("TXAPI_LIVE_OLT_IP"))
	if ip == "" {
		t.Skip("未设置 TXAPI_LIVE_OLT_IP，跳过真机实测（手动验证专用）")
	}
	port := strings.TrimSpace(os.Getenv("TXAPI_LIVE_OLT_PORT"))
	if port == "" {
		port = "23"
	}
	username := strings.TrimSpace(os.Getenv("TXAPI_LIVE_OLT_USER"))
	if username == "" {
		username = "shenbo"
	}
	password := strings.TrimSpace(os.Getenv("TXAPI_LIVE_OLT_PASS"))
	if password == "" {
		password = "aCcXGNrJkcjLss1EHp8dTJA25H8JMgAuKFO//us8sC4uhh+5" // 老实现测试口令（由平台 AES 解密后为明文）
	}

	module, _, _ := newDeviceTestModule(t)
	// 与生产一致：凭据密文由平台 AES key 解密（tmp_diag 已验证 /home/switchfile/aeskey.key 可用）。
	module.ctx.CredentialKeyPath = "/home/switchfile/aeskey.key"

	connData := fmt.Sprintf(`{"Type":2,"IP":"%s","Port":"%s","Username":"%s","Password":"%s"}`,
		ip, port, username, password)
	form := url.Values{
		"connData": {connData},
		"saveData": {`{"SlotId":""}`},
		"refresh":  {"1"},
	}
	code, payload := postOltForm(t, module.handleBoardAndSlotInfo, form)
	if code != http.StatusOK {
		t.Fatalf("http=%d payload=%v", code, payload)
	}
	if codeNum, _ := payload["code"].(float64); codeNum != 0 {
		t.Fatalf("业务 code=%v message=%v", payload["code"], payload["message"])
	}
	boards, _ := payload["data"].([]any)
	if len(boards) == 0 {
		t.Fatalf("真机板卡列表为空（采集可能被读断或设备拒绝）: %v", payload)
	}
	t.Logf("真机采集成功：板卡 %d 块，OntOnlines=%v OntTotal=%v", len(boards), payload["OntOnlines"], payload["OntTotal"])
	// 断言含预期主控/板卡（防止读成只剩 7 字节 "display" 的截断态）
	joined := ""
	for _, b := range boards {
		if m, ok := b.(map[string]any); ok {
			joined += fmt.Sprintf("%v ", m["BoardName"])
		}
	}
	t.Logf("板卡类型：%s", joined)
	if !strings.Contains(joined, "SCUN") && !strings.Contains(joined, "GPBD") &&
		!strings.Contains(joined, "EPBD") && !strings.Contains(joined, "GICF") {
		t.Fatalf("未读到任何板卡名（可能回显被截断/提示符判定异常）: %q", joined)
	}
}
