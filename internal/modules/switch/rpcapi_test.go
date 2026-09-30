package switchmod

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestExecuteCmdsAuthFailureReturnsPromptly 锁定一个实测过的设备行为：
// 盛科（Centec）RPC-API 在凭据错误时返回 401，但**既不带 Content-Length 也不关闭连接**。
// Go 的 http 客户端按“读到 EOF”处理这种响应体，若不主动要求关闭连接就会一直等到 30s 超时，
// 把“口令不对”伪装成“设备不可达”。本用例用同样行为的假设备验证：
//  1. 请求带 Connection: close（req.Close）；
//  2. 401 立即返回带状态码的错误，而不是等到客户端超时。
func TestExecuteCmdsAuthFailureReturnsPromptly(t *testing.T) {
	module := newTestModule(t)

	var sawConnectionClose atomic.Bool
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		closeAfterResponse := r.Close || strings.EqualFold(r.Header.Get("Connection"), "close")
		if closeAfterResponse {
			sawConnectionClose.Store(true)
		}
		// 模拟真实设备：401 + 无 Content-Length（不写 Content-Length 即“无分帧”）。
		w.Header().Set("WWW-Authenticate", `Basic real="CentecRpcApi"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Unauthorized user, invalid user name or password\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if closeAfterResponse {
			// 真实设备（BaseHTTP）收到 Connection: close 会立刻关闭连接 → 客户端读到 EOF 立即返回。
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			return
		}
		// 未要求关闭连接：像真实设备那样一直挂着（客户端会等它到超时）。
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	host, port := splitHostPort(t, server.URL)
	started := time.Now()
	_, err := module.executeCmds(host, port, []string{"show version"}, "text", "probe", "probe")
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("凭据错误时应返回错误，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "unexpected status 401") {
		t.Fatalf("错误信息应显式包含状态码，实际：%v", err)
	}
	if !sawConnectionClose.Load() {
		t.Fatal("请求未携带 Connection: close，无法从“401 无分帧”响应中及时脱身")
	}
	// 假设备会把连接挂住 3 秒；带 Connection: close 时客户端应几乎立刻拿到 401。
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("401 应在 1.5s 内返回（实测 %v）——否则又退化成超时", elapsed)
	}
}

func splitHostPort(t *testing.T, rawURL string) (string, int64) {
	t.Helper()
	trimmed := strings.TrimPrefix(rawURL, "http://")
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}
	port, err := strconv.ParseInt(portText, 10, 64)
	if err != nil {
		t.Fatalf("解析测试服务端口失败: %v", err)
	}
	return host, port
}

var _ = context.Background
