package switchmod

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"itmsapiproject/internal/core"
)

const testAESKey = "abcdefghijklmnopqrstuvwxyz012345" // 32 字节原文密钥

// newTestModule 在临时目录里起一套隔离的 switch 模块：模块库、登录记录文件、凭据密钥都落在 t.TempDir()。
func newTestModule(t *testing.T) *Module {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "aeskey.key")
	if err := os.WriteFile(keyPath, []byte(testAESKey), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envDBPath, filepath.Join(dir, "switch.db"))
	t.Setenv(envLoginFilePath, filepath.Join(dir, "loginfile.json"))

	module := New()
	ctx := &core.Context{
		Logger:            log.New(io.Discard, "", 0),
		DataDir:           dir,
		CredentialKeyPath: keyPath,
	}
	if err := module.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return module
}

// postForm 以老前端方式（POST + form）请求处理器，返回 code 与 data。
func postForm(t *testing.T, handler http.HandlerFunc, target, form string) (int, json.RawMessage) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	var payload struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, recorder.Body.String())
	}
	return payload.Code, payload.Data
}

// saveData 把结构体编码成老前端使用的 saveData 表单字段。
func saveData(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return "saveData=" + url.QueryEscape(string(raw))
}
