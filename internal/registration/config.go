// Package registration 实现平台级的注册 / 授权业务流。
//
// 归属（见 docs/03-data-authority.md 第 3 节结论 4）：注册与授权属于**平台层**，
// 随任意交付形态（仅 switch / 仅 olt+tr069 / all）一起安装，不随业务模块卸载；
// 因此本包不 import 任何业务模块，模块需要用新密钥重加密自己的凭据时，通过 CredentialHook 回调。
//
// 与老实现的对应关系（契约逐字段保持一致，便于黄金用例接管）：
//
//	switchapi/validation_handler.go   → service.go / handler.go / store.go
//	switchapi/registration_apikey.go  → apikey.go      （TXR1 注册码编解码，与 /txapikeygen 互通）
//	switchapi/registration_license.go → license.go     （签名授权状态文件：到期时间与时间锚点）
//	switchapi/registration_guard.go   → guard.go       （未注册 / 已过期时拦截业务接口）
//	switchapi/validation_table.go     → store.go       （tbValidation 建表与迁移）
//
// 对外契约（不得改名 / 改类型）：
//
//	POST /itms/handleRegistrationInfo/   nAct=0 读取注册信息；nAct=1 保存注册信息（saveData 为 JSON）
//	POST /itms/validateThirdToken/       登录令牌校验（P1，属登录链路）
//	POST /api/v2/platform/registration/  新命名空间，语义同上（新前端壳使用）
//	返回体 {code,message,data}；未注册 -3、已过期 -2、参数/业务错误 -1、成功 0
package registration

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 默认路径：与老实现完全一致，保证同一台设备升级后注册状态、注册码、AES 密钥都不变。
const (
	defaultStatePath      = "/opt/itmsapikey/registration.state"
	defaultKeyPath        = "/opt/itmsapikey/itmsapikey.key"
	defaultSecretPath     = "/txapikeygen/.registration-secret"
	defaultAESKeyPath     = "/home/switchfile/aeskey.key"
	defaultVerifyInfoPath = "/dev/shm/verify_info.json"

	// EnvRegistrationDir 是“把全部注册文件收敛到一个目录”的开关，仅用于本地自检 / 单元测试：
	// 设置后状态文件、注册码、AES 密钥、verify_info 都落在该目录，绝不触碰设备的真实注册数据。
	EnvRegistrationDir = "TXAPI_REGISTRATION_DIR"

	envStatePath      = "TXAPI_REGISTRATION_STATE"
	envKeyPath        = "TXAPI_REGISTRATION_KEY"
	envSecretPath     = "TXAPI_REGISTRATION_SECRET_FILE"
	envAESKeyPath     = "TXAPI_REGISTRATION_AESKEY"
	envVerifyInfoPath = "TXAPI_VERIFY_INFO"
)

// CredentialHook 在 AES 密钥发生变化、注册信息已提交后由平台层回调，
// 用于让各业务模块用新密钥重新加密自己保存的凭据（见 core.CredentialRotator）。
type CredentialHook func(oldKeyText, newKeyText string) error

// Config 是注册服务的路径与依赖配置。
type Config struct {
	// Dir 非空时（EnvRegistrationDir）覆盖下面全部路径，用于自检 / 测试的隔离环境。
	Dir string
	// StatePath 是签名授权状态文件（到期时间与时间锚点的权威来源）。
	StatePath string
	// KeyPath 是本机注册码文件（itmsapikey.key）。
	KeyPath string
	// SecretPath 是主注册服务（/txapikeygen）共享密钥文件。
	SecretPath string
	// AESKeyPath 是注册信息里 AES 密钥的落盘位置（供业务模块加密设备凭据使用）。
	AESKeyPath string
	// VerifyInfoPath 是云平台授权窗口文件（/dev/shm/verify_info.json），不存在表示无云授权。
	VerifyInfoPath string
	// Now 返回当前时间，测试可替换。
	Now func() time.Time
	// Logger 用于服务端日志；为空时使用标准 logger。
	Logger *log.Logger
	// CredentialHook 见 CredentialHook 说明，可为空。
	CredentialHook CredentialHook
}

// DefaultConfig 按环境变量构造配置：未设置时与老实现路径一致。
func DefaultConfig() Config {
	cfg := Config{
		Dir:            strings.TrimSpace(os.Getenv(EnvRegistrationDir)),
		StatePath:      strings.TrimSpace(os.Getenv(envStatePath)),
		KeyPath:        strings.TrimSpace(os.Getenv(envKeyPath)),
		SecretPath:     strings.TrimSpace(os.Getenv(envSecretPath)),
		AESKeyPath:     strings.TrimSpace(os.Getenv(envAESKeyPath)),
		VerifyInfoPath: strings.TrimSpace(os.Getenv(envVerifyInfoPath)),
		Now:            time.Now,
	}
	return cfg.withDefaults()
}

// withDefaults 补齐空值：显式单路径 > 注册目录 > 生产默认值。
func (c Config) withDefaults() Config {
	dir := strings.TrimSpace(c.Dir)
	if dir != "" {
		if c.StatePath == "" {
			c.StatePath = filepath.Join(dir, "registration.state")
		}
		if c.KeyPath == "" {
			c.KeyPath = filepath.Join(dir, "itmsapikey.key")
		}
		if c.SecretPath == "" {
			// 注意：主注册服务共享密钥文件不能与“设备本地历史密钥文件”
			// （fallbackSecretPath = 注册码文件同目录下的 .registration-secret）同路径，
			// 因此隔离目录下放到子目录，保持两者语义与生产环境一致。
			c.SecretPath = filepath.Join(dir, "txapikeygen", ".registration-secret")
		}
		if c.AESKeyPath == "" {
			c.AESKeyPath = filepath.Join(dir, "aeskey.key")
		}
		if c.VerifyInfoPath == "" {
			c.VerifyInfoPath = filepath.Join(dir, "verify_info.json")
		}
	}
	if c.StatePath == "" {
		c.StatePath = defaultStatePath
	}
	if c.KeyPath == "" {
		c.KeyPath = defaultKeyPath
	}
	if c.SecretPath == "" {
		c.SecretPath = defaultSecretPath
	}
	if c.AESKeyPath == "" {
		c.AESKeyPath = defaultAESKeyPath
	}
	if c.VerifyInfoPath == "" {
		c.VerifyInfoPath = defaultVerifyInfoPath
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = log.Default()
	}
	return c
}

// Service 是注册 / 授权业务流的唯一入口。
type Service struct {
	cfg    Config
	db     *sql.DB
	logger *log.Logger
	// saveMu 串行化“读-改-写注册信息 + 轮换 AES 密钥”的临界区
	// （老实现用 switchLoginFileMu 保护，新实现用独立互斥量，不再与交换机登录文件共用一把锁）。
	saveMu sync.Mutex
}

// New 创建注册服务；db 为平台库（tbValidation 表所在库），由调用方用 core.OpenSQLite 打开。
func New(cfg Config, db *sql.DB) *Service {
	cfg = cfg.withDefaults()
	return &Service{cfg: cfg, db: db, logger: cfg.Logger}
}

// Config 返回生效配置（测试与文档用）。
func (s *Service) Config() Config { return s.cfg }

// now 返回配置的时间源，保证测试可控。
func (s *Service) now() time.Time { return s.cfg.Now() }
