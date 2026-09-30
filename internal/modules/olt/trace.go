package olt

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// oltTrace 是老实现 newOLTConnectTrace / newONTListStatusTrace / newONTRegistrationTrace 的统一实现：
// 老接口支持 trace=1，此时把各阶段耗时放进 data.timings（老前端调试面板用）。
//
// 与老实现的一致性：阶段名沿用老实现的写法（connect_telnet / display_version / display_board_slot ...），
// 值为 time.Duration 的字符串（如 "1.234s"），并额外带 total（老实现 summary() 的等价物）。
type oltTrace struct {
	enabled bool
	mu      sync.Mutex
	stages  map[string]string
	order   []string
	total   time.Duration
}

// newOltTrace 创建跟踪器。enabled 只控制 writeTraceData 是否把 timings 合进响应；
// 各阶段/总耗时始终累计，以便 handler 在请求超阈值时打诊断慢日志（见 handleOltConnect /
// handleBoardAndSlotInfo：无关 trace=1，单次采集超 3 秒即打印分段耗时）。
func newOltTrace(enabled bool) *oltTrace {
	return &oltTrace{enabled: enabled, stages: map[string]string{}}
}

// add 累计某个阶段的耗时（同名阶段多次发生时累加，与老实现 stages[stage] 的写法一致）。
// 无论 enabled 与否都累计，供慢请求诊断日志使用；enabled 只影响 writeTraceData 的输出。
func (t *oltTrace) add(stage string, elapsed time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.stages[stage]; !exists {
		t.order = append(t.order, stage)
	}
	current, _ := time.ParseDuration(t.stages[stage])
	t.stages[stage] = (current + elapsed).String()
	t.total += elapsed
}

// snapshot 返回阶段 → 耗时的副本（始终累计；是否随响应返回由 writeTraceData 依据 enabled 决定）。
func (t *oltTrace) snapshot() map[string]string {
	result := map[string]string{}
	if t == nil {
		return result
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, stage := range t.order {
		result[stage] = t.stages[stage]
	}
	result["total"] = t.total.String()
	return result
}

// totalElapsed 返回累计耗时（用于慢请求日志）。
func (t *oltTrace) totalElapsed() time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

// summary 返回 `stage=耗时, stage=耗时` 形式的摘要（与老实现 summary() 同用途）。
func (t *oltTrace) summary() string {
	snapshot := t.snapshot()
	if len(snapshot) == 0 {
		return ""
	}
	stages := make([]string, 0, len(snapshot))
	for stage := range snapshot {
		stages = append(stages, stage)
	}
	sort.Strings(stages)
	parts := make([]string, 0, len(stages))
	for _, stage := range stages {
		parts = append(parts, stage+"="+snapshot[stage])
	}
	return strings.Join(parts, ", ")
}

// writeTraceData 按老实现 respond() 的口径把 timings 合进响应 data：
// map 直接加字段；nil 变成 {timings:...}；其余包装成 {result:..., timings:...}。
func writeTraceData(data any, trace *oltTrace) any {
	if trace == nil || !trace.enabled {
		return data
	}
	timings := trace.snapshot()
	switch typed := data.(type) {
	case map[string]any:
		typed["timings"] = timings
		return typed
	case nil:
		return map[string]any{"timings": timings}
	default:
		return map[string]any{"result": typed, "timings": timings}
	}
}

// paginateBounds 与老实现（tr069handler.go 中的同名函数）逐行一致：
// 页码超过总页数时收敛到末页，start/end 为切片边界。
func paginateBounds(page, limit, count int) (int, int, int) {
	totalPages := 0
	if count > 0 {
		totalPages = count / limit
		if count%limit != 0 {
			totalPages++
		}
	}
	if page > totalPages {
		page = totalPages
	}
	start := limit*page - limit
	if start < 0 {
		start = 0
	}
	end := page * limit
	if end > count {
		end = count
	}
	return page, start, end
}

// atoiOrZeroStrict 与 atoiOrZero 同义，供需要显式表达"解析失败即 0"的场景使用。
func atoiOrZeroStrict(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}
