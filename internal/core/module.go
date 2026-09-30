// Package core 提供统一工程的公共基建：模块契约、模块注册表与运行期上下文。
//
// 设计要点（对应 docs/00-overview.md 的模块边界）：
//  1. 模块只能通过 Module 接口暴露能力，禁止再使用包级全局（老单体的共享 db/mutex 是历史事故根源）；
//  2. 老接口兼容层（internal/compat）负责把 /itms/handleXxx 映射到模块的新处理器；
//  3. 未安装模块的调用返回 -4，供前端隐藏菜单并给出可读提示。
package core

import (
	"database/sql"
	"log"
	"net/http"
)

// 老接口沿用下来的返回码语义（禁止改动既有取值）。
const (
	CodeOK           = 0  // 成功
	CodeFail         = -1 // 参数或业务错误
	CodeExpired      = -2 // 授权已过期
	CodeUnregistered = -3 // 系统未注册
	CodeModuleAbsent = -4 // 模块未安装（本工程新增）
)

// Menu 描述模块在前端壳里的菜单项；由 /api/capabilities 下发给壳。
// 前端规则（见 web/README.md 第 0 节）：模块内的 Menu 以 **TAB** 形式呈现。
type Menu struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
	Order int    `json:"order"`
}

// Nav 描述模块在左侧一级导航里的展示信息（标题与排序）。
//
// 前端规则（见 web/README.md 第 0 节）：
//   - 只安装一个模块时不显示左侧导航，直接全屏展示该模块；
//   - 安装多个模块时，每个模块占一个一级导航项（按 Order 升序），
//     模块内的功能页以 TAB 呈现（顺序取 Menu.Order）。
type Nav struct {
	// Title 是一级导航标题，例如「交换机管理」「OLT/ONU 管理」；为空时前端回退显示模块 ID。
	Title string `json:"title"`
	// Order 是一级导航排序（同名模块先按此升序）。
	Order int `json:"order"`
}

// Module 是功能模块的统一契约（switch / olt / tr069 各实现一份）。
type Module interface {
	ID() string
	Version() string
	// DependsOn 返回依赖的模块 id；注册表按依赖拓扑决定启用顺序（如 tr069 -> olt）。
	DependsOn() []string
	// Nav 返回模块在一级导航中的标题与排序。
	Nav() Nav
	// Menus 返回该模块要展示的菜单（一级导航下的 TAB）；未安装时前端不会渲染。
	Menus() []Menu
	// LegacyRoutes 返回本模块负责的老接口路径字面量（需与 tests/golden/routes.json 一致）。
	// 兼容层据此做「未安装返回 -4 / 已安装走本模块处理器」的分发。
	LegacyRoutes() []string
	// Register 只注册本模块的新命名空间路由（/api/v2/<id>/...）。
	Register(mux *http.ServeMux, ctx *Context) error
	// Migrate 负责本模块的库表建设与版本记录（一模块一库时由本模块自行打开库）。
	Migrate(ctx *Context) error
}

// LegacyRouteProvider 是模块可选的扩展能力：为「已完成迁移」的老接口提供真实处理器。
// 模块未列出的老接口仍由兼容层返回 -1「尚未迁移」的过渡响应（见 cmd/txapi/main.go 的挂载顺序）。
type LegacyRouteProvider interface {
	Module
	LegacyHandlers() map[string]http.HandlerFunc
}

// RegistrationChecker 由平台层注册服务实现，供业务模块在内部调用（后台任务、设备操作）前校验注册状态：
// 外部请求已由注册守卫拦截，内部调用同样需要校验，避免未注册或已过期时仍然操作设备。
type RegistrationChecker interface {
	// Require 返回 nil 表示注册有效；否则返回可读错误（未注册 / 已过期 / 无法校验）。
	Require() error
}

// Context 由 core 注入给模块，替代老实现的包级全局变量。
type Context struct {
	DB      *sql.DB
	Logger  *log.Logger
	DataDir string
	Modules *Registry
	// CredentialKeyPath 是平台层注册信息写入的设备凭据密钥文件（aeskey.key）。
	// 业务模块用它加解密自己保存的凭据（见 core.EncryptSecret / core.DecryptSecret）。
	CredentialKeyPath string
	// Registration 是平台层注册服务的校验入口（可为 nil，表示未注入）。
	Registration RegistrationChecker
}

// APIResponse 是老接口统一响应体：字段名与历史保持一致，禁止改名。
type APIResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
