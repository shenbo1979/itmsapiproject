// 由 tools/port_switch_batch2.py 从 switchapi/models.go 按类型名提取（第二批所需的结构体）。
package switchmod

import (
	"encoding/json"
	"net"
	"time"

	sshlib "golang.org/x/crypto/ssh"
)

// JSONRPCRequest JSON-RPC 2.0 请求
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  []CmdParams `json:"params"`
	ID      string      `json:"id"`
}

// CmdParams 命令参数
type CmdParams struct {
	Format  string   `json:"format"`
	Version int      `json:"version"`
	Cmds    []string `json:"cmds"`
}

// JSONRPCResponse JSON-RPC 2.0 响应
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError JSON-RPC 错误
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Client struct {
	// owner 指向所属模块：reconnect 需要复用模块的注册校验与连接配置（老实现用包级函数）。
	owner         *Module
	conn          *sshlib.Client
	host          string
	port          int64
	username      string
	password      string
	kexAlgorithms []string
}

type InteractiveOptions struct {
	PreCommands       []string
	PromptSuffixes    []string
	MorePatterns      []string
	ExitCommand       string
	CommandDelay      time.Duration
	ReadTimeout       time.Duration
	WaitAfterCommands time.Duration
	AutoConfirm       bool
	AutoExit          bool
	StopOnErrorOutput bool
}

type SwitchInterfaceInfo struct {
	ID                    int
	Port                  string
	Status                string
	Duplex                string
	Speed                 string
	Mode                  string
	Type                  string
	Description           string
	TransceiverDiagnostic string //收发器诊断   - available：设备返回了实时 DDM 光模块数据 - unsupported：模块存在，但不支持诊断信息 - absent：模块缺失 - unknown：没有拿到明确状态
	ModuleType            string
	Wavelength            string
	Temperature           float64 //温度
	Voltage               float64 //电压
	Current               float64 //电流
	TxPower               float64 //发射功率
	RxPower               float64 //接收功率
	VlanMapping           SwitchVlanMapping
}

type TransceiverInfo struct {
	Port                  string //接口名称
	TransceiverDiagnostic string
	ModuleType            string
	Wavelength            string
	Temperature           float64 //温度
	Voltage               float64 //电压
	Current               float64 //电流
	TxPower               float64 //发射功率
	RxPower               float64 //接收功率
}

type SwitchVlanMapping struct {
	Port            string //接口类型和编号
	AggregationPort string `json:"aggregationPort"` //成员接口所属的聚合接口
	LinkType        string //接口链路类型 access,trunk,hybrid,dot1q-tunnel
	PVID            int64  //接口所属的缺省VLAN ID。缺省情况下，所有接口的缺省VLAN为VLAN1
	TrunkVLANList   string //1、接口动态加入的VLAN ID。2、静态配置允许通过的VLAN ID
	PortDescription string //标识二层接口的描述信息
}

type RoutingTable struct {
	Destination       string `json:"destination,omitempty"`       //目的网段
	Mask              string `json:"mask,omitempty"`              //目的网段掩码
	PrefixLength      string `json:"prefixLength,omitempty"`      //目的网段前缀长度
	Protocol          string `json:"protocol,omitempty"`          //路由协议
	Preference        string `json:"preference,omitempty"`        //路由优先级
	Cost              string `json:"cost,omitempty"`              //路由开销
	NextHop           string `json:"nextHop,omitempty"`           //下一跳地址
	Interface         string `json:"interface,omitempty"`         //出接口
	ManagementGateway string `json:"managementGateway,omitempty"` //交换机自身管理网关
}

// SwitchDeviceDetail 实时采集的设备详情（设备级信息 + 接口列表）
type SwitchDeviceDetail struct {
	Hostip             string                    `json:"szHostip"`                     //设备IP
	Online             int                       `json:"online"`                       //在线状态：1 在线(可连接)，0 离线
	DeviceName         string                    `json:"deviceName,omitempty"`         //设备名称
	SoftwareVersion    string                    `json:"softwareVersion,omitempty"`    //软件版本
	Uptime             string                    `json:"uptime,omitempty"`             //运行时长
	CPUUsage           float64                   `json:"cpuUsage,omitempty"`           //CPU 五秒平均利用率
	CPUInterrupt       float64                   `json:"cpuInterrupt,omitempty"`       //CPU 五秒平均中断利用率
	MemoryAllocated    int64                     `json:"memoryAllocated,omitempty"`    //已分配内存字节数
	DeviceTemperature  float64                   `json:"deviceTemperature,omitempty"`  //设备传感器最高温度
	FanSpeedRatio      float64                   `json:"fanSpeedRatio,omitempty"`      //风扇平均转速比
	Fans               []SwitchFanInfo           `json:"fans,omitempty"`               //风扇状态
	TemperatureSensors []SwitchTemperatureSensor `json:"temperatureSensors,omitempty"` //设备温度传感器
	RoutingTable       []RoutingTable            `json:"routingTable,omitempty"`       //设备级路由表信息
	Interfaces         []SwitchInterfaceInfo     `json:"interfaces"`                   //接口信息列表
}

type SwitchFanInfo struct {
	ID              string  `json:"id"`
	Index           string  `json:"index"`
	Type            string  `json:"type,omitempty"`
	Status          string  `json:"status"`
	Speed           float64 `json:"speedRatio"`
	Mode            string  `json:"mode"`
	HardwareVersion string  `json:"hardwareVersion,omitempty"`
	SerialNumber    string  `json:"serialNumber,omitempty"`
}

type SwitchTemperatureSensor struct {
	Index       string  `json:"index"`
	Temperature float64 `json:"temperature"`
	Position    string  `json:"position"`
	Status      string  `json:"status,omitempty"`
}

// SwitchRequest 前端发送的请求
type SwitchRequest struct {
	DeviceID  int64    `json:"nDeviceId,omitempty"`  //设备台账主键，仅用于同步在线状态
	Type      int64    `json:"nType"`                //连接类型（1：RPC-API；2：TELNET；3：SSH）;
	Hostip    string   `json:"szHostip"`             //服务器IP，设备IP(IPv4或IPv6)；字符串;
	Port      int64    `json:"nPort,omitempty"`      //服务器端口(当连接类型为RPC时端口为1025-65535)；数值
	Username  string   `json:"szUsername,omitempty"` //连接账号
	Password  string   `json:"szPassword,omitempty"` //连接密码
	Interface string   `json:"szInterface,omitempty"`
	Vlan      string   `json:"szVlan,omitempty"`
	VlanName  string   `json:"szVlanName,omitempty"`
	Cmds      []string `json:"cmds,omitempty"` //执行命令
	Format    string   `json:"format,omitempty"`
	Maker     string   `json:"szMaker,omitempty"` //厂商名称
}

// LocalDeviceInformation 本地缓存的设备信息（读取 /home/switchfile/devicefile/ 下的 json 文件）
type LocalDeviceInformation struct {
	Hostip string          `json:"szHostip"` //设备IP，对应文件名(去掉.json后缀)
	Data   json.RawMessage `json:"data"`     //文件中保存的设备信息原始JSON内容
}

// InterfaceVlanif 存储接口信息
type InterfaceVlanif struct {
	VlanId         string `json:"vlanId,omitempty"` //VLAN ID
	Name           string `json:"name,omitempty"`
	IPAddress      string `json:"ipAddress,omitempty"`
	SubnetMask     string `json:"subnetMask,omitempty"`
	SubnetMaskBits int    `json:"subnetMaskBits,omitempty"`
	EthName        string `json:"ethName,omitempty"`
	EthMode        string `json:"ethMode,omitempty"`
	EthPVID        int    `json:"ethPVID,omitempty"`
	EthJoinVlan    string `json:"ethJoinVlan,omitempty"`
	OldEthPVID     int    `json:"oldEthPVID,omitempty"`
	OldEthJoinVlan string `json:"oldEthJoinVlan,omitempty"`
}

type SwitchInfos struct {
	ProductModel      string            `json:"productModel"`      //产品型号
	SystemImage       string            `json:"systemImage"`       //系统映像
	WebImage          string            `json:"webImage"`          //Web映像
	SerialNumber      string            `json:"serialNumber"`      //序列号
	DeviceName        string            `json:"deviceName"`        //设备名称
	BootRomVersion    string            `json:"bootRomVersion"`    //BootRom版本
	MACAddress        string            `json:"macAddress"`        //MAC地址
	HardwareVersion   string            `json:"hardwareVersion"`   //硬件版本
	Runtime           string            `json:"runtime"`           //运行时间
	SoftwareVersion   string            `json:"softwareVersion"`   //软件版本
	InterfaceEthTotal int               `json:"interfaceEthTotal"` //以太网接口总数
	EthInfos          map[string]string `json:"ethInfos"`          //以太网接口信息列表
}

// VLAN列表信息结构体

// VLAN列表信息结构体
type VLANInfo struct {
	Mode            int    `json:"mode,omitempty"`            //配置模式：0:单个;1：范围;
	VlanId          string `json:"vlanId"`                    //VLAN ID
	Name            string `json:"name"`                      //VLAN名称
	Status          string `json:"status"`                    //VLAN状态
	STPID           string `json:"stpId,omitempty"`           //STP ID
	MemberPort      string `json:"memberPort"`                //成员端口
	MacLearning     string `json:"macLearning,omitempty"`     //MAC学习
	MacLimitAction  string `json:"macLimitAction,omitempty"`  //MAC动作
	MacLimitMaximum int    `json:"macLimitMaximum,omitempty"` //MAC最大条目
}

// VLAN分类规则结构体
type VLANClassific struct {
	RuleId      int    `json:"ruleId,omitempty"`      //规则ID(0-4095)
	RuleType    string `json:"ruleType,omitempty"`    //规则类型(ip,mac,protocol)
	RuleContent string `json:"ruleContent,omitempty"` //规则内容
	VlanId      int    `json:"vlanId,omitempty"`      //vlanId(1-4094)
	GroupId     int    `json:"groupId,omitempty"`     //组ID列表(0-31)
	EthPort     string `json:"ethPort,omitempty"`     //端口
}

/*
查询请求结构体
CPE终端列表请求结构体:
*/

// OLT用户结构体
type TerminalUser struct {
	TerminalUserName string `json:"TerminalUserName"`
	TerminalPassword string `json:"TerminalPassword"`
}

// Telnet客户端结构体

// 交换机SSH连接配置
type SwitchSSHConfig struct {
	Host          string
	Port          int64
	Username      string
	Password      string
	kexAlgorithms []string
}

// LocalDeviceInformation 本地缓存的设备信息（读取 /home/switchfile/devicefile/ 下的 json 文件）

type SwitchPortVlanRequest struct {
	Port            string `json:"port"`
	AggregationPort string `json:"aggregationPort"`
	LinkType        string `json:"linkType,omitempty"`
	PVID            int64  `json:"pvid,omitempty"`
	TrunkVLANList   string `json:"trunkVlanList,omitempty"`
	PortDescription string `json:"portDescription,omitempty"`
}

type TerminalMACInfo struct {
	MAC      string `json:"mac"`
	VLAN     string `json:"vlan"`
	Port     string `json:"port,omitempty"`
	Type     string `json:"type,omitempty"`
	RawEntry string `json:"rawEntry,omitempty"`
}

type SwitchPortDetailRequest struct {
	PortEnabled     *bool  `json:"portEnabled,omitempty"`
	PortDescription string `json:"portDescription,omitempty"`
}

type SwitchPortDetailBasicInfo struct {
	PortName      string `json:"portName"`
	PortStatus    string `json:"portStatus,omitempty"`
	PortSpeed     string `json:"portSpeed,omitempty"`
	PortBandWidth string `json:"portBandWidth,omitempty"`
}

type SwitchPortPacketStats struct {
	InBroadcast  int64 `json:"inBroadcast"`
	OutBroadcast int64 `json:"outBroadcast"`
	InCRC        int64 `json:"inCRC"`
	InError      int64 `json:"inError"`
	OutError     int64 `json:"outError"`
	InDiscard    int64 `json:"inDiscard"`
	OutDiscard   int64 `json:"outDiscard"`
	InPause      int64 `json:"inPause"`
	OutPause     int64 `json:"outPause"`
	InBytes      int64 `json:"inBytes"`
	OutBytes     int64 `json:"outBytes"`
	InRate       int64 `json:"inRate"`
	OutRate      int64 `json:"outRate"`
}

type SwitchPortOpticalInfo struct {
	TransceiverDiagnostic string  `json:"transceiverDiagnostic,omitempty"`
	ModuleType            string  `json:"moduleType,omitempty"`
	Wavelength            string  `json:"wavelength,omitempty"`
	Temperature           float64 `json:"temperature,omitempty"`
	Voltage               float64 `json:"voltage,omitempty"`
	Current               float64 `json:"current,omitempty"`
	RxPower               float64 `json:"rxPower"`
	TxPower               float64 `json:"txPower"`
}

type SwitchPortTrafficPoint struct {
	CollectedAt  string `json:"collectedAt"`
	InBroadcast  int64  `json:"inBroadcast"`
	OutBroadcast int64  `json:"outBroadcast"`
	InCRC        int64  `json:"inCRC"`
	InError      int64  `json:"inError"`
	OutError     int64  `json:"outError"`
	InDiscard    int64  `json:"inDiscard"`
	OutDiscard   int64  `json:"outDiscard"`
	InPause      int64  `json:"inPause"`
	OutPause     int64  `json:"outPause"`
	InBytes      int64  `json:"inBytes"`
	OutBytes     int64  `json:"outBytes"`
	InRate       int64  `json:"inRate"`
	OutRate      int64  `json:"outRate"`
}

type SwitchPortTrafficTrend struct {
	InRateMax  int64                    `json:"inRateMax"`
	OutRateMax int64                    `json:"outRateMax"`
	OneHour    []SwitchPortTrafficPoint `json:"oneHour"`
	OneDay     []SwitchPortTrafficPoint `json:"oneDay"`
	SevenDays  []SwitchPortTrafficPoint `json:"sevenDays"`
}

type SwitchPortVLANItem struct {
	VLAN       string `json:"vlan"`
	VPCName    string `json:"vpcName,omitempty"`
	Gateway    string `json:"gateway,omitempty"`
	SubnetMask string `json:"subnetMask,omitempty"`
}

type SwitchPortDetail struct {
	Hostip             string                    `json:"szHostip"`
	Port               string                    `json:"szInterface"`
	BasicInfo          SwitchPortDetailBasicInfo `json:"basicInfo"`
	PacketStats        SwitchPortPacketStats     `json:"packetStats"`
	OpticalInfo        SwitchPortOpticalInfo     `json:"opticalInfo"`
	TrafficTrend       SwitchPortTrafficTrend    `json:"trafficTrend"`
	VLANList           []SwitchPortVLANItem      `json:"vlanList"`
	CurrentDescription string                    `json:"currentDescription,omitempty"`
	LinkType           string                    `json:"linkType,omitempty"`
	PVID               int64                     `json:"pvid,omitempty"`
	TrunkVLANList      string                    `json:"trunkVlanList,omitempty"`
	TerminalMAC        []TerminalMACInfo         `json:"terminalMAC"`
	TerminalMACError   string                    `json:"terminalMACError,omitempty"`
}

// TelnetClient 是 Telnet 连接客户端（老实现 models.go 同名类型）。
type TelnetClient struct {
	// owner 指向所属模块（注册校验/日志注入；老实现用包级函数）。
	owner   *Module
	Host    string
	Port    int64
	Maker   string
	Conn    net.Conn
	Timeout time.Duration
}

type RpcApiConfig struct {
	Hostip   string `json:"hostip"`             //设备IP(IPv4)
	Apiport  int64  `json:"apiport"`            //API端口(1025-65535)
	Username string `json:"username,omitempty"` //用户名(可选)
	Password string `json:"password,omitempty"` //密码(可选)
	Enable   string `json:"enable,omitempty"`   // RPC-API 服务状态(enable或disable)
}
