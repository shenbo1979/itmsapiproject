package switchmod

import (
	"net/http"

	"itmsapiproject/internal/core"
)

// 老接口的响应适配：搬运过来的代码原本依赖包级 writeJSON + APIResponse，
// 这里保留同名写法，内部统一走 core.WriteAPI（{code,message,data} + 服务端路径清洗），
// 避免为了改名而大面积改动被搬运的逻辑本身。
type APIResponse = core.APIResponse

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	if response, ok := v.(APIResponse); ok {
		core.WriteAPI(w, status, response.Code, response.Message, response.Data)
		return
	}
	if response, ok := v.(*APIResponse); ok && response != nil {
		core.WriteAPI(w, status, response.Code, response.Message, response.Data)
		return
	}
	core.WriteAPI(w, status, core.CodeOK, "success", v)
}
