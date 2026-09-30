// Package compat 实现老接口兼容层（M0 只落骨架，具体映射在 M1/M2 填充）。
//
// 兼容规则见 docs/01-api-compat.md：
//   - 老路径 /itms/handleXxx/ 原样保留，由显式映射表转发到模块处理器；
//   - 未命中且模块未安装 → code=-4；已安装但无该路由 → 明确的“接口不存在”提示（不裸奔 404）；
//   - 大小写不一致（如 /itms/HandleUploadFile/）要同时接受；
//   - query 与 form 两种传参都要能取到（老前端用 axios params）。
package compat

import (
	"encoding/json"
	"net/http"
	"strings"

	"itmsapiproject/internal/core"
)

// Route 是一条老接口到模块处理器的映射。
type Route struct {
	Path   string
	Module string
	Legacy bool // true 表示由老接口兼容层提供，false 表示模块的新命名空间
	Handle http.HandlerFunc
}

// ModuleState 是兼容层需要的“模块安装状态”视图（由注册表实现）。
type ModuleState interface {
	// IsEnabled 判断某模块当前是否已安装/启用。
	IsEnabled(module string) bool
	// EnabledIDs 返回已安装模块 id（用于 -4 响应中的 installed 列表）。
	EnabledIDs() []string
}

// Router 负责老接口分发与未安装模块的错误码。
type Router struct {
	routes map[string]Route
	state  ModuleState
}

// NewRouter 创建兼容层；state 由模块注册表提供。
func NewRouter(state ModuleState) *Router {
	router := &Router{routes: map[string]Route{}, state: state}
	router.routes["/api/capabilities"] = Route{Path: "/api/capabilities", Module: "platform"}
	router.routes["/healthz"] = Route{Path: "/healthz", Module: "platform"}
	return router
}

// Mount 注册一条老接口（path 需与 tests/golden/routes.json 中的字面量一致）。
func (r *Router) Mount(path, module string, handle http.HandlerFunc) {
	r.routes[path] = Route{Path: path, Module: module, Legacy: true, Handle: handle}
	// 兼容历史大小写差异：同时接受 path 的大小写变体请求（见 docs/01 第 2.5 条）。
}

// Handle 注册模块自身的新命名空间路由（/api/v2/<module>/...）。
func (r *Router) Handle(path, module string, handle http.HandlerFunc) {
	r.routes[path] = Route{Path: path, Module: module, Handle: handle}
}

// Routes 返回已注册路由（供启动日志与自检使用）。
func (r *Router) Routes() []Route {
	list := make([]Route, 0, len(r.routes))
	for _, route := range r.routes {
		list = append(list, route)
	}
	return list
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	if req.Method != http.MethodPost && req.Method != http.MethodGet {
		WriteLegacy(w, http.StatusMethodNotAllowed, core.CodeFail, "method not allowed", nil)
		return
	}
	if route, ok := r.routes[path]; ok {
		r.dispatch(w, req, route)
		return
	}
	// 大小写容错：老前端存在 /itms/HandleUploadFile/ 这类与路由大小写不一致的调用。
	for registered, route := range r.routes {
		if strings.EqualFold(registered, path) {
			r.dispatch(w, req, route)
			return
		}
	}
	// Python 侧接口（/client/fnXxx）：M0 阶段仍由 /itms-py 提供服务，统一工程尚未接管。
	if strings.HasPrefix(path, "/client/") {
		WritePythonProviderNotice(w, path)
		return
	}
	WriteUnknownInterface(w, path)
}

func (r *Router) dispatch(w http.ResponseWriter, req *http.Request, route Route) {
	if route.Legacy && route.Module != "platform" && r.state != nil && !r.state.IsEnabled(route.Module) {
		WriteModuleAbsent(w, route.Module, r.state.EnabledIDs())
		return
	}
	route.Handle(w, req)
}

// WriteLegacy 以老接口格式输出响应（{code,message,data}）。
func WriteLegacy(w http.ResponseWriter, status, code int, message string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	payload := core.APIResponse{Code: code, Message: message, Data: data}
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteModuleAbsent 输出“模块未安装”（-4），供前端隐藏菜单并给出可读提示。
func WriteModuleAbsent(w http.ResponseWriter, module string, installed []string) {
	WriteLegacy(w, http.StatusOK, core.CodeModuleAbsent,
		"当前设备未安装该功能模块（"+module+"）",
		map[string]any{"module": module, "installed": installed})
}

// WriteUnknownInterface 处理已装模块但路径不存在的调用，避免老前端拿到裸 404。
func WriteUnknownInterface(w http.ResponseWriter, path string) {
	WriteLegacy(w, http.StatusNotFound, core.CodeFail,
		"接口不存在："+path, map[string]any{"path": path})
}

// WritePythonProviderNotice 用于 /client/* 这类仍由 Python（/itms-py）提供的接口，
// 让老前端得到可读说明而不是 404。
func WritePythonProviderNotice(w http.ResponseWriter, path string) {
	WriteLegacy(w, http.StatusOK, core.CodeFail,
		"该接口由 TR069/Python 服务提供，统一工程尚未接管",
		map[string]any{"path": path, "provider": "python:/itms-py/vTr69Api.py", "stage": "M0"})
}

// WriteNotMigrated 用于“模块已安装、接口仍在迁移中”的过渡期：
// 明确告知前端与调用方，避免误判为参数错误。
func WriteNotMigrated(w http.ResponseWriter, module, path string) {
	WriteLegacy(w, http.StatusOK, core.CodeFail,
		"接口尚未迁移到统一工程（过渡期）："+path,
		map[string]any{"module": module, "path": path, "stage": "M0",
			"hint": "M1/M2 按 docs/01-api-compat.md 迁入后返回真实结果"})
}
