# itmsapiproject 常用命令
# 说明：
#   - 默认端口 18092，刻意避开线上 8092（未经授权不得操作原系统）；
#   - 本地运行/自检统一使用隔离目录 DEV_DIR（TXAPI_DATA_DIR + TXAPI_REGISTRATION_DIR），
#     绝不读写设备真实注册数据（/opt/itmsapikey、/dev/shm/verify_info.json、/home/switchfile）。
GO       ?= go
PORT     ?= 18092
WEB_PORT ?= 18094
MODULES  ?= switch,olt,tr069
DEV_DIR  ?= /tmp/txapi-dev
# 自检端口与 make run/演示错开：自检只杀自己启动的进程，可与演示并存
SELFCHECK_PORT ?= 18093
# 前端渲染冒烟的目标服务（默认指向前端壳 18094；也可直接给 18092 骨架）
WEB_SMOKE_BASE ?= http://127.0.0.1:$(WEB_PORT)

.PHONY: help build vet test run web-run web-smoke web-cred olt-ui olt-dom switch-audit migrate-switch-data migrate-switch-check migrate-olt-data migrate-olt-check migrate-olt-apply migrate-olt-replace olt-contract olt-contract-check demo-seed demo-status inventory check-inventory golden-init golden selfcheck selfcheck-switch clean

help: ## 列出所有目标
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## 编译骨架服务到 dist/txapi
	$(GO) build -buildvcs=false -o dist/txapi ./cmd/txapi

vet: ## 静态检查 + 格式化
	$(GO) vet ./...
	gofmt -w $$(find . -name '*.go' -not -path './dist/*')

test: ## 运行 Go 单元测试（含注册/授权链路回归）
	$(GO) test ./...

run: build ## 本地运行（隔离目录；默认 18092，可用 PORT/MODULES 覆盖）
	@mkdir -p $(DEV_DIR)/data $(DEV_DIR)/reg
	TXAPI_PORT=$(PORT) TXAPI_MODULES=$(MODULES) \
	TXAPI_DATA_DIR=$(DEV_DIR)/data TXAPI_REGISTRATION_DIR=$(DEV_DIR)/reg \
	./dist/txapi

web-run: ## 启动界面（默认 18094，代理 /itms、/api、/client 到 TXAPI_TARGET/18092；凭据密钥用 DEV_DIR 隔离目录）
	cd web && PORT=$(WEB_PORT) TXAPI_TARGET=http://127.0.0.1:$(PORT) SWITCH_LOGIN_KEY_FILE=$(DEV_DIR)/reg/aeskey.key node server.cjs

web-smoke: ## 界面冒烟（最小 DOM 桩真实启动 createApp + 三处定制 + 基线未漂移）
	@TXAPI_BASE=$(WEB_SMOKE_BASE) node tools/web_smoke.mjs

web-cred: ## 界面与骨架的凭据链路端到端（加密端点 → 台账保存 → 回读解密 → 清理）
	@BASE=$(WEB_SMOKE_BASE) python3 tools/web_cred_e2e.py

olt-ui: ## OLT 第一批 × 前端壳 结合验证（DOM 桩真实启动 createApp + 穿壳调接口 + 自带假 OLT）
	@TXAPI_BASE=$(WEB_SMOKE_BASE) node tools/olt_ui_integration.mjs

olt-dom: ## OLT 台账页真实 DOM 校验（jsdom 真点击真冒泡；jsdom 未装则跳过，见 tools/domcheck）
	@cd tools/domcheck && TXAPI_BASE=$(WEB_SMOKE_BASE) node $(CURDIR)/tools/olt_ui_dom_check.mjs

switch-audit: build ## 交换机功能（含注册）迁入审计：老壳调用面逐 nAct 对账 + 注册链路 E2E
	@BASE=$(WEB_SMOKE_BASE) python3 tools/switch_migration_audit.py

migrate-switch-data: ## 交换机数据迁移（老库+设备缓存+配置 → 模块库/模块数据目录；老库只读）
	@python3 tools/migrate_switch_data.py --data-dir $(DEV_DIR)/data --replace --yes

migrate-switch-check: ## 凭据可解密性体检（哪些设备迁过来后需要重存密码）
	@python3 tools/migrate_switch_data.py --data-dir $(DEV_DIR)/data --skip-files --check-credentials --yes

migrate-olt-data: ## OLT 数据迁移 dry-run（老 itms.db → <DataDir>/olt.db；只读老库、不建库不写盘）
	@python3 tools/migrate_olt_data.py

migrate-olt-check: ## OLT 迁移两通道对账（sxzdbmid:8306 中间件 vs 直读 itms.db，行数 + 主键）
	@python3 tools/migrate_olt_data.py --cross-check

migrate-olt-apply: ## OLT 数据迁移执行（merge 幂等写入 <DataDir>/olt.db；老库只读，写前自动备份）
	@python3 tools/migrate_olt_data.py --apply

migrate-olt-replace: ## OLT 取精确副本（切流当天用：清空目标表后重写；需先停老进程/单写者窗口）
	@python3 tools/migrate_olt_data.py --apply --replace --yes

olt-contract: ## OLT 接口契约基线（参数/Act/连设备/含写；只读老工程生成 docs/generated/olt-contract.md）
	@python3 tools/olt_contract_scan.py

olt-contract-check: ## 校验 OLT 契约声明一致（routes.json / module.go / manifest）且生成物最新（CI 用）
	@python3 tools/olt_contract_scan.py --check

demo-seed: ## 灌入演示数据（自动注册 + 示例基础资料/设备；需先 make run）
	python3 tools/demo_seed.py --base-url http://127.0.0.1:$(PORT)

demo-status: ## 查看演示数据概览
	python3 tools/demo_seed.py --check --base-url http://127.0.0.1:$(PORT)

inventory: ## 重新生成兼容清单与文件扫描清单（只读原工程）
	python3 tools/api_inventory.py
	python3 tools/tr069_file_scan.py

check-inventory: ## 校验清单是否最新（CI 用）
	python3 tools/api_inventory.py --check

golden-init: ## 从兼容清单生成/刷新黄金用例骨架
	python3 tools/run_golden.py --from-inventory

golden: ## 回放黄金用例（默认自检用例，目标为本地骨架服务）
	python3 tools/run_golden.py --cases tests/golden/cases/selfcheck.json

selfcheck: build ## 自检（独立端口 18093 + 临时目录，不与 make run/演示抢端口）
	@SELFCHECK_PORT=$(SELFCHECK_PORT) TXAPI_MODULES=$(MODULES) tools/selfcheck.sh

selfcheck-switch: build ## switch 模块冒烟（独立端口 + 先注册，再验证 6 条真实接口与未迁入接口的过渡响应）
	@SELFCHECK_PORT=$(SELFCHECK_PORT) TXAPI_MODULES=$(MODULES) tools/selfcheck.sh switch

clean: ## 清理构建产物

	rm -rf dist
	find . -name '__pycache__' -type d -prune -exec rm -rf {} +
