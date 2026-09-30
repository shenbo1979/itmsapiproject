package core

import (
	"fmt"
	"sort"
	"strings"
)

// ModuleInfo 是 /api/capabilities 中单个模块的描述。
type ModuleInfo struct {
	ID        string   `json:"id"`
	Version   string   `json:"version"`
	DependsOn []string `json:"dependsOn,omitempty"`
	Nav       Nav      `json:"nav"`
	Menus     []Menu   `json:"menus,omitempty"`
}

// Capabilities 是前端壳与运维工具的能力声明（前端据此渲染菜单，无需硬编码）。
type Capabilities struct {
	Product    string       `json:"product"`
	Version    string       `json:"version"`
	APIVersion int          `json:"apiVersion"`
	Modules    []ModuleInfo `json:"modules"`
}

// Registry 持有全部可用模块，并按配置启用其中一部分。
type Registry struct {
	available map[string]Module
	enabled   []string
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{available: map[string]Module{}}
}

// Add 注册可用模块（不做依赖校验，校验发生在 Enable）。
func (r *Registry) Add(modules ...Module) error {
	for _, module := range modules {
		if module == nil {
			continue
		}
		id := strings.TrimSpace(module.ID())
		if id == "" {
			return fmt.Errorf("模块 ID 不能为空")
		}
		if _, exists := r.available[id]; exists {
			return fmt.Errorf("模块重复注册：%s", id)
		}
		r.available[id] = module
	}
	return nil
}

// Available 返回全部可用模块 id（排序后）。
func (r *Registry) Available() []string {
	ids := make([]string, 0, len(r.available))
	for id := range r.available {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AllModules 返回全部随包模块（含未启用），供兼容层登记“本路径属于哪个模块”，
// 从而在模块未安装时返回 code=-4，而不是含糊的 404。
func (r *Registry) AllModules() []Module {
	modules := make([]Module, 0, len(r.available))
	for _, id := range r.Available() {
		modules = append(modules, r.available[id])
	}
	return modules
}

// EnabledIDs 返回已启用模块 id（按依赖顺序）。
func (r *Registry) EnabledIDs() []string {
	ids := make([]string, 0, len(r.enabled))
	ids = append(ids, r.enabled...)
	return ids
}

// IsEnabled 判断模块是否已启用。
func (r *Registry) IsEnabled(id string) bool {
	for _, enabled := range r.enabled {
		if enabled == id {
			return true
		}
	}
	return false
}

// Enable 按依赖拓扑启用指定模块；缺依赖时返回错误（用于安装期快速失败）。
func (r *Registry) Enable(ids ...string) error {
	wanted := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := r.available[id]; !ok {
			return fmt.Errorf("模块未随包安装：%s（可用：%s）", id, strings.Join(r.Available(), ","))
		}
		wanted[id] = true
	}

	resolved := []string{}
	state := map[string]int{} // 0 未处理 1 处理中 2 完成
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("模块依赖存在环：%s", id)
		}
		state[id] = 1
		module := r.available[id]
		for _, dep := range module.DependsOn() {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if _, ok := r.available[dep]; !ok {
				return fmt.Errorf("模块 %s 依赖 %s，但该模块未随包安装", id, dep)
			}
			wanted[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		resolved = append(resolved, id)
		return nil
	}

	ordered := make([]string, 0, len(wanted))
	for id := range wanted {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		if err := visit(id); err != nil {
			return err
		}
	}

	r.enabled = resolved
	return nil
}

// EnabledModules 返回已启用模块（按依赖顺序）。
func (r *Registry) EnabledModules() []Module {
	modules := make([]Module, 0, len(r.enabled))
	for _, id := range r.enabled {
		modules = append(modules, r.available[id])
	}
	return modules
}

// Capabilities 汇总当前启用能力。
func (r *Registry) Capabilities(product, version string) Capabilities {
	info := Capabilities{Product: product, Version: version, APIVersion: 2}
	for _, module := range r.EnabledModules() {
		info.Modules = append(info.Modules, ModuleInfo{
			ID:        module.ID(),
			Version:   module.Version(),
			DependsOn: module.DependsOn(),
			Nav:       module.Nav(),
			Menus:     module.Menus(),
		})
	}
	return info
}
