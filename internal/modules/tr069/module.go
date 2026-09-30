// Package tr069 是 TR069/CPE 功能模块。
//
// 组成（见 docs/00-overview.md）：
//   - Go 侧 16 条 /itms/handleTr069* 等（M2 从老单体迁入）；
//   - Python 侧 31 条 /client/fnXxx（/itms-py，是否随包迁入待业务确认）；
//   - socket 层（itmssocketmain，8012）与 CPE 主动上报相关。
package tr069

import (
	"net/http"

	"itmsapiproject/internal/core"
)

const (
	// ModuleID 与 deploy/modules/tr069.manifest 的模块 id 一致。
	ModuleID = "tr069"
	// ModuleVersion 为模块自身版本。
	ModuleVersion = "0.1.0-m0"
)

// Module 实现 core.Module。
type Module struct{}

// New 返回 tr069 模块实例。
func New() core.Module { return Module{} }

// ID 返回模块标识。
func (Module) ID() string { return ModuleID }

// Version 返回模块版本。
func (Module) Version() string { return ModuleVersion }

// DependsOn 声明依赖：TR069 与 OLT 共用 ONT/CPE 上下文，因此依赖 olt。
func (Module) DependsOn() []string { return []string{"olt"} }

// Nav 返回本模块在一级导航中的标题与排序（口径与 tr069.manifest 的 module.nav_title 一致）。
func (Module) Nav() core.Nav {
	return core.Nav{Title: "TR069 管理", Order: 20}
}

// Menus 声明模块在一级导航下的 TAB。
func (Module) Menus() []core.Menu {
	return []core.Menu{
		{ID: "tr069.cpe", Title: "光猫管理", Path: "/tr069/cpe", Order: 10},
		{ID: "tr069.template", Title: "配置模板", Path: "/tr069/template", Order: 20},
		{ID: "tr069.group", Title: "分组与策略", Path: "/tr069/group", Order: 30},
	}
}

// LegacyRoutes 返回由 tr069 模块负责的 Go 侧老接口（字面量来自 tests/golden/routes.json）。
// Python 侧 31 条 /client/fnXxx 由 /itms-py 提供，是否随包迁入待业务确认（见 docs/03）。
func (Module) LegacyRoutes() []string {
	return []string{
		"/itms/handleCpeGroupInfo/",
		"/itms/handleDeviceDelete/",
		"/itms/handleDeviceInfo/",
		"/itms/handleDeviceStatus/",
		"/itms/handleEditRoom/",
		"/itms/handleItmsUpgraded/",
		"/itms/handleItmsVersion/",
		"/itms/handleServerConfig/",
		// handleTestDiag 原先在清单里未归类；依据老单体定义位置
		// （/switchProject/switchmain/tr069handler.go）归 tr069。
		"/itms/handleTestDiag/",
		"/itms/handleTr069LogList/",
		"/itms/handleTr069Poliey/",
		// handleTr069Profile 已划归 olt 模块（OLT 详情页「业务/TR069模板」TAB）：依据老单体定义位置
		// （/switchProject/switchmain/olthandler.go 2960 行起）与运行时上下文（操作 OLT 设备、
		// 解析 OLT 的 [global-config]），见 docs/generated/olt-contract.md 第 2 节。
		"/itms/handleTr069TemplateList/",
		"/itms/handleUploadFile/",
		"/itms/handleUserAction/",
		"/itms/handleVersionInfo/",
		"/itms/handleWorkOrderList/",
	}
}

// Register 注册本模块新命名空间路由。
func (Module) Register(mux *http.ServeMux, _ *core.Context) error {
	mux.HandleFunc("/api/v2/tr069/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"module":"tr069","version":"` +
			ModuleVersion + `","stage":"M0 骨架：M2 迁入业务处理器 + 文件入库"}}`))
	})
	return nil
}

// Migrate 负责本模块库表（/home/tx/tr069.db，M2 实现：文件→表迁移）。
func (Module) Migrate(_ *core.Context) error { return nil }
