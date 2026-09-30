package oltconn

import (
	"fmt"
	"strings"
	"time"
)

// 连接层：OLT 设备的会话抽象（Telnet 与 SSH 两种通道统一接口）。
//
// 背景（见 docs/09-olt-m2.md §0 施工政策）：老 OLT 实现只有 Telnet（connectOltTelnet），
// 新实现按要求同时支持 Telnet 与 SSH，并保留老实现的业务流程（登录 → 关分页 → 逐条发命令 → 读回显）。
//
// 包结构：
//
//	session.go  Session 接口与提示符/分页/登录失败的判定
//	telnet.go   Telnet 实现（net.Conn）
//	ssh.go      SSH 实现（golang.org/x/crypto/ssh，PTY + 交互 shell）
//	conn.go     按 Type/端口选择通道、登录、执行命令的高层封装
//
// 与 switch 模块自带客户端（internal/modules/switch/telnet.go、sshclient.go）的关系：
// 两者会话语义一致但提示符/分页特征不同（OLT 常见 `>>`、`---- More ----`），
// 待 OLT 迁移稳定后再评估抽公共包（避免现在动 switch 已上线的代码）。
type Kind string

// 连接方式（与老实现的 `ConnectParameters.Type` 对齐：1 RPC-API / 2 Telnet / 3 SSH；
// 新口径 0 TELNET / 1 SSH，见 Params.Type 的说明）。
const (
	KindTelnet Kind = "telnet"
	KindSSH    Kind = "ssh"
)

// TypeAuto 表示"调用方没有指定连接方式"，由通道层按端口推断（22 → SSH，其余 → TELNET）。
const TypeAuto = -1

// Params 是连接参数（老接口 connData 的语义，字段名沿用老实现）。
type Params struct {
	// Type：连接方式。新口径 **0 = TELNET / 1 = SSH**（默认 0，见 web/src/modules/olt.js 的表单）；
	// 为兼容老壳与迁移数据，老口径 2 = TELNET / 3 = SSH 同样接受；
	// TypeAuto（-1）表示"调用方没指定"，此时按端口推断（22 → SSH，其余 → TELNET）。
	Type int
	IP   string
	Port int
	// Username / Password 为设备登录凭据（口令可能来自"密文优先、明文兜底"的读取）。
	Username string
	Password string
	// Timeout 为单次读写超时（<=0 时用 DefaultTimeout）。
	Timeout time.Duration
	// Maker 仅用于日志/错误提示（如 huawei/zte/h3c）。
	Maker string
}

const (
	// DefaultTimeout 是缺省读写超时（老实现 TelnetClient.Timeout = 10s）。
	DefaultTimeout = 10 * time.Second
	// DefaultIdleTimeout 是"本轮输出结束"的静默判定窗口。
	DefaultIdleTimeout = 300 * time.Millisecond
	// DefaultLoginTimeout 是登录阶段单次读取超时。
	DefaultLoginTimeout = 3 * time.Second
)

// Session 是设备会话：两种通道实现同一组语义。
type Session interface {
	// Send 发送一条命令（实现负责补换行）。
	Send(command string) error
	// Read 读取一轮输出：收到数据后以 idle 静默窗口判定本轮结束，整体不超过 timeout。
	Read(timeout time.Duration) (string, error)
	// ReadPaged 读取分页输出：遇到分页提示自动翻页，直到没有分页提示。
	ReadPaged(timeout time.Duration) (string, error)
	// Kind 返回通道类型（用于日志与错误提示）。
	Kind() Kind
	// Close 关闭会话。
	Close() error
}

// PagingPatterns 是各厂商 OLT 的分页提示（命中即需要发送空格继续）。
// 含 MA5603T 实测分页提示 `---- More ( Press 'Q' to break ) ----`（整串不匹配则大板回显
// 会被截断在首屏，导致下钻端口解析丢失，见 docs/09 §5 真机阶段）。
var PagingPatterns = []string{
	"---- More ----",
	"--More--",
	"---- more ----",
	"press any key",
	"---- More (", // MA5603T：`---- More ( Press 'Q' to break ) ----`
}

// PromptSuffixes 是命令提示符的尾缀特征（Huawei/ZTE/H3C OLT 常见 `>>`、`#`、`>`、`]`）。
var PromptSuffixes = []string{">>", ">", "#", "]"}

// loginFailureHints 是登录被拒的典型回显（用于尽早失败，避免在失效会话上阻塞）。
var loginFailureHints = []string{
	"error", "invalid", "incorrect", "failed", "denied", "authentication fail",
	"用户名或密码", "密码错误", "登录失败", "认证失败",
}

// LooksLikePrompt 判断一段回显是否以命令提示符结尾（用于登录完成与命令结束判定）。
func LooksLikePrompt(output string) bool {
	trimmed := strings.TrimRight(strings.ReplaceAll(output, "\r\n", "\n"), " \t\n")
	if trimmed == "" {
		return false
	}
	for _, suffix := range PromptSuffixes {
		if strings.HasSuffix(trimmed, suffix) {
			return true
		}
	}
	return false
}

// LooksLikeLoginFailure 判断回显中是否出现登录失败特征。
func LooksLikeLoginFailure(output string) bool {
	lower := strings.ToLower(output)
	for _, hint := range loginFailureHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// 说明：用户名/口令提示的判定口径由 internal/devcli 统一提供（与交换机模块共用同一套规则），
// 这里不再各写一份，避免再次出现「真机提示带空格就漏判」的漂移（见 docs/09-olt-m2.md §5.2 #18）。

// HasPaging 判断回显是否停在分页提示上。
func HasPaging(output string) bool {
	for _, pattern := range PagingPatterns {
		if strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

// stripPaging 去掉回显里的分页提示（保留正文，便于上层解析）。
func stripPaging(output string) string {
	for _, pattern := range PagingPatterns {
		output = strings.ReplaceAll(output, pattern, "")
	}
	return output
}

// describeParams 生成用于错误提示的脱敏描述（不包含口令）。
func describeParams(params Params) string {
	host := strings.TrimSpace(params.IP)
	if host == "" {
		host = "?"
	}
	return fmt.Sprintf("%s:%d（%s）", host, params.Port, params.Kind())
}

// Kind 依据 Type/端口推断通道。
//
// 口径（2026-09-22 按业务方规格调整，见 docs/09 §5.2）：
//   - 0 = TELNET（默认）、1 = SSH；
//   - 兼容老口径：2 = TELNET、3 = SSH（老壳与迁移数据仍在用）；
//   - 其它取值（含 TypeAuto = -1，表示调用方未指定）：按端口推断 —— 22 → SSH，其余 → TELNET。
func (p Params) Kind() Kind {
	switch p.Type {
	case 0, 2:
		return KindTelnet
	case 1, 3:
		return KindSSH
	}
	if p.Port == 22 {
		return KindSSH
	}
	return KindTelnet
}

// effectiveTimeout 返回实际使用的读写超时。
func (p Params) effectiveTimeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return DefaultTimeout
}
