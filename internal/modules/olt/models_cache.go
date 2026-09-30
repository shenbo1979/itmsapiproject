package olt

// 文件数据的表结构（2026-09-22 立项：把原先写在 /home/olt/<IP>/ 下的缓存改为入库表）。
//
// 为什么可以入库：四类文件都是**结构化数据**（JSON 或按行 JSON），字段固定、以设备为主键，
// 与 SQLite 的关系模型天然匹配；入库后可直接分页/筛选/排序（ONT 采集页需要），
// 也避免了"多进程同时读写同一 JSON 文本"的竞态。映射关系：
//
//	/home/olt/<IP>/currentconfiguration.json → tbOltDeviceSnapshot（标量）
//	                                         + tbOltDeviceBoard / tbOltDevicePonPort / tbOltDeviceGePort（嵌套数组拆子表）
//	/home/olt/<IP>/slot_ont_file/<槽位>.txt  → tbOltOntCache（每行一个 ONT JSON，一列一字段）
//	/home/olt/<IP>/savedconfiguration.txt    → tbOltConfigCache（Kind=savedconfiguration，TEXT 全文）
//	/home/olt/<IP>/olt_cur_cfg.txt           → tbOltConfigCache（Kind=currentconfig，TEXT 全文）
//	/home/olt/<IP>/ontautomaticcfg.json      → tbOltAutoRegConfig（口令列加密存放）
//
// 说明：老库里的 oltboard/oltontlist（迁移后为 tbOltBoard/tbOltOnt）是另一套较薄的缓存表，
// 与本文件对应、字段更全的 tbOltDevice* 系列**并存**，互不覆盖（详见 docs/02 §4.4 与 docs/09 §4）。
const (
	// ConfigKindSaved 是「已保存配置」文本（老 savedconfiguration.txt）。
	ConfigKindSaved = "savedconfiguration"
	// ConfigKindCurrent 是「当前配置」文本（老 olt_cur_cfg.txt）。
	ConfigKindCurrent = "currentconfig"
)

// OltDeviceSnapshot 是设备快照的标量部分（JSON 标签与老 currentconfiguration.json 逐字一致，
// 便于文件↔表对账）。
type OltDeviceSnapshot struct {
	HostIP            string `json:"-"`
	ConnectTime       string `json:"ConnectTime"`
	OLTRunTime        string `json:"OLTRunTime"`
	RebootTime        string `json:"RebootTime"`
	Version           string `json:"Version"`
	Patch             string `json:"Patch"`
	Product           string `json:"Product"`
	Frame             string `json:"Frame"`
	FrameType         string `json:"FrameType"`
	OntOnlineCount    int    `json:"OntOnlineCount"`
	FrameTotal        int    `json:"FrameTotal"`
	FrameCount        int    `json:"FrameCount"`
	DefaultRoute      string `json:"DefaultRoute"`
	SlotidRefreshTime string `json:"SlotidRefreshTime"`
	FrameDesc         string `json:"FrameDesc"`
	OntOnlines        int    `json:"OntOnlines"`
	OntTotal          int    `json:"OntTotal"`
	// SourceFile / SourceMTime 记录这条数据来自哪个文件、文件修改时间（首次从文件导入时写入；
	// 设备实时采集后 SourceFile 为空、OnlyFromFile=false）。
	SourceFile  string `json:"-"`
	SourceMTime string `json:"-"`
	UpdatedAt   string `json:"-"`
}

// OltOntCacheRow 是 ONT 缓存行（字段与老 slot_ont_file/*.txt 每行的 JSON 一一对应；
// 老 JSON 的 `IP` 是 ONT 的管理地址，与"设备 IP"区分，列名用 OntIP）。
type OltOntCacheRow struct {
	HostIP       string
	SlotID       string
	FSP          string
	OntId        string
	PonType      string
	Code         string
	SerialNumber string
	ControlFlag  string
	RunState     string
	ConfigState  string
	MatchState   string
	ProtectSide  string
	AuthType     string
	Protocol     string
	LineProfile  string
	SrvProfile   string
	Desc         string
	Mac          string
	OntIP        string
	SourceFile   string
	SourceMTime  string
	UpdatedAt    string
}

// OltAutoRegConfig 对应老 ontautomaticcfg.json（ONTAutoMatic）；Password 按"密文优先、明文兜底"存取。
type OltAutoRegConfig struct {
	HostIP       string `json:"-"`
	Type         int    `json:"Type"`
	Port         string `json:"Port"`
	Username     string `json:"Username"`
	Password     string `json:"Password,omitempty"`
	Registration int    `json:"Registration"`
	Relocation   int    `json:"Relocation"`
	TemplateId   int    `json:"TemplateId"`
	ScanInterval int    `json:"ScanInterval"`
	SourceFile   string `json:"-"`
	SourceMTime  string `json:"-"`
	UpdatedAt    string `json:"-"`
}

// OltFileImportResult 是一次"文件→表"导入的统计（供启动日志与工具输出）。
type OltFileImportResult struct {
	Devices      int // 导入的设备快照数
	Boards       int // 板卡行数
	PonPorts     int // PON 口行数
	GePorts      int // GE 口行数
	OntItems     int // ONT 缓存行数
	ConfigTexts  int // 文本配置条数
	AutoRegConfs int // 自动注册配置条数
	Skipped      int // 跳过的文件（解析失败/无内容）
}
