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
//  1. 严禁包级全局：DB、密钥路径、日志全部由 core.Context 注入，锁放在 Store/CredentialCipher 内部；
//  2. 一模块一库：本模块独占自己的 SQLite（默认 <DataDir>/switch.db），不再与别的进程共写；
//  3. 接口契约逐字保持：路径、nAct 语义、字段名（szName/szRpcUsername…）、返回体 {code,message,data} 都不变；
//  4. 设备 RPC 账号密码沿用老实现的可移植 AES-GCM 线格式（IV||密文||tag 的 base64），
//     密钥来自平台层写入的 aeskey.key，因此老库里的密文在新模块可以直接解出来。
package switchmod

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"itmsapiproject/internal/core"
)

const (
	// ModuleID 与打包清单 deploy/modules/switch.manifest 的模块 id 一致。
	ModuleID = "switch"
	// ModuleVersion 记录模块自身版本，便于 /api/capabilities 与灰度升级。
	ModuleVersion = "0.1.0-m1"
	// envDBPath 可覆盖模块库位置（默认 <TXAPI_DATA_DIR>/switch.db）。
	envDBPath = "TXAPI_SWITCH_DB"
	// envLoginFilePath 可覆盖登录记录文件位置（默认 /home/switchfile/loginfile.json）。
	envLoginFilePath = "TXAPI_SWITCH_LOGIN_FILE"
	// envDeviceFileDir 可覆盖设备采集缓存目录（默认 <DataDir>/devicefile；老实现为 /home/switchfile/devicefile/）。
	envDeviceFileDir = "TXAPI_SWITCH_DEVICE_DIR"
	// envCollectionDir 可覆盖采集文件导入目录（默认 <DataDir>/collectionfile）。
	envCollectionDir = "TXAPI_SWITCH_COLLECTION_DIR"
	// envSnifferDir 可覆盖嗅探目录（默认 <DataDir>，配置文件为 `<dir>/sniffinginfocfg.json`）。
	envSnifferDir = "TXAPI_SWITCH_SNIFFER_DIR"
)

// Module 实现 core.Module：持有本模块独占的数据库与凭据加解密器。
// 不再使用任何包级全局（老实现的 GetDB()/switchLoginFileMu/switchSnifferDir 等已移除）。
type Module struct {
	ctx    *core.Context
	db     *sql.DB
	store  *Store
	cipher *CredentialCipher

	// 设备采集缓存目录（老实现 /home/switchfile/devicefile/）：<DataDir>/devicefile
	deviceFileDir string
	// 采集文件导入目录（老实现 /home/switchfile/collectionfile）：<DataDir>/collectionfile
	collectionDir string
	// 嗅探目录与配置路径（老实现 /home/switchfile + /sniffinginfocfg.json）：<DataDir> 下同名文件
	snifferDir        string
	snifferConfigPath string
	snifferMu         sync.Mutex
	snifferManager    *SwitchSnifferManager

	// 采集配置/任务（老实现为包级 switchCollection* 变量）
	collectionSettingsPath string
	collectionMu           sync.Mutex
	collectionFileMu       sync.Mutex
	collectionRunMu        sync.Mutex
	collectionManager      *SwitchCollectionManager
}

// New 返回 switch 模块实例。
func New() *Module { return &Module{} }

// ID 返回模块标识。
func (m *Module) ID() string { return ModuleID }

// Version 返回模块版本。
func (m *Module) Version() string { return ModuleVersion }

// DependsOn 声明依赖：switch 模块不依赖其他业务模块。
func (m *Module) DependsOn() []string { return nil }

// Nav 返回本模块在一级导航中的标题与排序。
// 口径与 deploy/modules/switch.manifest 的 module.nav_title 保持一致。
func (m *Module) Nav() core.Nav {
	return core.Nav{Title: "交换机管理", Order: 30}
}

// Menus 声明本模块在一级导航下的 TAB（未安装该模块时壳不会渲染）。
func (m *Module) Menus() []core.Menu {
	return []core.Menu{
		{ID: "switch.ledger", Title: "设备台账", Path: "/switch/devices", Order: 10},
		{ID: "switch.masterdata", Title: "基础资料", Path: "/switch/masterdata", Order: 20},
		{ID: "switch.panel", Title: "设备面板", Path: "/switch/panel", Order: 30},
		{ID: "switch.collection", Title: "采集配置", Path: "/switch/collection", Order: 40},
	}
}

// LegacyRoutes 返回由 switch 模块负责的老接口（字面量来自 tests/golden/routes.json）。
// 注：validateThirdToken 属登录链路，划归平台层（随任意包安装），不在此列出。
func (m *Module) LegacyRoutes() []string {
	return []string{
		"/itms/getTerminalMAC/",
		"/itms/handleDeviceInformation/",
		"/itms/handleRpcApiConfig/",
		"/itms/handleSwitchCollectionSettings/",
		"/itms/handleSwitchInfo/",
		"/itms/handleSwitchManufacturer/",
		"/itms/handleSwitchModel/",
		"/itms/handleSwitchPortDetail/",
		"/itms/handleSwitchProtocol/",
		"/itms/handleSwitchPurpose/",
		"/itms/handleSwitchSnifferConfig/",
		"/itms/handleSwitchTerminal/",
		"/itms/handleSwitchVenue/",
		"/itms/handleSwitchVlan/",
		"/itms/handleVlanClassifier/",
		"/itms/handleVlanInformation/",
	}
}

// LegacyHandlers 返回本模块已经完成迁移的老接口处理器；
// 其余老接口由兼容层继续返回“尚未迁移”的过渡响应（见 cmd/txapi/main.go 的挂载顺序）。
func (m *Module) LegacyHandlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/itms/handleSwitchVenue/":        m.HandleVenue,
		"/itms/handleSwitchManufacturer/": m.HandleManufacturer,
		"/itms/handleSwitchPurpose/":      m.HandlePurpose,
		"/itms/handleSwitchProtocol/":     m.HandleProtocol,
		"/itms/handleSwitchModel/":        m.HandleModel,
		"/itms/handleSwitchInfo/":         m.HandleDeviceLedger,
		// 第二批（SSH/Telnet/RPC 客户端 + 采集与嗅探）
		"/itms/handleDeviceInformation/":        m.handleDeviceInformation,
		"/itms/handleSwitchPortDetail/":         m.handleSwitchPortDetail,
		"/itms/handleSwitchVlan/":               m.handleSwitchVlan,
		"/itms/handleVlanInformation/":          m.handleVlanInformation,
		"/itms/handleVlanClassifier/":           m.handleVlanClassifier,
		"/itms/getTerminalMAC/":                 m.getTerminalMAC,
		"/itms/handleSwitchTerminal/":           m.handleSwitchTerminal,
		"/itms/handleSwitchSnifferConfig/":      m.handleSwitchSnifferConfig,
		"/itms/handleSwitchCollectionSettings/": m.handleSwitchCollectionSettings,
		"/itms/handleRpcApiConfig/":             m.handleRpcApiConfig,
	}
}

// Register 注册本模块的新命名空间路由（/api/v2/switch/...）。
func (m *Module) Register(mux *http.ServeMux, _ *core.Context) error {
	if m.store == nil {
		return fmt.Errorf("switch 模块未完成初始化（Migrate 未执行）")
	}
	mux.HandleFunc("/api/v2/switch/ping", func(w http.ResponseWriter, _ *http.Request) {
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", map[string]any{
			"module":  ModuleID,
			"version": ModuleVersion,
			"stage":   "M1：设备台账与基础资料已迁入；端口/VLAN/终端/采集等待迁入",
		})
	})
	// 新壳可直接读取台账（与老接口 /itms/handleSwitchInfo/?nAct=0 语义相同）。
	mux.HandleFunc("/api/v2/switch/ledger", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			core.WriteMethodNotAllowed(w)
			return
		}
		devices, err := m.store.GetAllDeviceInformation()
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "读取设备台账失败: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", devices)
	})
	return nil
}

// Migrate 打开本模块独占的数据库并建表（一模块一库：默认 <DataDir>/switch.db）。
//
// 升级注意（见 docs/06）：老库 /home/switch_rpc_api.db 里的 5 张基础资料表与设备台账表
// 需要由迁移工具（M2）搬入本模块库；未迁移时台账为空，但注册与其它模块不受影响。
func (m *Module) Migrate(ctx *core.Context) error {
	m.ctx = ctx
	if ctx == nil {
		return fmt.Errorf("缺少运行上下文")
	}
	dbPath := strings.TrimSpace(os.Getenv(envDBPath))
	if dbPath == "" {
		dbPath = filepath.Join(ctx.DataDir, "switch.db")
	}
	db, err := core.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	m.db = db
	m.cipher = NewCredentialCipher(ctx.CredentialKeyPath, strings.TrimSpace(os.Getenv(envLoginFilePath)))
	m.store = NewStore(db, m.cipher)
	// 设备采集缓存 / 采集导入 / 嗅探配置目录：默认落在模块数据目录下（老实现共用 /home/switchfile）。
	m.deviceFileDir = envOrDefault(envDeviceFileDir, filepath.Join(ctx.DataDir, "devicefile"))
	m.collectionDir = envOrDefault(envCollectionDir, filepath.Join(ctx.DataDir, "collectionfile"))
	m.snifferDir = envOrDefault(envSnifferDir, ctx.DataDir)
	m.snifferConfigPath = filepath.Join(m.snifferDir, "sniffinginfocfg.json")
	m.store.legacyImportDir = m.collectionDir
	m.store.deviceFileDir = m.deviceFileDir
	m.snifferManager = m.newSnifferManager()
	m.collectionSettingsPath = filepath.Join(m.snifferDir, "collectionsettings.json")
	m.collectionManager = m.newCollectionManager()
	if err := m.store.EnsureTables(); err != nil {
		return err
	}
	if err := m.store.ensureSwitchCollectionTables(); err != nil {
		return err
	}
	// 后台任务：按已保存的配置恢复嗅探/采集定时任务（老实现 initSwitchSniffer/initSwitchCollection）。
	m.initSwitchSniffer()
	m.initSwitchCollection()
	ctx.Logger.Printf("switch 模块库就绪：%s（基础资料 5 张表 + 设备台账 + 采集快照 2 张表）", dbPath)
	return nil
}

// envOrDefault 读取环境变量（为空时用默认值）。
func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// Close 关闭模块库（进程退出时由宿主调用）。
func (m *Module) Close() error {
	if m.db == nil {
		return nil
	}
	return m.db.Close()
}

// RotateCredentials 实现 core.CredentialRotator：平台层 AES 密钥变化时，
// 把设备台账（tbDeviceInformation）与登录记录文件里的账号密码改用新密钥重新加密。
//
// 顺序：先改库（同一事务内），再改登录记录文件；任何一步失败都回滚，
// 平台层据此放弃本次注册保存（不会出现“密钥文件已换、凭据还是旧密钥”的不可解密状态）。
func (m *Module) RotateCredentials(oldKeyText, newKeyText string) error {
	if m.store == nil {
		return fmt.Errorf("switch 模块未完成初始化")
	}
	oldKey := strings.TrimSpace(oldKeyText)
	newKey := strings.TrimSpace(newKeyText)
	if newKey == "" {
		return fmt.Errorf("新 AES 密钥不能为空")
	}
	if oldKey == "" || oldKey == newKey {
		// 首次注册（还没有旧密钥）或密钥未变化：无需轮换。
		return nil
	}
	if err := m.store.rotateDeviceCredentials(oldKey, newKey); err != nil {
		return err
	}
	backup, err := m.cipher.rotateLoginFile(oldKey, newKey)
	if err != nil {
		if restoreErr := backup.restore(); restoreErr != nil {
			return fmt.Errorf("%v；恢复历史登录信息失败: %w", err, restoreErr)
		}
		return err
	}
	return nil
}
