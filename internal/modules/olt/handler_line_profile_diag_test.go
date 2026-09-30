package olt

// handler_line_profile_diag_test.go —— 覆盖「ONT 线路模板新增去了却在 OLT 上没建出 T-CONT/GEM」的
// 诊断收集（runLineProfileSubcmd / applyLineProfileMapping）：
//
//   - 子命令回显含 `Failure:` 时，首条失败被收进 firstFail（不再静默吞），commit 后
//     applyLineProfileMapping 返回诊断消息而不是 success；
//   - 回显正常（无 Failure/Error）时 firstFail 保持空、返回 success。
//
// 用一个只回一条「以 `#` 结尾」回显的假会话驱动 runCommand（RunCommandUntilSignal 见 `#` 即结束），
// 与 collect_timeout_test.go 的假会话同一手法，无需真机。

import (
	"strings"
	"testing"
	"time"

	"itmsapiproject/internal/oltconn"
)

// ruleReplySession 每次 Read 都返回同一份回显文本（runCommand 首次 Read 命中 `#` 即判定命令结束）。
type ruleReplySession struct {
	text string
}

func (s *ruleReplySession) Send(command string) error { return nil }
func (s *ruleReplySession) Kind() oltconn.Kind        { return oltconn.KindTelnet }
func (s *ruleReplySession) Close() error              { return nil }
func (s *ruleReplySession) ReadPaged(timeout time.Duration) (string, error) {
	return s.Read(timeout)
}
func (s *ruleReplySession) Read(timeout time.Duration) (string, error) {
	return s.text, nil
}

func TestRunLineProfileSubcmdCollectsFailure(t *testing.T) {
	failSess := &ruleReplySession{text: "\r\n  Failure: The DBA profile 10 does not exist.\r\nMA5603T(config-ont-lineprofile)#"}
	dev := &oltDevice{module: &Module{}, session: failSess}
	firstFail := ""
	(&Module{}).runLineProfileSubcmd(dev, "tcont 1 dba-profile-id 10", "TCONT", &firstFail)
	if firstFail == "" {
		t.Fatalf("回显含 Failure 时 firstFail 应为非空，got %q", firstFail)
	}
	if !strings.Contains(firstFail, "DBA profile 10 does not exist") {
		t.Fatalf("firstFail 应含设备失败原文，got %q", firstFail)
	}

	okSess := &ruleReplySession{text: "\r\n  ont-lineprofile success\r\nMA560UA(config)#"}
	dev2 := &oltDevice{module: &Module{}, session: okSess}
	firstFail2 := ""
	(&Module{}).runLineProfileSubcmd(dev2, "tcont 1 dba-profile-id 10", "TCONT", &firstFail2)
	if firstFail2 != "" {
		t.Fatalf("正常回显时 firstFail 应为空，got %q", firstFail2)
	}
}

func TestApplyLineProfileMappingSurfacesDiagnostic(t *testing.T) {
	mod := &Module{}
	devFail := &oltDevice{module: mod, session: &ruleReplySession{
		text: "\r\n  Failure: Traffic Container does not exist.\r\nMA5603T(config-ont-lineprofile)#",
	}}
	got := mod.applyLineProfileMapping(devFail, LineProfile{Id: 12, Name: "testline12", DbaProfileId: 10, PonType: "gpon", OpenTr069: 1, Vlan: "200,300"})
	if got == "" {
		t.Fatalf("子命令失败时应返回诊断消息，got 空串")
	}
	// 假会话对每条子命令都回同一份含 Failure 的回显，首个子命令（TR069）即捕获失败子段；
	// 无论捕获在哪个子段，最终都应带「配置未全部生效」前缀与具体 Failure 原文。
	if !strings.Contains(got, "T-CONT/GEM") || !strings.Contains(got, "Traffic Container does not exist") {
		t.Fatalf("诊断消息应标注 T-CONT/GEM 与设备 Failure 原文，got %q", got)
	}

	devOK := &oltDevice{session: &ruleReplySession{text: "\r\n  ont-lineprofile ok\r\nMA5600(config)#"}}
	if r := mod.applyLineProfileMapping(devOK, LineProfile{Id: 12, Name: "testline12", DbaProfileId: 100, PonType: "gpon", OpenTr069: 1, Vlan: "200,300"}); r != "" {
		t.Fatalf("子命令正常时 applyLineProfileMapping 应返回空串，got %q", r)
	}
}
