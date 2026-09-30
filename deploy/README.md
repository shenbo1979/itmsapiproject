# 打包与安装清单（manifest）设计

三种交付形态（仅 switch / 仅 olt+tr069 / 全功能）**同一份源码、同一套脚本**，
差异只体现在 manifest 选中的文件集合与模块开关上。

## 1. manifest 格式

`deploy/modules/<module>.manifest`，纯 key=value，便于 shell 直接 `source` 或解析：

> **M1 新增**：`deploy/modules/platform.manifest` 描述**平台层**（注册 / 授权 / 能力清单 /
> 前端壳文件）。按 `docs/03` 结论 4，注册授权不随业务模块卸载，因此
> **三个交付形态（switch / olt / all）都必须包含 platform 清单**（`bundle=switch,olt,all`）。

| 键 | 含义 | 示例 |
|---|---|---|
| `module.id` | 模块标识（与代码、`/api/capabilities`、systemd 单元命名一致） | `switch` |
| `module.title` | 交付给界面/文档的中文名 | `交换机运维` |
| `module.version` | 模块版本（可独立升级） | `0.1.0-m0` |
| `module.depends` | 依赖模块（逗号分隔，安装时自动带装） | `olt` |
| `module.port` | 模块服务端口 | `8092` |
| `module.env` | 环境变量文件名（写入 `/etc/txconsole/`） | `switch.env` |
| `module.service` | systemd 单元名 | `txapi-switch.service` |
| `module.datadir` | 模块数据库（一模块一库） | `/home/tx/switch.db` |
| `module.files` | 该模块产出的文件（逗号分隔，相对包根） | `bin/txapi,web/src/modules/switch.js` |
| `module.legacy_routes` | 该模块负责的老接口前缀（兼容层校验用） | `/itms/handleSwitch,/itms/handleVlan` |
| `module.menus` | 前端菜单元数据（与代码 `Menus()` 对齐，便于离线渲染） | `switch.ledger,switch.panel` |
| `module.external` | 该模块依赖的外部服务（如 Python/socket） | `python:/itms-py, service:itmssocketmain` |
| `bundle` | 该 manifest 参与哪些包 | `switch,all` |

## 2. 三种包的生成方式（M3 实现）

```bash
./deploy/build.sh                      # 同一 commit，一次构建，产出：
#  dist/txconsole-switch-<ver>.tar.gz        只含 bundle 含 switch 的 manifest 所选文件
#  dist/txconsole-olt-tr069-<ver>.tar.gz
#  dist/txconsole-all-<ver>.tar.gz           = 全部模块文件（install.sh 内选装）
```

## 3. 安装/卸载（M3 实现）

```bash
sudo ./install.sh                        # all 包默认全装；单模块包只装自身
sudo ./install.sh --modules switch       # 从 all 包里选装
sudo ./install.sh --add-module olt       # 后期补装（复用 core、前端、跑增量迁移）
sudo ./install.sh --remove-module tr069  # 只清模块（保留数据；--purge-module 连数据）
sudo ./install.sh --list-modules         # 列出包内模块与当前启用状态
```

硬性约定：
1. **三形态同 commit 同版本构建**，禁止分叉代码；
2. 卸载以 manifest 的 `module.files/service/env` 为准，避免残留；
3. `--add-module` 必须能跑增量迁移（`migrate apply` 幂等）；
4. 模块未安装时，其老接口返回 `code=-4`（见 `docs/01-api-compat.md` 第 4 节）。
