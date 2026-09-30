package core

import (
	"encoding/json"
	"net/http"
	"testing"
)

type stubModule struct {
	id    string
	order int
	title string
	menus []Menu
}

func (m stubModule) ID() string                              { return m.id }
func (m stubModule) Version() string                         { return "test" }
func (m stubModule) DependsOn() []string                     { return nil }
func (m stubModule) Nav() Nav                                { return Nav{Title: m.title, Order: m.order} }
func (m stubModule) Menus() []Menu                           { return m.menus }
func (m stubModule) LegacyRoutes() []string                  { return nil }
func (m stubModule) Register(*http.ServeMux, *Context) error { return nil }
func (m stubModule) Migrate(*Context) error                  { return nil }

// TestCapabilitiesExposesNav 校验 /api/capabilities 会下发模块的一级导航信息
// （前端据此渲染「一级=模块、模块内=TAB」，不允许前端硬编码模块清单）。
func TestCapabilitiesExposesNav(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Add(
		stubModule{id: "switch", order: 30, title: "交换机管理", menus: []Menu{{ID: "switch.ledger", Title: "设备台账", Path: "/switch/devices", Order: 10}}},
		stubModule{id: "olt", order: 10, title: "OLT/ONU 管理", menus: []Menu{{ID: "olt.ont", Title: "ONT 管理", Path: "/olt/ont", Order: 20}}},
	); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := registry.Enable("olt", "switch"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	payload := registry.Capabilities("txconsole", "0.1.0-m1")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Modules []struct {
			ID  string `json:"id"`
			Nav struct {
				Title string `json:"title"`
				Order int    `json:"order"`
			} `json:"nav"`
			Menus []Menu `json:"menus"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Modules) != 2 {
		t.Fatalf("modules = %d; want 2", len(decoded.Modules))
	}
	for _, module := range decoded.Modules {
		if module.Nav.Title == "" {
			t.Fatalf("模块 %s 缺少 nav.title（前端一级导航将回退为模块 id）", module.ID)
		}
		if len(module.Menus) == 0 {
			t.Fatalf("模块 %s 缺少 menus（前端 TAB 将为空）", module.ID)
		}
	}
	if decoded.Modules[0].ID != "olt" || decoded.Modules[1].ID != "switch" {
		t.Fatalf("模块顺序 = %s,%s; want olt,switch（按依赖拓扑）", decoded.Modules[0].ID, decoded.Modules[1].ID)
	}
}
