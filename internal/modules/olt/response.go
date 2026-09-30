package olt

import (
	"encoding/json"
	"net/http"

	"itmsapiproject/internal/core"
)

// writeJSON 保留老实现的同名写法（搬运过来的 handler 不必改名），但按本模块的 APIResponse 全字段输出
// （含 count/nPage/nLimit 等顶层分页字段），并复用 core 的服务端路径清洗。
func writeJSON(w http.ResponseWriter, status int, v any) {
	if response, ok := v.(APIResponse); ok {
		response.Message = core.SanitizeClientMessage(response.Message)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	core.WriteAPI(w, status, core.CodeOK, "success", v)
}
