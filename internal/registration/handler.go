package registration

import (
	"log"
	"net/http"
	"strconv"

	"itmsapiproject/internal/core"
)

// 老接口路径与新命名空间路径（新壳使用新路径，老壳继续用老路径，两者语义完全一致）。
const (
	// PathRegistrationInfo 是注册信息接口（nAct=0 读取、nAct=1 保存），老壳调用路径。
	PathRegistrationInfo = "/itms/handleRegistrationInfo/"
	// PathValidateToken 是登录令牌校验接口（P1，属登录链路）。
	PathValidateToken = "/itms/validateThirdToken/"
	// PathPlatformRegistration 是新前端壳使用的平台命名空间路径，语义同 PathRegistrationInfo。
	PathPlatformRegistration = "/api/v2/platform/registration/"
)

// HandleRegistrationInfo 处理注册信息读写：
//
//	nAct=0 读取当前注册信息（含未注册时的草稿记录，供注册页直接回显）；
//	nAct=1 保存注册信息（saveData 为 JSON，兼容 query 与 form 两种传参）。
//
// 返回体与老实现逐字段一致（{code,message,data}），前端无需改动即可接管。
func (s *Service) HandleRegistrationInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLegacy(w, http.StatusMethodNotAllowed, codeFail, "method not allowed", nil)
		return
	}

	act, err := strconv.Atoi(r.FormValue("nAct"))
	if err != nil {
		writeLegacy(w, http.StatusOK, codeFail, "invalid nAct: "+err.Error(), nil)
		return
	}

	switch act {
	case 0:
		info, err := s.Info()
		if err != nil {
			s.logger.Printf("读取注册信息失败: %v", err)
			writeLegacy(w, http.StatusInternalServerError, codeFail, "读取注册信息失败: "+err.Error(), nil)
			return
		}
		writeLegacy(w, http.StatusOK, core.CodeOK, "success", info)
	case 1:
		var record Record
		if err := decodeFormJSON(r, "saveData", &record); err != nil {
			writeLegacy(w, http.StatusOK, codeFail, "invalid request: "+err.Error(), nil)
			return
		}
		if err := validateBrowserTime(record.BrowserTime, s.now()); err != nil {
			writeLegacy(w, http.StatusOK, codeFail, err.Error(), nil)
			return
		}
		if err := s.Save(&record); err != nil {
			s.logger.Printf("保存注册信息失败: %v", err)
			writeLegacy(w, http.StatusOK, codeFail, "保存注册信息失败: "+err.Error(), nil)
			return
		}
		// 统一按授权状态（签名文件）读取到期时间，“注册”与“续期”返回同一口径。
		info, err := s.Info()
		if err != nil {
			s.logger.Printf("保存后读取注册信息失败: %v", err)
			writeLegacy(w, http.StatusOK, codeFail, "读取注册信息失败: "+err.Error(), nil)
			return
		}
		writeLegacy(w, http.StatusOK, core.CodeOK, "success", info)
	default:
		writeLegacy(w, http.StatusOK, codeFail, "invalid nAct", nil)
	}
}

// HandleValidateThirdToken 处理登录令牌校验。
//
// 现状说明（与当前线上一致）：接口本身只做方法校验并返回成功；
// 老实现里真正的令牌检查由 requireLogin 中间件完成，而 switchapi 的入口只挂了注册校验
// （见 /switchapi/main.go 的说明：“部署在可信内网，接口请求不需要登录令牌”）。
// 统一工程沿用同一策略，登录链路改造留待平台层统一认证时处理。
func (s *Service) HandleValidateThirdToken(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("handleValidateThirdToken method=%s", r.Method)
	if r.Method != http.MethodPost {
		writeLegacy(w, http.StatusMethodNotAllowed, codeFail, "method not allowed", nil)
		return
	}
	writeLegacy(w, http.StatusOK, core.CodeOK, "success", nil)
}

// writeLegacy 以老接口格式输出响应（{code,message,data}），并清洗消息里的服务端目录信息。
func writeLegacy(w http.ResponseWriter, status, code int, message string, data any) {
	core.WriteAPI(w, status, code, message, data)
}

// SetLogger 替换日志器（便于测试与宿主程序统一日志格式）。
func (s *Service) SetLogger(logger *log.Logger) {
	if logger != nil {
		s.logger = logger
		s.cfg.Logger = logger
	}
}
