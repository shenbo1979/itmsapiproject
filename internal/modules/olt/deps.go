package olt

import (
	"strings"

	"itmsapiproject/internal/core"
)

// 本文件集中放"老实现里的包级全局/跨层调用"在新模块中的适配点，避免把全局状态带进来。

// requireRegistration 在操作设备前校验注册状态：
// 外部请求已由注册守卫拦截，模块内部调用（后台任务、设备操作）同样需要校验，
// 与 switch 模块 requireRegistration 同一口径（未注册/已过期时拒绝操作设备）。
func (m *Module) requireRegistration() error {
	if m == nil || m.ctx == nil || m.ctx.Registration == nil {
		return nil
	}
	return m.ctx.Registration.Require()
}

// logf 统一日志出口（无 logger 时静默，便于单测）。
func (m *Module) logf(format string, args ...any) {
	if m == nil || m.ctx == nil || m.ctx.Logger == nil {
		return
	}
	m.ctx.Logger.Printf(format, args...)
}

// decryptOltSecret 用平台层 AES 密钥解密凭据；密文非法（老库存量明文）时原样返回。
//
// 与 tools/migrate_olt_data.py 的 plain_value 同一口径：**密文优先、明文兜底**，
// 这样"迁移前写入的明文"与"新实现写入的密文"两代数据都能直接用。
func (m *Module) decryptOltSecret(value string) string {
	if m == nil || m.ctx == nil || m.ctx.CredentialKeyPath == "" {
		return value
	}
	key, err := core.LoadAESKey(m.ctx.CredentialKeyPath)
	if err != nil {
		return value
	}
	plaintext, err := core.DecryptSecret(value, key)
	if err != nil {
		return value
	}
	return plaintext
}

// encryptOltSecret 用平台层 AES 密钥加密凭据（入库前调用）；空值原样返回。
//
// 老口径：账号列可读、口令列加密（见 tools/migrate_olt_data.py 的说明）；
// 若值本身已是密文（前端把查询结果原样回传，例如只改备注），**不再套一层**，
// 否则会出现"能加密不能解密"的两层密文（与 switch 模块同一防护）。
// 密钥文件不可用时降级为明文保存并记日志——老库存量口令本就是明文，读取侧两种都能识别。
func (m *Module) encryptOltSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || m == nil || m.ctx == nil || m.ctx.CredentialKeyPath == "" {
		return value
	}
	key, err := core.LoadAESKey(m.ctx.CredentialKeyPath)
	if err != nil {
		m.logf("读取 AES 密钥失败，OLT 凭据按原文保存: %v", err)
		return value
	}
	if _, err := core.DecryptSecret(value, key); err == nil {
		return value // 已是密文
	}
	encrypted, err := core.EncryptSecret(value, key)
	if err != nil {
		m.logf("加密 OLT 凭据失败，按原文保存: %v", err)
		return value
	}
	return encrypted
}
