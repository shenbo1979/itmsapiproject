package switchmod

// 本文件集中放"老实现里的包级全局/跨层调用"在新模块中的适配点，避免把全局状态带进来。

// requireRegistration 在操作设备前校验注册状态：
// 外部请求已由注册守卫拦截，模块内部调用（后台采集、嗅探、设备操作）同样需要校验，
// 与老实现的 requireSwitchRegistration() 语义一致（未注册/已过期时拒绝操作设备）。
func (m *Module) requireRegistration() error {
	if m == nil || m.ctx == nil || m.ctx.Registration == nil {
		return nil
	}
	return m.ctx.Registration.Require()
}

// recordLoginConnection 记录一次成功登录（账号密码加密后写入登录记录文件）；
// 失败只写日志，不影响设备操作主流程（与老实现 recordSwitchLoginConnection 一致）。
func (m *Module) recordLoginConnection(host string, port, connectionType int64, username, password, maker string) {
	if m == nil || m.cipher == nil {
		return
	}
	if err := m.cipher.RecordLoginConnection(host, port, connectionType, username, password, maker); err != nil {
		m.logf("记录交换机登录信息失败(%s): %v", host, err)
	}
}

// logf 统一日志出口（无 logger 时静默，便于单测）。
func (m *Module) logf(format string, args ...any) {
	if m == nil || m.ctx == nil || m.ctx.Logger == nil {
		return
	}
	m.ctx.Logger.Printf(format, args...)
}
