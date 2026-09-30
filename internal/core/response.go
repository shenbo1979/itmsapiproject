package core

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// serverPathPattern 匹配服务端绝对路径（以常见系统目录开头），用于避免把服务器目录信息返回给前端。
// 只匹配目录前缀，避免误伤类似 Ten-GigabitEthernet1/0/30 的正常文本。
var serverPathPattern = regexp.MustCompile(`/(?:dev|opt|home|etc|var|tmp|run|root|usr|srv|mnt|media|proc|sys|switchfile|txapikeygen|switchapi|tx)(?:/[A-Za-z0-9._-]+)*`)

// SanitizeClientMessage 去除返回给前端的消息中的服务端目录信息（详细原因仍写入服务端日志）。
func SanitizeClientMessage(message string) string {
	if message == "" || !strings.Contains(message, "/") {
		return message
	}
	return serverPathPattern.ReplaceAllString(message, "服务端文件")
}

// WriteAPI 以老接口统一格式输出响应（{code,message,data}），并清洗消息里的服务端目录信息。
// 全工程（兼容层、平台层注册、各业务模块）共用这一份实现，避免各处字段或清洗策略不一致。
func WriteAPI(w http.ResponseWriter, status, code int, message string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	payload := APIResponse{Code: code, Message: SanitizeClientMessage(message), Data: data}
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteMethodNotAllowed 统一处理非 POST 请求（老接口只接受 POST）。
func WriteMethodNotAllowed(w http.ResponseWriter) {
	WriteAPI(w, http.StatusMethodNotAllowed, CodeFail, "method not allowed", nil)
}
