package olt

import (
	"testing"
	"time"
)

// TestTraceAlwaysAccumulatesRegardlessOfEnabled 锁定诊断语义：
// 慢日志不依赖 trace=1 —— 即使 enabled=false（前端连接默认不发 trace），
// add 也累计各阶段/总耗时，handler 才能无关 trace 打印分段慢日志。
func TestTraceAlwaysAccumulatesRegardlessOfEnabled(t *testing.T) {
	tr := newOltTrace(false) // 前端默认：不传 trace
	tr.add("connect_telnet", 1500*time.Millisecond)
	tr.add("display_board_slot", 800*time.Millisecond)
	tr.add("display_board_slot", 700*time.Millisecond)

	if got := tr.totalElapsed(); got != 3*time.Second {
		t.Fatalf("disabled 也累计 totalElapsed = %v, want 3s", got)
	}
	summary := tr.summary()
	if summary == "" {
		t.Fatal("disabled 也应能生成 summary（供慢日志）")
	}
}

// TestWriteTraceDataNotLeakTimingsWhenDisabled 验证：enabled=false 时 writeTraceData
// 原样返回（结构体不包 {result,timings}）——这是前端 connect 不带 trace 也不会破坏渲染的保证。
func TestWriteTraceDataNotLeakTimingsWhenDisabled(t *testing.T) {
	tr := newOltTrace(false)
	tr.add("display_version", 500*time.Millisecond)
	type cfg struct{ Boards []int }
	in := cfg{Boards: []int{1, 2}}

	out := writeTraceData(in, tr)
	got, ok := out.(cfg)
	if !ok {
		t.Fatalf("disabled 时应原样返回结构体, got %T", out)
	}
	if len(got.Boards) != 2 {
		t.Fatalf("结构体内容应保留, got %+v", got)
	}
}
