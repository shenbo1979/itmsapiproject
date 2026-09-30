package olt

// 老接口的响应体：字段与 JSON 标签与老实现（/switchProject/switchmain/models.go 的 APIResponse）完全一致。
// 注意 **不能** 直接用 core.APIResponse：老 OLT 接口的日志/列表类响应会把 count / nPage / nLimit 等
// 分页字段放在**顶层**（老前端按 `res.count`、`res.nPage` 取值），少了它们界面分页会失效。
type APIResponse struct {
	Code              int    `json:"code"`
	Total             int    `json:"count,omitempty"`
	Page              int    `json:"nPage,omitempty"`
	Limit             int    `json:"nLimit,omitempty"`
	Field             string `json:"DataProp,omitempty"`
	Order             string `json:"Order,omitempty"`
	Message           string `json:"message"`
	Data              any    `json:"data,omitempty"`
	OntOnlines        int    `json:"OntOnlines,omitempty"`
	OntTotal          int    `json:"OntTotal,omitempty"`
	SlotidRefreshTime string `json:"SlotidRefreshTime,omitempty"`

	// VLAN 功能模块（handleVLANFunctionModule Act=0）的顶层返回字段。
	// 老壳 `getOltVlan` 按 `res.vlanifdata / vlandata / tranvlandata / routestaticdata` 取值，
	// 所以这些字段必须放在**顶层**（不能只塞进 data）。与老实现 models.go 的 APIResponse 逐字一致。
	VlanifData      any `json:"vlanifdata,omitempty"`
	VlanData        any `json:"vlandata,omitempty"`
	TranVlanData    any `json:"tranvlandata,omitempty"`
	RouteStaticData any `json:"routestaticdata,omitempty"`
}

// HandleVlan 是 handleVLANFunctionModule 的 saveData 结构（字段名与老实现一致）。
//
//	Act：动作（0 获取 / 1 新增 / 2 删除）；VlanID 共用（多个逗号分割、连续用 \"-\" 连接）；
//	Type：操作类型（0 VLAN/二层互通 / 1 上行VLAN透传 / 2 三层VLAN接口）；
//	VlanType：作用于 Type=0（0 Smart VLAN / 1 基本QinQ / 2 灵活QinQ）；
//	Fsp：机框/插槽/端口，作用于上行VLAN透传；
//	IP / Mask：作用于三层VLAN接口。
type HandleVLAN struct {
	Act      int    `json:"Act"`
	Type     int    `json:"Type"`
	VlanType int    `json:"VlanType"`
	VlanID   string `json:"VlanID"`
	Fsp      string `json:"Fsp"`
	IP       string `json:"IP"`
	Mask     string `json:"Mask"`
}

// LogListRequest 是 handleOltLogList 的 reqData 结构（字段名与老实现一致）。
type LogListRequest struct {
	SqlTime string `json:"SqlTime"`
	SqlStr  string `json:"SqlStr"`
	Ids     string `json:"Ids"`
	Limit   int    `json:"Limit"`
	Page    int    `json:"Page"`
	Field   string `json:"Field"`
	Order   string `json:"Order"`
}

// OltOperationLog 对应表 tbOltOperationLog（列名与 JSON 标签与老实现一致）。
type OltOperationLog struct {
	Id          int    `json:"nID" db:"nID"`
	OltName     string `json:"szOltName" db:"szOltName"`
	Menu        string `json:"szMenu" db:"szMenu"`
	Act         string `json:"szAct" db:"szAct"`
	OperationIP string `json:"szOperationIP" db:"szOperationIP"`
	Text        string `json:"szText" db:"szText"`
	CreatTime   string `json:"dtCreatTime" db:"dtCreatTime"`
	Remark      string `json:"szRemark" db:"szRemark"`
}

// ConnectParameters 是 OLT 连接参数（connData），字段名与老实现一致（供后续设备类接口使用）。
//
// 注意 Port 在老实现里是**字符串**（前端下拉/输入框传文本），这里保持不变，
// 以免老壳传 "23" 之类文本时 Unmarshal 失败；内部连接时再转成整数。
type ConnectParameters struct {
	Type     int    `json:"Type"`
	Username string `json:"Username"`
	Password string `json:"Password"`
	Port     string `json:"Port"`
	IP       string `json:"IP"`
}

// OltUser 对应表 tbOltUser（列名与 JSON 标签与老实现 models.go 的 OltUser 完全一致）。
type OltUser struct {
	Id           int    `json:"Id"`
	Username     string `json:"Username"`
	Password     string `json:"Password"`
	Port         string `json:"Port"`
	Type         int    `json:"Type"`
	Comment      string `json:"Comment"`
	IP           string `json:"IP"`
	Automatic    int    `json:"Automatic"`
	ScanInterval int    `json:"ScanInterval"`
	TemplateId   int    `json:"TemplateId"`
}

// DBAProfile 是 handleDBAProfile 的 saveData 结构（字段名与老实现一致）。
//
//	Act：动作（0 获取 / 1 新增 / 2 删除）；Id：模板ID(10-512)；Name：模板名(1-33)；
//	Type：模板类型(type1..type5)；Fix/Assure/Max：按类型对应的带宽(128-10000000)。
type DBAProfile struct {
	Act    int    `json:"Act"`
	Id     int    `json:"Id"`
	Name   string `json:"Name"`
	Type   string `json:"Type"`
	Fix    int    `json:"Fix"`
	Assure int    `json:"Assure"`
	Max    int    `json:"Max"`
}

// DBAProfileInfo 是 handleDBAProfile Act=0 返回的单条 DBA 模板信息（老实现 parseOLTDBAProfiles 的 map 字段）。
type DBAProfileInfo struct {
	Id     int    `json:"Id"`
	Name   string `json:"Name"`
	Type   string `json:"Type"`
	Fix    int    `json:"Fix"`
	Assure int    `json:"Assure"`
	Max    int    `json:"Max"`
}

// LineProfile 是 handleLineProfile 的 saveData 结构（字段名与老实现一致）。
//
//	Act：动作（0 获取 / 1 新增 / 2 删除）；Id：模板ID(1-8192，不可重复)；Name：模板名(1-33)；
//	DbaProfileId：绑定 DBA 模板 ID；PonType：PON 类型(gpon,epon)；
//	OpenTr069：启用 TR069 管理(0:不启用,1:启用;默认:1)；Vlan：VLAN 列表(多个 VLAN 逗号分隔,
//	每一种类型的业务占一行,行内用逗号分隔、不同类型业务用 \" / \" 分隔)。
type LineProfile struct {
	Act          int    `json:"Act"`
	Id           int    `json:"Id"`
	Name         string `json:"Name"`
	DbaProfileId int    `json:"DbaProfileId"`
	PonType      string `json:"PonType"`
	OpenTr069    int    `json:"OpenTr069"`
	Vlan         string `json:"Vlan"`
}

// OntSrvProfile 是 handleSrvProfile 的 saveData 结构（字段名与老实现一致）。
//
//	Act：动作（0 获取 / 1 新增 / 2 删除）；Id：模板ID(1-8192，不可重复)；Name：模板名(1-33)；
//	PonType：PON 类型(gpon,epon)；Pots：电话口数量(0-32，默认 0)；Eths：网口数量(0-24，默认 0)；
//	Vlan：业务 VLAN 列表（多个 VLAN 用逗号分隔，每一个业务 VLAN 对应一个网口）。
type OntSrvProfile struct {
	Act     int    `json:"Act"`
	Id      int    `json:"Id"`
	Name    string `json:"Name"`
	PonType string `json:"PonType"`
	Pots    string `json:"Pots"`
	Eths    string `json:"Eths"`
	Vlan    string `json:"Vlan"`
}

// OntSrvProfileInfo 是 handleSrvProfile Act=0 返回的单条 ONT 业务模板信息（老实现
// parseOLTSrvProfiles 的产物）。Id/Name/Type 与 SrvVlan[] 字段名与老前端
// chunk-ont_tr069Tem.js 渲染的层级（业务VLAN 子表：网口/业务VLAN）完全一致。
type OntSrvProfileInfo struct {
	Id      string    `json:"Id"`
	Name    string    `json:"Name"`
	Type    string    `json:"Type"`
	Pots    string    `json:"Pots"`
	Eths    string    `json:"Eths"`
	SrvVlan []SrvVlan `json:"SrvVlan"`
}

// SrvVlan 是 ONT 业务模板下一条「网口 → 业务VLAN」映射。
type SrvVlan struct {
	Eth  string `json:"Eth"`
	Vlan string `json:"Vlan"`
}

// Tr069Profile 是 handleTr069Profile 的 saveData 结构（字段名与老实现一致）。
//
//	Act：动作（0 获取 / 1 新增 / 2 删除）；Id：模板ID(1-32，不可重复)；Name：模板名(1-32)；
//	AcsUrl：ACS 地址(1-63)；AcsUserName：ACS 账号(1-50)；AcsPassword：ACS 密码(1-25)。
//	注：读取时**不回密码**（与老实现一致）。
type Tr069Profile struct {
	Act         int    `json:"Act"`
	Id          int    `json:"Id"`
	Name        string `json:"Name"`
	AcsUrl      string `json:"AcsUrl"`
	AcsUserName string `json:"AcsUserName"`
	AcsPassword string `json:"AcsPassword"`
}

// Tr069ProfileInfo 是 handleTr069Profile Act=0 返回的单条 TR069 管理模板信息（老实现
// parseOLTTR069Profiles 的 map 字段：Id/Name/AcsUrl/AcsUserName，不含密码）。
type Tr069ProfileInfo struct {
	Id          string `json:"Id"`
	Name        string `json:"Name"`
	AcsUrl      string `json:"AcsUrl"`
	AcsUserName string `json:"AcsUserName"`
}

// OntLineProfile 是 handleLineProfile Act=0 返回的单条 ONT 线路模板信息（老实现 parseOLTLineProfiles 的产物）。
// Id/Name/Type 与 GemMapping[] 字段名与老前端 dba_lineTem 页渲染的层级（T-CONT / DBA模版 / GEM Port /
// GEM 映射ID / VLAN 子表格）完全一致。
type OntLineProfile struct {
	Id         string       `json:"Id"`
	Name       string       `json:"Name"`
	Type       string       `json:"Type"`
	GemMapping []GemMapping `json:"GemMapping"`
}

// GemMapping 是一条线路模板下、某个 T-CONT 与其绑定 DBA 模板及所挂 GEM 端口的映射关系。
type GemMapping struct {
	TcontId      string    `json:"TcontId"`
	DbaProfileId string    `json:"DbaProfileId"`
	GemInfo      []GemInfo `json:"GemInfo"`
}

// GemInfo 是一个 GEM 端口的映射信息（GemToId/GemVlan 为逗号分隔的多个值）。
type GemInfo struct {
	GemPort string `json:"GemPort"`
	GemToId string `json:"GemToId"`
	GemVlan string `json:"GemVlan"`
}

// OltCurrentConfig 是 OLT 当前配置（板卡/版本/温度/ONT 统计）的结构化缓存，
// 字段与 JSON 标签与老实现一致（老前端按这些字段渲染 OLT 总览页）。
type OltCurrentConfig struct {
	ConnectTime       string         `json:"ConnectTime"`
	OLTRunTime        string         `json:"OLTRunTime"`
	RebootTime        string         `json:"RebootTime"`
	Version           string         `json:"Version"`
	Patch             string         `json:"Patch"`
	Product           string         `json:"Product"`
	Frame             string         `json:"Frame"`
	FrameType         string         `json:"FrameType"`
	OntOnlineCount    int            `json:"OntOnlineCount"`
	FrameTotal        int            `json:"FrameTotal"`
	FrameCount        int            `json:"FrameCount"`
	DefaultRoute      string         `json:"DefaultRoute"`
	SlotidRefreshTime string         `json:"SlotidRefreshTime"`
	FrameDesc         string         `json:"FrameDesc"`
	OntOnlines        int            `json:"OntOnlines"`
	OntTotal          int            `json:"OntTotal"`
	OltBoardInfo      []OltBoardInfo `json:"OltBoardInfo"`
	// Automatic 表示该设备当前是否处于"已连接/在线"状态（读自台账 tbOltUser.Automatic=1）：
	// Act=0 读缓存时连带返回，供前端在只看缓存时也能展示设备连接状态（只增字段，老前端忽略）。
	Automatic int `json:"Automatic,omitempty"`
}

// OltBoardInfo 是单块板卡的信息（老实现同名字段）。
type OltBoardInfo struct {
	Id          string     `json:"Id"`
	Name        string     `json:"Name"`
	Status      string     `json:"Status"`
	Online      bool       `json:"Online"`
	Type        string     `json:"Type"`
	Desc        string     `json:"Desc"`
	Temperature string     `json:"Temperature"`
	PortNum     int        `json:"PortNum"`
	PonType     string     `json:"PonType"`
	PonInfos    []PonInfos `json:"PonInfos"`
	GeInfos     []GeInfos  `json:"GeInfos"`
}

// PonInfos 是 PON 口信息（老实现同名字段）。
type PonInfos struct {
	Id         string `json:"Id"`
	FSP        string `json:"FSP"`
	Type       string `json:"Type"`
	OntTotal   int    `json:"OntTotal"`
	OntOnline  int    `json:"OntOnline"`
	PortStatus string `json:"PortStatus"`
}

// GeInfos 是 GE 口信息（老实现同名字段）。
type GeInfos struct {
	Id         string `json:"Id"`
	FSP        string `json:"FSP"`
	Type       string `json:"Type"`
	NativeVLAN string `json:"NativeVLAN"`
	Speed      string `json:"Speed"`
	Duplex     string `json:"Duplex"`
	Link       string `json:"Link"`
}

// BoardSlotInfo 是 handleBoardAndSlotInfo 的 saveData（SlotId 为空表示查询全部槽位）。
type BoardSlotInfo struct {
	SlotId string `json:"SlotId"`
}

// OntListRequest 是 handleONTListAndStatus 的 reqData（字段名与老实现一致）。
type OntListRequest struct {
	SqlBoardId string `json:"SqlBoardId"`
	SqlStr     string `json:"SqlStr"`
	SqlOnline  string `json:"SqlOnline"`
	Limit      int    `json:"Limit"`
	Page       int    `json:"Page"`
	Field      string `json:"Field"`
	Order      string `json:"Order"`
}

// OntListResponse 是单台 ONT 的汇总信息（字段名与老实现一致，也是槽位缓存文件的每行 JSON）。
type OntListResponse struct {
	PonType      string `json:"PonType"`
	FSP          string `json:"FSP"`
	OntId        string `json:"OntId"`
	Code         string `json:"Code"`
	SerialNumber string `json:"SerialNumber"`
	ControlFlag  string `json:"ControlFlag"`
	RunState     string `json:"RunState"`
	ConfigState  string `json:"ConfigState"`
	MatchState   string `json:"MatchState"`
	ProtectSide  string `json:"ProtectSide"`
	AuthType     string `json:"AuthType"`
	Protocol     string `json:"Protocol"`
	LineProfile  string `json:"LineProfile"`
	SrvProfile   string `json:"SrvProfile"`
	Desc         string `json:"Desc"`
	Mac          string `json:"Mac"`
	IP           string `json:"IP"`
}
