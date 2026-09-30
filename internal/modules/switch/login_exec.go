// 由 tools/port_switch_batch2.py 从 switchapi/switch_login_file.go 的 77-84（ExecuteCmdsWithLoginInfo） 段搬运。
package switchmod

// SwitchLoginInfo 是一次设备连接使用的登录信息（老实现同名类型）。
type SwitchLoginInfo struct {
	Username string
	Password string
	Maker    string
}

func (m *Module) ExecuteCmdsWithLoginInfo(host string, port int64, cmds []string, format string, loginInfo SwitchLoginInfo) (*JSONRPCResponse, error) {
	rpcResp, err := m.executeCmds(host, port, cmds, format, loginInfo.Username, loginInfo.Password)
	if err != nil {
		return nil, err
	}
	m.recordLoginConnection(host, port, 1, loginInfo.Username, loginInfo.Password, loginInfo.Maker)
	return rpcResp, nil
}
