// Package olt 是 OLT 功能模块（PON/ONT 管理）。
//
// 迁移进度（M2，详见 docs/09-olt-m2.md §5）：
//
//	第一批（已迁入）：/itms/handleOltLogList/（纯模块库）
//	              /itms/handleConnectOltUser/（Act=0 查询 / 1 新增 / 2 修改 / 3 删除）
//	              /itms/handleOltConnect/（Act=0 读缓存 / Act=1 采集，兼作"连接"）
//	              /itms/handleBoardAndSlotInfo/（全量或单槽只读采集）
//	              /itms/handleCurrentConfiguration/（Act=0 读 / Act=1 采集）
//	              /itms/handleONTListAndStatus/（Act=0 读槽位缓存）
//	              /itms/handleOltTerminalUser/（Act=0 读用户名 / 1 新增 / 2 修改口令 / 3 删除）
//	              /itms/handleVLANFunctionModule/（Act=0 读 / 1 新增 / 2 删除）
//	第二批（已迁入）：/itms/handleDBAProfile/（Act=0 读 / 1 新增 / 2 删除）
//	              /itms/handleLineProfile/（Act=0 读 / 1 新增 / 2 删除）
//	第三批（已迁入）：/itms/handleSrvProfile/（ONT 业务模板，Act=0 读 / 1 新增 / 2 删除）
//	              /itms/handleTr069Profile/（TR069 管理模板，Act=0 读 / 1 新增 / 2 删除）
//	待迁入：       handleLinkAggregationInfo / handleOltSavedConfig / handleOltConfigAutoSave /
//	              handleONTRegisterProfile / handleONTRegistration / handleONTAutoMatic /
//	              handleONTAutoFind
//	              （设备写操作与后台任务，以及 handleONTListAndStatus 的 Act=9 采集/明细补全）
//
// 迁移原则（见 docs/09-olt-m2.md）：
//  1. 无包级全局：DB/Store/日志/会话锁由 Module 字段持有（老实现的 OltFilePath、GetDB()、
//     lockOLTSession、oltCurrentConfigRefreshInFlight 等一律改为 Module 字段）；
//  2. 一模块一库：本模块独占 <DataDir>/olt.db（老数据由 tools/migrate_olt_data.py 从 itms.db 搬入）；
//     文件类缓存写在 <DataDir>/olt/<OLTIP>/，老目录 /home/olt 只做只读兜底（见 files.go）；
//  3. 契约逐字保持：路径、`Act` 语义（注意不是 switch 的 nAct）、字段名（nID/szOltName…）、
//     返回体（含顶层 count/nPage/nLimit）都不变；老实现的怪口径（如 nLimit=Page*Limit、
//     Message="sueccess" 笔误、PonType 取最后端口类型）同样保持；
//  4. 老库口径兼容：口令列可能同时存在**密文**（新写入）与**明文**（老库存量），
//     读取一律「密文优先、明文兜底」（与 tools/migrate_olt_data.py 的 plain_value 同一口径）；
//  5. 设备通道：Telnet 与 SSH 都支持（internal/oltconn），只读命令先行，写操作按批迁入；
//  6. 已知 BUG 必须修：老实现里 mergeCachedOltBoardDetail 的位置导致 PON 口重复、
//     查询错误被静默吞掉等，已在搬运时修掉并写成回归断言（见 docs/09 §5.2）。
package olt

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
	// ModuleID 与 deploy/modules/olt.manifest 的模块 id 一致。
	ModuleID = "olt"
	// ModuleVersion 为模块自身版本。
	ModuleVersion = "0.1.0-m2"
	// envDBPath 可覆盖模块库位置（默认 <TXAPI_DATA_DIR>/olt.db）。
	envDBPath = "TXAPI_OLT_DB"
)

// Module 实现 core.Module（并实现 core.LegacyRouteProvider 提供已完成迁移的老接口）。
type Module struct {
	ctx   *core.Context
	db    *sql.DB
	store *Store
	// sessionLocks 用读写锁协调同一 (IP/Port/Username) 的设备会话：
	// 写锁用于独占会话（保存/写命令，同一时刻只允许一个），读锁用于只读并行下钻
	// （方案 B：多块板同时开只读会话采集命令，互不干扰但阻塞写会话）。
	sessionLocks struct {
		sync.Mutex
		locks map[string]*sync.RWMutex
	}
	// configRefresh 保证同一台 OLT 同时只有一个"当前配置"后台刷新（老实现 oltCurrentConfigRefreshInFlight）。
	configRefresh struct {
		sync.Mutex
		inFlight map[string]bool
	}
}

// New 返回 olt 模块实例。
func New() *Module {
	module := &Module{}
	module.sessionLocks.locks = make(map[string]*sync.RWMutex)
	module.configRefresh.inFlight = make(map[string]bool)
	return module
}

// ID 返回模块标识。
func (m *Module) ID() string { return ModuleID }

// Version 返回模块版本。
func (m *Module) Version() string { return ModuleVersion }

// DependsOn 声明依赖：OLT 模块可独立安装。
func (m *Module) DependsOn() []string { return nil }

// Nav 返回本模块在一级导航中的标题与排序（口径与 olt.manifest 的 module.nav_title 一致）。
func (m *Module) Nav() core.Nav {
	return core.Nav{Title: "OLT/ONU 管理", Order: 10}
}

// Menus 声明模块在一级导航下的 TAB。
func (m *Module) Menus() []core.Menu {
	return []core.Menu{
		{ID: "olt.overview", Title: "OLT 总览", Path: "/olt/overview", Order: 10},
		{ID: "olt.ont", Title: "ONT 管理", Path: "/olt/ont", Order: 20},
		{ID: "olt.profile", Title: "线路/业务模板", Path: "/olt/profile", Order: 30},
	}
}

// LegacyRoutes 返回由 olt 模块负责的老接口（字面量来自 tests/golden/routes.json）。
func (m *Module) LegacyRoutes() []string {
	return []string{
		"/itms/handleBoardAndSlotInfo/",
		"/itms/handleConnectOltUser/",
		"/itms/handleCurrentConfiguration/",
		"/itms/handleDBAProfile/",
		"/itms/handleLineProfile/",
		"/itms/handleLinkAggregationInfo/",
		"/itms/handleONTAutoFind/",
		"/itms/handleONTAutoMatic/",
		"/itms/handleONTListAndStatus/",
		"/itms/handleONTRegisterProfile/",
		"/itms/handleONTRegistration/",
		"/itms/handleOltConfigAutoSave/",
		"/itms/handleOltConnect/",
		"/itms/handleOltLogList/",
		"/itms/handleOltSavedConfig/",
		"/itms/handleOltTerminalUser/",
		"/itms/handleSrvProfile/",
		// handleVLANFunctionModule 原先在清单里未归类；依据老单体定义位置
		// handleTr069Profile 原先在清单里归 tr069（路径带 TR069 关键词）；依据老单体定义位置
		// （/switchProject/switchmain/olthandler.go 2960 行起「TR069管理模板」）与运行时上下文
		// （操作 OLT 设备的 `ont tr069-server-profile` 命令、解析 OLT 的 [global-config]）归 olt，
		// 与 ONT 业务模板同属 OLT 详情页「业务/TR069模板」TAB，见 docs/generated/olt-contract.md 第 2 节。
		"/itms/handleTr069Profile/",
		// （/switchProject/switchmain/olthandler.go）归 olt，见 docs/generated/olt-contract.md 第 2 节。
		"/itms/handleVLANFunctionModule/",
	}
}

// LegacyHandlers 返回**已完成迁移**的老接口（兼容层据此挂真实处理器；未列出的仍返回「过渡期」提示）。
//
// 第一批（2026-09-22）：
//
//	handleOltLogList         纯模块库（操作日志，已上线）
//	handleConnectOltUser     Act=0 查询 / 1 新增 / 2 修改 / 3 删除（全部四个 Act）
//	handleOltConnect         Act=0 读缓存 / Act=1 采集板卡与版本（兼作"连接"）
//	handleBoardAndSlotInfo   全量/单槽只读采集
//	handleCurrentConfiguration Act=0 读已保存配置 / Act=1 连设备刷新
//	handleONTListAndStatus   Act=0 读槽位 ONT 缓存
//	handleOltTerminalUser    Act=0 读登录用户名 / 1 新增 / 2 修改口令 / 3 删除（配置信息栏目）
//	handleVLANFunctionModule Act=0 读 VLAN 四类信息 / 1 新增 / 2 删除（顶层 vlanifdata/vlandata/...）
//	handleDBAProfile        Act=0 读已保存配置的 DBA 模板 / 1 新增 / 2 删除（DBA 模板，第二批）
//	handleLineProfile       Act=0 读已保存配置的 ONT 线路模板 / 1 新增 / 2 删除（线路模板，第二批）
//	handleSrvProfile        Act=0 读已保存配置的 ONT 业务模板 / 1 新增 / 2 删除（业务模板，第三批）
//	handleTr069Profile      Act=0 读已保存配置的 TR069 管理模板 / 1 新增 / 2 删除（TR069 模板，第三批）
func (m *Module) LegacyHandlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/itms/handleOltLogList/":           m.handleOltLogList,
		"/itms/handleConnectOltUser/":       m.handleConnectOltUser,
		"/itms/handleOltConnect/":           m.handleOltConnect,
		"/itms/handleBoardAndSlotInfo/":     m.handleBoardAndSlotInfo,
		"/itms/handleCurrentConfiguration/": m.handleCurrentConfiguration,
		"/itms/handleONTListAndStatus/":     m.handleONTListAndStatus,
		"/itms/handleOltTerminalUser/":      m.handleOltTerminalUser,
		"/itms/handleVLANFunctionModule/":   m.handleVLANFunctionModule,
		"/itms/handleDBAProfile/":           m.handleDBAProfile,
		"/itms/handleLineProfile/":          m.handleLineProfile,
		"/itms/handleSrvProfile/":           m.handleSrvProfile,
		"/itms/handleTr069Profile/":         m.handleTr069Profile,
	}
}

// Register 注册本模块新命名空间路由。
func (m *Module) Register(mux *http.ServeMux, _ *core.Context) error {
	if m.store == nil {
		return fmt.Errorf("olt 模块未完成初始化（Migrate 未执行）")
	}
	mux.HandleFunc("/api/v2/olt/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"module":"olt","version":"` +
			ModuleVersion + `","stage":"M2 第一批：handleOltLogList 已迁入（其余按 docs/09 分批）"}}`))
	})
	return nil
}

// Migrate 打开模块库并建表（一模块一库：默认 <TXAPI_DATA_DIR>/olt.db）。
func (m *Module) Migrate(ctx *core.Context) error {
	if ctx == nil {
		return fmt.Errorf("缺少运行上下文")
	}
	m.ctx = ctx
	dbPath := strings.TrimSpace(os.Getenv(envDBPath))
	if dbPath == "" {
		dbPath = filepath.Join(ctx.DataDir, "olt.db")
	}
	db, err := core.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	m.db = db
	m.store = NewStore(db)
	if err := m.store.EnsureTables(); err != nil {
		return err
	}
	// 文件类缓存目录（currentconfiguration.json / savedconfiguration.txt / slot_ont_file）：
	// 只创建本模块自己的目录；老目录 /home/olt 保持只读（见 files.go 的落地口径）。
	if err := os.MkdirAll(m.fileDir(), 0755); err != nil {
		return fmt.Errorf("创建 OLT 文件目录 %s 失败: %w", m.fileDir(), err)
	}
	// 文件→表：缓存表为空时，把老目录里的文件缓存导入一次（老目录只读、幂等，见 filecache.go）。
	// 导入失败不影响启动（表为空时接口返回空数据，后续可重试）。
	if imported, importErr := m.importFileCacheIfEmpty(); importErr != nil {
		ctx.Logger.Printf("olt 文件缓存导入失败（不影响启动，可重试）：%v", importErr)
	} else if imported.Devices > 0 || imported.OntItems > 0 || imported.ConfigTexts > 0 || imported.AutoRegConfs > 0 {
		ctx.Logger.Printf("olt 文件缓存已导入表：设备快照 %d、板卡 %d、PON 口 %d、GE 口 %d、ONT %d、配置文本 %d、自动注册 %d（跳过 %d）",
			imported.Devices, imported.Boards, imported.PonPorts, imported.GePorts,
			imported.OntItems, imported.ConfigTexts, imported.AutoRegConfs, imported.Skipped)
	}
	ctx.Logger.Printf("olt 模块库就绪：%s（OLT 账号/注册模板/板卡/ONT/操作日志 + 版本表）；文件缓存目录：%s",
		dbPath, m.fileDir())
	return nil
}

// Close 关闭模块库（main 退出时按 interface{ Close() error } 调用）。
func (m *Module) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}
