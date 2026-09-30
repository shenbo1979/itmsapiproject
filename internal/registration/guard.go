package registration

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"itmsapiproject/internal/core"
)

// 老接口返回码语义（禁止改动）：未注册 -3、已过期 -2；无法校验注册状态按 -1 处理（HTTP 503）。
const (
	codeFail         = core.CodeFail
	codeExpired      = core.CodeExpired
	codeUnregistered = core.CodeUnregistered
)

const (
	unregisteredMessage = "系统尚未注册，请完成注册信息后继续使用"
	expiredMessage      = "注册信息已过期，请更新注册信息后继续使用"
)

// Validate 校验当前注册信息是否可用于业务操作：
// 未注册、已过期或注册状态无法校验时返回对应错误（nil 表示可以继续）。
func (s *Service) Validate() error {
	info, err := s.Info()
	if err != nil {
		return fmt.Errorf("无法校验注册状态，请稍后重试")
	}
	if !info.Registered {
		return errors.New(unregisteredMessage)
	}
	if info.Expired {
		return errors.New(expiredMessage)
	}
	return nil
}

// Require 供内部调用（后台采集任务、设备操作入口）使用：
// 外部请求已由 Guard 拦截，内部调用同样需要校验，避免未注册或已过期时仍然操作设备。
func (s *Service) Require() error {
	if err := s.Validate(); err != nil {
		s.logger.Printf("业务操作被拒绝: %v", err)
		return err
	}
	return nil
}

// Guard 拦截业务接口：注册信息无效时返回 -3 / -2，注册信息接口本身保持可用，
// 这样前端可以展示注册页并完成注册，而其他接口一律不可用。
//
// 拦截范围与老实现一致：只拦 /itms/* 前缀（新命名空间 /api/v2/* 供新壳使用，不受影响）。
func (s *Service) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresRegistration(r) || isRegistrationEndpoint(r) {
			next.ServeHTTP(w, r)
			return
		}

		info, err := s.Info()
		if err != nil {
			s.logger.Printf("校验注册状态失败: %v", err)
			writeLegacy(w, http.StatusServiceUnavailable, codeFail, "无法校验注册状态，请稍后重试", nil)
			return
		}
		if info.Registered && !info.Expired {
			next.ServeHTTP(w, r)
			return
		}
		code := codeExpired
		message := expiredMessage
		if !info.Registered {
			code = codeUnregistered
			message = unregisteredMessage
		}
		writeLegacy(w, http.StatusOK, code, message, info)
	})
}

// requiresRegistration 判断请求是否属于需要注册才能使用的业务接口。
func requiresRegistration(r *http.Request) bool {
	return r != nil && strings.HasPrefix(r.URL.Path, "/itms/")
}

// isRegistrationEndpoint 判断请求是否是注册链路本身的接口（必须始终放行）。
func isRegistrationEndpoint(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch strings.TrimRight(r.URL.Path, "/") {
	case strings.TrimRight(PathRegistrationInfo, "/"), strings.TrimRight(PathValidateToken, "/"):
		return true
	default:
		return false
	}
}
