package olt

// collect_timeout_test.go —— 覆盖「OLT DBA 模板下发卡死」根因（collectSavedConfiguration 无限空转）
// 的回归：真机 MA5603T 的 `display current-configuration` 输出大、分段下发现，段间停顿常超 idle
// 静默窗口，`idlePrompts` 会被持续到达的数据反复清零、永远到不了 >oltMaxIdlePrompts；若设备又
// 卡在某个交互参数提示上，`return` 结束行也永不出现。修复前该循环没有绝对值上限，会一直空转、
// 占用会话写锁并拖死后续所有写入。
//
// 本文件通过一个「每次 Read 都吐一段非空回显、永不返回 `return`」的假会话，验证新增的总兜底
// 超时（collectSavedConfigurationWithin 的 timeout 参数）会让采集必然收敛而不是死循环。

import (
	"strings"
	"testing"
	"time"

	"itmsapiproject/internal/oltconn"
)

// streamingNoReturnSession 每次都返回一段非空文本（永远不等于 `return`），并且不回 `#` 结尾。
// 模拟真机大配置回显被分成多段、段间停顿导致 idlePrompts 无法累计到收尾阈值的情形。
type streamingNoReturnSession struct {
	readC int
}

func (s *streamingNoReturnSession) Send(command string) error { return nil }
func (s *streamingNoReturnSession) Kind() oltconn.Kind        { return oltconn.KindTelnet }
func (s *streamingNoReturnSession) Close() error              { return nil }
func (s *streamingNoReturnSession) ReadPaged(timeout time.Duration) (string, error) {
	return s.Read(timeout)
}
func (s *streamingNoReturnSession) Read(timeout time.Duration) (string, error) {
	// 每轮都返回非空、非 `return`、非分页、非交互提示的行，让 idlePrompts 恒为 0。
	s.readC++
	return "  port vlan 100 0/3/0\r\n", nil
}

// TestCollectSavedConfigurationTimesOutOnStreamingDevice 覆盖根因：设备持续回显却永不出现
// `return` 时，采集必须在总超时内报错返回，而不是无限空转（修复前会卡死，用例会超时失败）。
func TestCollectSavedConfigurationTimesOutOnStreamingDevice(t *testing.T) {
	device := &oltDevice{module: nil, session: &streamingNoReturnSession{}}
	// 用很短的总超时（如 120ms）快速验证收敛路径；生产入口走 30s 的 oltConfigCollectTimeout。
	const short = 120 * time.Millisecond
	start := time.Now()
	_, err := device.collectSavedConfigurationWithin(short)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("持续回显且永不 return 的设备应返回超时错误，got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timed out") {
		t.Fatalf("错误应为超时语义，got: %v", err)
	}
	// 应大致按短超时返回（允许系统调度抖动，但绝不能接近无上界的数秒）。
	if elapsed > 5*short {
		t.Fatalf("采集未按总超时收敛：elapsed=%s limit=%s", elapsed, short)
	}
}

// TestCollectSavedConfigurationSucceedsOnWellBehaved 回归：正常设备按 `return` 结束仍能成功。
func TestCollectSavedConfigurationSucceedsOnWellBehaved(t *testing.T) {
	device := &oltDevice{session: &returnChunkSession{}}
	got, err := device.collectSavedConfigurationWithin(5 * time.Second)
	if err != nil {
		t.Fatalf("正常设备应采集成功: %v", err)
	}
	if !strings.Contains(got, "hostname OLT-TEST") {
		t.Fatalf("采集内容缺失: %q", got)
	}
}

// returnChunkSession 先回显多段正文，最后给出一行 `return` 结束。
type returnChunkSession struct{ index int }

func (s *returnChunkSession) Send(command string) error { return nil }
func (s *returnChunkSession) Kind() oltconn.Kind        { return oltconn.KindTelnet }
func (s *returnChunkSession) Close() error              { return nil }
func (s *returnChunkSession) ReadPaged(timeout time.Duration) (string, error) {
	return s.Read(timeout)
}
func (s *returnChunkSession) Read(timeout time.Duration) (string, error) {
	chunks := []string{
		"  !\r\n  hostname OLT-TEST\r\n",
		"  #\r\n",
		"  return\r\n",
	}
	if s.index >= len(chunks) {
		time.Sleep(timeout)
		return "", nil
	}
	chunk := chunks[s.index]
	s.index++
	return chunk, nil
}
