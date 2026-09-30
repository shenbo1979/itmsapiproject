package core

// CredentialRotator 是模块可选的扩展能力：平台层 AES 密钥（注册信息里的 szAesKey）发生变化时，
// 模块需要用它自己保存的旧/新密钥重新加密模块内的凭据（例如交换机设备的 RPC 账号密码、
// 登录记录文件），否则旧数据在新密钥下无法解密。
//
// 归属说明（见 docs/05-m1-web-registration.md）：注册/授权属于平台层，密钥文件由平台层写入；
// 但“谁保存了用该密钥加密的数据”只有模块自己知道，因此平台层通过本接口回调各模块，
// 而不是像老实现那样跨模块直接 UPDATE tbDeviceInformation、直接改写 /home/switchfile/loginfile.json。
type CredentialRotator interface {
	Module
	// RotateCredentials 把本模块内用 oldKeyText 加密的凭据改为 newKeyText 加密，要求幂等。
	// 实现失败时必须返回错误且不改动数据（平台层会把失败模块记录到日志，注册本身不回滚）。
	RotateCredentials(oldKeyText, newKeyText string) error
}

// CredentialRotators 返回已启用模块中实现了 CredentialRotator 的模块，以及未实现该能力的模块 id。
// 未实现的模块由调用方（平台注册服务）记录待办日志，避免静默丢失凭据轮换。
func (r *Registry) CredentialRotators() (rotators []CredentialRotator, pending []string) {
	for _, module := range r.EnabledModules() {
		if rotator, ok := module.(CredentialRotator); ok {
			rotators = append(rotators, rotator)
			continue
		}
		pending = append(pending, module.ID())
	}
	return rotators, pending
}
