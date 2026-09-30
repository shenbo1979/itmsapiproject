// Package switchmod 是 switch 功能模块（交换机运维）。
//
// M1 迁入范围（第一批：设备台账与基础资料）：
//
//	/itms/handleSwitchVenue/        场景（tbVenue）
//	/itms/handleSwitchManufacturer/ 制造商（tbManufacturer）
//	/itms/handleSwitchPurpose/      用途（tbPurpose）
//	/itms/handleSwitchProtocol/     协议（tbProtocol）
//	/itms/handleSwitchModel/        型号（tbModel）
//	/itms/handleSwitchInfo/         设备台账（tbDeviceInformation，含 RPC 账号密码加解密）
//
// 仍未迁入（第二批：需要 SSH/Telnet/RPC 客户端或它产出的采集缓存）：
//
//	/itms/handleDeviceInformation/  设备详情（nAct=1 需要实时采集）
//	/itms/handleSwitchPortDetail/   端口详情与操作
//	/itms/handleSwitchVlan/         端口 VLAN 查询与下发
//	/itms/handleVlanInformation/、/itms/handleVlanClassifier/、/itms/getTerminalMAC/
//	/itms/handleSwitchTerminal/     SSH 交互终端
//	/itms/handleSwitchSnifferConfig/、/itms/handleSwitchCollectionSettings/  采集与嗅探配置
//	/itms/handleRpcApiConfig/       RPC-API 接口设置
//
// 迁移原则（与 docs/05 一致）：
//  1. 严禁包级全局：DB、密钥路径、日志全部由 core.Context 注入，锁放在 Store 内部；
//  2. 一模块一库：本模块独占自己的 SQLite（默认 <DataDir>/switch.db），不再与别的进程共写；
//  3. 接口契约逐字保持：路径、nAct 语义、字段名（szName/szRpcUsername…）、返回体 {code,message,data} 都不变；
//  4. 设备 RPC 账号密码沿用老实现的可移植 AES-GCM 线格式（IV||密文||tag 的 base64），
//     密钥来自平台层写入的 aeskey.key，因此老库里的密文在新模块可以直接解出来。
package switchmod

// SwitchVenue 对应 tbVenue（字段名与老工程 models.go 一致，禁止改名）。
type SwitchVenue struct {
	Id   int64  `json:"nId" db:"nId"`
	Name string `json:"szName" db:"szName"`
}

// SwitchManufacturer 对应 tbManufacturer。
type SwitchManufacturer struct {
	Id   int64  `json:"nId" db:"nId"`
	Name string `json:"szName" db:"szName"`
}

// SwitchPurpose 对应 tbPurpose。
type SwitchPurpose struct {
	Id   int64  `json:"nId" db:"nId"`
	Name string `json:"szName" db:"szName"`
}

// SwitchProtocol 对应 tbProtocol。
type SwitchProtocol struct {
	Id   int64  `json:"nId" db:"nId"`
	Name string `json:"szName" db:"szName"`
}

// SwitchModel 对应 tbModel。
type SwitchModel struct {
	Id             int64  `json:"nId" db:"nId"`
	Name           string `json:"szName" db:"szName"`
	VenueId        int64  `json:"nVenueId" db:"nVenueId"`
	PurposeId      int64  `json:"nPurposeId" db:"nPurposeId"`
	ProtocolId     int64  `json:"nProtocolId" db:"nProtocolId"`
	EthPort        int64  `json:"nEthPort" db:"nEthPort"`
	CreationTime   string `json:"szCreationTime" db:"szCreationTime"`
	Remarks        string `json:"szRemarks" db:"szRemarks"`
	ManufacturerId int64  `json:"nManufacturerId" db:"nManufacturerId"`
}

// SwitchDeviceInformation 对应 tbDeviceInformation（设备台账）。
// RpcUsername/RpcPassword 在响应中已是明文（与老实现一致：读取时解密）。
type SwitchDeviceInformation struct {
	Id          int64  `json:"nId" db:"nId"`
	Name        string `json:"szName" db:"szName"`
	DevMac      string `json:"szDevMac" db:"szDevMac"`
	ModelId     int64  `json:"nModelId" db:"nModelId"`
	PurposeId   int64  `json:"nPurposeId" db:"nPurposeId"`
	ManageIP    string `json:"szManageIP" db:"szManageIP"`
	Gateway     string `json:"szGateway" db:"szGateway"`
	SubnetMask  string `json:"szSubnetMask" db:"szSubnetMask"`
	ServerIP    string `json:"szServerIP" db:"szServerIP"`
	AutoEnable  int64  `json:"nAutoEnable" db:"nAutoEnable"`
	DevPassword string `json:"szDevPassword" db:"szDevPassword"`
	Remarks     string `json:"szRemarks" db:"szRemarks"`
	RpcHostip   string `json:"szRpcHostip" db:"szRpcHostip"`
	RpcPort     int64  `json:"nRpcPort" db:"nRpcPort"`
	RpcUsername string `json:"szRpcUsername" db:"szRpcUsername"`
	RpcPassword string `json:"szRpcPassword" db:"szRpcPassword"`
	RpcType     int64  `json:"nRpcType" db:"nRpcType"`
}
