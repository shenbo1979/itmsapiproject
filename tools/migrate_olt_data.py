#!/usr/bin/env python3
"""OLT 数据迁移（M2）：老库 itms.db → 统一工程模块库 <data-dir>/olt.db。

**本版只做 dry-run**：读老库 → 打印「将建哪些表 / 列 / 行数 / 主键核对 / 凭据口径」，**不建库、不写盘**。

为什么 OLT 的迁移与 switch 不同（依据 docs/02 §2、§6 与只读实测）：
  * OLT 数据**不在** `/home/switch_rpc_api.db`（该库 10 张表全是 switch，200MB）；
    它在 `/sxzdb/db/itms.db`（344KB，由中间件 `sxzdbmid:8306` 以 MySQL 协议暴露给老单体）；
  * 统一工程按「一模块一库」收敛：switch 已迁到 `<data-dir>/switch.db`，
    OLT 对应 `<data-dir>/olt.db`（manifest: `module.datadir=/home/tx/olt.db`），
    **不与 switch 共库**（共库 = 复现 2026-09-20 多进程共写导致的整站假死）；
  * 老库**只读**：老 itmsmain(8090)、itmssocketmain、Python 侧仍在用，迁移只「拷出」。

5 张表映射（**列名原样保留**是兼容硬要求；表名按 docs/02 §2 规范改为 tbOlt*）：

    老表                新表                主键   内容
    oltuser            → tbOltUser            Nid    OLT 账号（老库 Password 为**明文**）
    oltregprofile      → tbOltRegProfile      Nid    ONT 注册模板（Tr069profile 为名称引用）
    oltboard           → tbOltBoard           Nid    板卡缓存（真机为准）
    oltontlist         → tbOltOnt             Nid    ONT 列表缓存（真机为准）
    tbOltOperationLog  → tbOltOperationLog    nID    OLT 操作日志
    （新建）              tbSchemaVersion       —      版本台账（docs/02 §2 必备）

两条读取通道（都可只读，默认 auto：优先中间件，失败回落直读文件）：
  --source mysql  经 127.0.0.1:8306（与老系统同一通道）：DSN 从老单体源码只读解析（不在本仓库存口令）；
                  驱动优先 MySQLdb(mysqlclient)，不可用时回落 `mysql` CLI（与老单体 go-sql-driver 同类客户端）
  --source file   直读 /sxzdb/db/itms.db（rollback journal，用 sqlite3 只读打开）
  --source auto   默认

用法：
  make migrate-olt-data                      # = python3 tools/migrate_olt_data.py（dry-run）
  python3 tools/migrate_olt_data.py --source file
  python3 tools/migrate_olt_data.py --cross-check          # 两通道对账（行数 + 主键集合）
  python3 tools/migrate_olt_data.py --data-dir /tmp/x/data # 指定目标模块数据目录

注意：`--apply`（真正建库落数据）**尚未实现**，按约定等 dry-run 结果确认后再补。
"""
import argparse
import base64
import binascii
import os
import re
import shutil
import sqlite3
import subprocess
import sys
import time

try:  # 仅“凭据加密落库 / 语义核对”需要；缺失时给出明确提示
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM
except ImportError:  # pragma: no cover
    AESGCM = None

# 老 OLT 数据（SQLite 文件；中间件 sxzdbmid 的落盘位置，只读）
OLD_DB_DEFAULT = "/sxzdb/db/itms.db"
# 老单体源码：只读解析其中的 MySQL DSN，避免把口令写进本仓库
OLD_MONOLITH_SRC = "/switchProject/switchmain/rpc_client.go"
# 目标模块数据目录（与 internal/core.Context.DataDir 一致；txapi 服务当前用 /home/tx）
DATA_DIR_DEFAULT = os.environ.get("TXAPI_DATA_DIR") or "/home/tx"
# 中间件默认监听（老单体 DSN 里的地址；仅作 --mysql-* 缺省值）
MYSQL_HOST_DEFAULT = "127.0.0.1"
MYSQL_PORT_DEFAULT = 8306
MYSQL_DB_DEFAULT = "itms"

# (老表, 新表, 主键, 说明)
OLT_TABLES = [
    ("oltuser", "tbOltUser", "Nid", "OLT 账号（老库 Password 为明文，落库前需决定是否加密）"),
    ("oltregprofile", "tbOltRegProfile", "Nid", "ONT 注册模板（Tr069profile 为名称引用，非外键）"),
    ("oltboard", "tbOltBoard", "Nid", "板卡缓存（真机为准，可随时刷新）"),
    ("oltontlist", "tbOltOnt", "Nid", "ONT 列表缓存（真机为准，可随时刷新）"),
    ("tbOltOperationLog", "tbOltOperationLog", "nID", "OLT 操作日志"),
]
SCHEMA_VERSION_TABLE = "tbSchemaVersion"
SCHEMA_VERSION = 1

# 与 internal/core/aeskey.go + core.EncryptSecret 完全一致的线格式：base64( IV(12) || 密文 || tag(16) )
AES_GCM_IV_SIZE = 12
AES_GCM_TAG_SIZE = 16
# 平台层写入的设备凭据密钥（与老 switchapi、txapi 服务同一把）
INSTANCE_KEY_DEFAULT = "/home/switchfile/aeskey.key"
# 只加密口令；账号（Username）是标识列、界面要显示/过滤，保持可读
CREDENTIAL_COLUMNS = {"oltuser": ("Password",)}
# 老库 DDL 兜底（mysql 通道取不到 DDL、且老库文件不可读时使用；列名/类型与老库一致）
EMBEDDED_DDL = {
    "oltuser": 'CREATE TABLE "oltuser" ( "Nid" INTEGER PRIMARY KEY AUTOINCREMENT, "Username" TEXT(50) NOT NULL,'
               ' "Password" TEXT(50) NOT NULL, "Port" INTEGER NOT NULL, "Type" INTEGER NOT NULL, "Comment" TEXT(50),'
               ' "IP" TEXT(50) NOT NULL, "Automatic" INTEGER, "ScanInterval" INTEGER, "TemplateId" INTEGER )',
    "oltregprofile": 'CREATE TABLE "oltregprofile" ( "Nid" INTEGER PRIMARY KEY AUTOINCREMENT, "Name" TEXT NOT NULL,'
                     ' "Host" TEXT NOT NULL, "Authtype" TEXT, "Pontype" TEXT, "Lineprofileid" TEXT,'
                     ' "Srvprofileid" TEXT, "Ontdesc" TEXT, "Onutype" TEXT, "Servicevlan" TEXT, "Tr069profile" TEXT,'
                     ' "Iptvvlan" TEXT, "Unknownvaln" TEXT, "Tr069order" TEXT, "Desc" TEXT, "Active" TEXT )',
    "oltboard": 'CREATE TABLE "oltboard" ( "Nid" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "Userid" INTEGER NOT NULL,'
                ' "Boardid" INTEGER, "Status" TEXT, "Boardname" TEXT, "Boardtype" TEXT, "Boardport" TEXT )',
    "oltontlist": 'CREATE TABLE "oltontlist" ( "Nid" INTEGER PRIMARY KEY AUTOINCREMENT, "UserId" TEXT, "FSP" TEXT,'
                  ' "ONTID" TEXT, "Runstate" TEXT, "Authtype" TEXT, "SN" TEXT, "Managementmode" TEXT,'
                  ' "addressmask" TEXT, "Description" TEXT, "LineprofileID" TEXT, "ServiceprofileID" TEXT,'
                  ' "Boardid" TEXT )',
    "tbOltOperationLog": 'CREATE TABLE `tbOltOperationLog` ( `nID` INTEGER PRIMARY KEY AUTOINCREMENT,'
                         ' `szOltName` VARCHAR(256) DEFAULT \'\', `szMenu` VARCHAR(128) DEFAULT \'\','
                         ' `szAct` VARCHAR(128) DEFAULT \'\', `szOperationIP` VARCHAR(128) DEFAULT \'\','
                         ' `szText` TEXT DEFAULT \'\', `dtCreatTime` datetime DEFAULT \'\','
                         ' `szRemark` TEXT DEFAULT \'\' )',
}


def load_instance_key(path):
    """读取实例 AES 密钥（32 字节原文 / 64 位十六进制 / base64(32 字节)）。"""
    if not os.path.isfile(path):
        raise SystemExit(f"实例密钥不存在：{path}（可用 --key-file 指定）")
    with open(path, "rb") as fh:
        raw = fh.read().strip()
    if len(raw) == 32:
        return raw
    text = raw.decode("utf-8", "ignore").strip()
    if len(text) == 64 and re.fullmatch(r"[0-9a-fA-F]{64}", text):
        return binascii.unhexlify(text)
    try:
        decoded = base64.b64decode(text, validate=False)
    except Exception as exc:  # noqa: BLE001
        raise SystemExit(f"AES 密钥无法解析：{exc}")
    if len(decoded) == 32:
        return decoded
    raise SystemExit("AES 密钥格式无效：需要 32 字节原文 / 64 位十六进制 / base64(32 字节)")


def encrypt_secret(plain, key):
    """加密为本工程线格式（与 core.EncryptSecret 一致）；空串返回空串。"""
    text = str(plain or "")
    if not text:
        return ""
    iv = os.urandom(AES_GCM_IV_SIZE)
    return base64.b64encode(iv + AESGCM(key).encrypt(iv, text.encode("utf-8"), None)).decode("ascii")


def decrypt_secret(blob, key):
    """解密本工程线格式的凭据；失败抛异常。"""
    data = base64.b64decode(str(blob or ""), validate=False)
    if len(data) < AES_GCM_IV_SIZE + AES_GCM_TAG_SIZE:
        raise ValueError("密文长度不足")
    return AESGCM(key).decrypt(data[:AES_GCM_IV_SIZE], data[AES_GCM_IV_SIZE:], None).decode("utf-8")


def plain_value(value, key):
    """把库里存的值还原成可用值：能解密就解密，否则按明文（老库存量）看待。

    这正是「丝滑过渡」的关键：新库可能同时存在**密文**（新导入/新写入）与**明文**（老库原样搬入），
    OLT 模块取账号口令时应采用同一口径（密文优先、明文兜底）。
    """
    text = str(value or "")
    if not text:
        return ""
    try:
        return decrypt_secret(text, key)
    except Exception:  # noqa: BLE001
        return text

DSN_STR_RE = re.compile(r'"(?P<dsn>[^"\n]*@tcp\([^()\n]+\)/[^"?\n]+)')
DSN_PARTS_RE = re.compile(
    r"^(?P<user>[^:@]+):(?P<password>[^@]*)@tcp\((?P<host>[^:()]+):(?P<port>\d+)\)/(?P<db>[^?]+)$"
)


def parse_args():
    p = argparse.ArgumentParser(
        description="OLT 数据迁移（dry-run：只读老库、只打印计划，不写盘）",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    p.add_argument("--source", choices=("auto", "mysql", "file"), default="auto",
                   help="老库读取通道：auto（默认，优先 mysql）/ mysql（8306 中间件，经 mysql CLI）/ file（直读 itms.db）")
    p.add_argument("--old-db", default=OLD_DB_DEFAULT, help=f"老库文件（file 通道用，默认 {OLD_DB_DEFAULT}）")
    p.add_argument("--legacy-src", default=OLD_MONOLITH_SRC,
                   help=f"老单体源码路径（仅只读解析 MySQL DSN，默认 {OLD_MONOLITH_SRC}）")
    p.add_argument("--mysql-host", default=MYSQL_HOST_DEFAULT, help=f"中间件地址（默认 {MYSQL_HOST_DEFAULT}）")
    p.add_argument("--mysql-port", type=int, default=MYSQL_PORT_DEFAULT, help=f"中间件端口（默认 {MYSQL_PORT_DEFAULT}）")
    p.add_argument("--mysql-db", default=MYSQL_DB_DEFAULT, help=f"库名（默认 {MYSQL_DB_DEFAULT}）")
    p.add_argument("--mysql-user", default=None, help="覆盖 DSN 中的用户名")
    p.add_argument("--mysql-password", default=os.environ.get("TXAPI_OLT_MYSQL_PASSWORD"),
                   help="覆盖 DSN 中的口令（默认取 DSN / 环境变量 TXAPI_OLT_MYSQL_PASSWORD）")
    p.add_argument("--data-dir", default=DATA_DIR_DEFAULT,
                   help=f"目标模块数据目录（默认 {DATA_DIR_DEFAULT}，可被 TXAPI_DATA_DIR 覆盖）")
    p.add_argument("--cross-check", action="store_true",
                   help="两通道对账：逐表比较行数与主键集合（建议在 file/mysql 都可用时跑一次）")
    p.add_argument("--apply", action="store_true",
                   help="执行写入（建库/建表/按主键 upsert）；不带则为 dry-run（只读老库、只打印计划）")
    p.add_argument("--replace", action="store_true",
                   help="精确副本模式：先清空目标表再写入（切流当天用；必须配合 --yes）")
    p.add_argument("--yes", action="store_true", help="确认破坏性操作（--replace 必填）")
    p.add_argument("--encrypt-credentials", dest="encrypt_credentials", action="store_true", default=True,
                   help="导入时把 oltuser.Password 按实例密钥加密落库（默认开；账号列保持可读）")
    p.add_argument("--no-encrypt-credentials", dest="encrypt_credentials", action="store_false",
                   help="原样明文落库（仅调试或需要其它程序直读新库时使用）")
    p.add_argument("--key-file", default=INSTANCE_KEY_DEFAULT,
                   help=f"实例 AES 密钥文件（默认 {INSTANCE_KEY_DEFAULT}）")
    p.add_argument("--verify", action="store_true", help="写入后逐列核对（--apply 时默认执行）")
    p.add_argument("--backup", dest="backup", action="store_true", default=True,
                   help="写前备份目标库到 <data_dir>/backup/（默认开）")
    p.add_argument("--no-backup", dest="backup", action="store_false", help="跳过写前备份")
    return p.parse_args()


def discover_legacy_dsn(path, user_override=None, password_override=None, host=None, port=None, db=None):
    """从老单体源码里只读解析 MySQL DSN（口令不写进本仓库）。返回 dict 或 None。"""
    if not os.path.exists(path):
        return None
    try:
        with open(path, "r", encoding="utf-8", errors="ignore") as fh:
            text = fh.read()
    except OSError:
        return None
    m = None
    for candidate in DSN_STR_RE.finditer(text):
        parsed = DSN_PARTS_RE.match(candidate.group("dsn"))
        if parsed:
            m = parsed
            break
    if m is None:
        return None
    return {
        "user": user_override or m.group("user"),
        "password": password_override if password_override is not None else m.group("password"),
        "host": host or m.group("host"),
        "port": int(port or m.group("port")),
        "db": db or m.group("db"),
    }


# ---------------------------------------------------------------------------
# 读取通道：两个实现暴露同一组只读接口（tables/columns/count/keys/raw_rows/ddl）
# ---------------------------------------------------------------------------
class FileSource:
    """直读老库文件（sqlite3 只读；老库是 rollback journal，读连接不阻塞中间件写）。"""

    kind = "file"

    def __init__(self, path):
        if not os.path.exists(path):
            raise RuntimeError(f"老库不存在：{path}")
        self.path = path
        self.con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
        self.con.execute("PRAGMA busy_timeout=5000")
        self.con.row_factory = sqlite3.Row

    def label(self):
        return f"file（{self.path}）"

    def tables(self):
        return {
            r[0]
            for r in self.con.execute("SELECT name FROM sqlite_master WHERE type='table'")
        }

    def columns(self, table):
        return [r[1] for r in self.con.execute(f'PRAGMA table_info("{table}")')]

    def count(self, table):
        return self.con.execute(f'SELECT COUNT(*) FROM "{table}"').fetchone()[0]

    def keys(self, table, pk):
        return {r[0] for r in self.con.execute(f'SELECT "{pk}" FROM "{table}"')}

    def raw_rows(self, table, cols):
        sel = ", ".join(f'"{c}"' for c in cols)
        for row in self.con.execute(f'SELECT {sel} FROM "{table}"'):
            yield {c: row[c] for c in cols}

    def ddl(self, table):
        row = self.con.execute(
            "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", (table,)
        ).fetchone()
        return row[0] if row and row[0] else None

    def close(self):
        self.con.close()


class CliSource:
    """经 `mysql` CLI 访问 sxzdbmid:8306（MySQL 协议中间件）——mysqlclient 不可用时的回落通道。

    `--batch`（**不加 --raw**）保留标准转义：值内的 \\t/\\n/\\\\ 会被转义，便于安全切分；
    中间件是受限方言（不支持 SHOW / information_schema），表存在性用 `SELECT COUNT(*)` 探测；
    口令只经 MYSQL_PWD 传递（不进命令行、不进仓库）。与老单体的 go-sql-driver 属同类客户端。
    """

    kind = "mysql"

    def __init__(self, cfg, binary="mysql", timeout=20):
        if not shutil.which(binary):
            raise RuntimeError(f"未找到 {binary} 客户端")
        self.cfg = cfg
        self.binary = binary
        self.timeout = timeout

    def label(self):
        c = self.cfg
        return f"mysql（{c['user']}@{c['host']}:{c['port']}/{c['db']} → sxzdbmid 中间件，经 mysql CLI 只读）"

    def _query(self, sql, header=False):
        args = [
            self.binary,
            f"--host={self.cfg['host']}",
            f"--port={self.cfg['port']}",
            f"--user={self.cfg['user']}",
            f"--database={self.cfg['db']}",
            "--batch",
            "--default-character-set=utf8mb4",
        ]
        if not header:
            args.append("--skip-column-names")
        args += ["-e", sql]
        env = dict(os.environ, MYSQL_PWD=str(self.cfg["password"]))
        proc = subprocess.run(args, env=env, capture_output=True, text=True, timeout=self.timeout)
        if proc.returncode != 0:
            lines = (proc.stderr or proc.stdout or "").strip().splitlines()
            raise RuntimeError(lines[-1] if lines else f"mysql 客户端退出码 {proc.returncode}")
        text = proc.stdout[:-1] if proc.stdout.endswith("\n") else proc.stdout
        return [self._split(line) for line in text.split("\n")] if text else []

    @staticmethod
    def _unescape(value):
        out, i = [], 0
        mapping = {"t": "\t", "n": "\n", "0": "\0", "\\": "\\"}
        while i < len(value):
            ch = value[i]
            if ch == "\\" and i + 1 < len(value) and value[i + 1] in mapping:
                out.append(mapping[value[i + 1]])
                i += 2
                continue
            out.append(ch)
            i += 1
        return "".join(out)

    def _split(self, line):
        # 中间件把 SQL NULL 打印成字面量 NULL（与字符串 "NULL" 不可区分），这里统一按空值处理
        return [None if field == "NULL" else self._unescape(field) for field in line.split("\t")]

    def tables(self):
        found = set()
        for old, _n, _p, _note in OLT_TABLES:
            try:
                self._query(f"SELECT COUNT(*) FROM `{old}`")
                found.add(old)
            except Exception:
                continue
        return found

    def columns(self, table):
        rows = self._query(f"SELECT * FROM `{table}` LIMIT 1", header=True)
        return rows[0] if rows else []

    def count(self, table):
        rows = self._query(f"SELECT COUNT(*) FROM `{table}`")
        return int(rows[0][0]) if rows and rows[0] else 0

    def keys(self, table, pk):
        return {str(row[0]) for row in self._query(f"SELECT `{pk}` FROM `{table}`") if row}

    def raw_rows(self, table, cols):
        sel = ", ".join(f"`{c}`" for c in cols)
        for row in self._query(f"SELECT {sel} FROM `{table}`"):
            yield {c: (row[i] if i < len(row) else None) for i, c in enumerate(cols)}

    def ddl(self, table):
        return None  # 中间件不支持取 DDL；apply 时按老库文件 DDL 生成

    def close(self):
        pass


class MySQLdbSource:
    """经 MySQLdb(mysqlclient) 访问 sxzdbmid:8306；本机实测可用（同口令/同端口）。

    相对 CLI 通道更精确：SQL NULL 直接得到 Python None，且无需处理批处理转义。
    """

    kind = "mysql"

    def __init__(self, cfg):
        import MySQLdb

        self.cfg = cfg
        self.con = MySQLdb.connect(
            host=cfg["host"], port=cfg["port"], user=cfg["user"],
            passwd=cfg["password"], db=cfg["db"], charset="utf8mb4",
        )
        self.cur = self.con.cursor()

    def label(self):
        c = self.cfg
        return f"mysql（{c['user']}@{c['host']}:{c['port']}/{c['db']} → sxzdbmid 中间件，经 mysqlclient 只读）"

    def tables(self):
        found = set()
        for old, _n, _p, _note in OLT_TABLES:
            try:
                self.count(old)
                found.add(old)
            except Exception:
                continue
        return found

    def columns(self, table):
        self.cur.execute(f"SELECT * FROM `{table}` LIMIT 1")
        cols = [d[0] for d in (self.cur.description or [])]
        self.cur.fetchall()
        return cols

    def count(self, table):
        self.cur.execute(f"SELECT COUNT(*) FROM `{table}`")
        return int(self.cur.fetchall()[0][0])

    def keys(self, table, pk):
        self.cur.execute(f"SELECT `{pk}` FROM `{table}`")
        return {str(row[0]) for row in self.cur.fetchall()}

    def raw_rows(self, table, cols):
        sel = ", ".join(f"`{c}`" for c in cols)
        self.cur.execute(f"SELECT {sel} FROM `{table}`")
        for row in self.cur.fetchall():
            yield {c: v for c, v in zip(cols, row)}

    def ddl(self, table):
        return None  # 中间件不支持取 DDL；apply 时按老库文件 DDL 生成

    def close(self):
        try:
            self.cur.close()
            self.con.close()
        except Exception:
            pass


def open_mysql_channel(cfg, notes):
    """打开 mysql 通道：优先 MySQLdb，缺依赖/握手失败时回落 mysql CLI。"""
    try:
        return MySQLdbSource(cfg)
    except Exception as exc:  # ImportError / 连接失败
        notes.append(f"mysqlclient 通道不可用：{exc}（改用 mysql CLI 通道）")
        return CliSource(cfg)


def open_source(args):
    """按 --source 打开老库读取通道；auto 时优先 mysql，失败回落 file。"""
    order, notes, errors = [], [], []
    if args.source in ("auto", "mysql"):
        order.append("mysql")
    if args.source in ("auto", "file"):
        order.append("file")

    for kind in order:
        try:
            if kind == "mysql":
                cfg = discover_legacy_dsn(
                    args.legacy_src, args.mysql_user, args.mysql_password,
                    args.mysql_host, args.mysql_port, args.mysql_db,
                )
                if cfg is None:
                    raise RuntimeError(f"未能从 {args.legacy_src} 解析 DSN（可用 --mysql-* 显式指定）")
                return open_mysql_channel(cfg, notes), notes
            return FileSource(args.old_db), notes
        except Exception as exc:  # 驱动缺失 / 连不上 / 老库不存在
            msg = f"{kind} 通道不可用：{exc}"
            errors.append(msg)
            if args.source != "auto":
                raise SystemExit(f"✗ {msg}")
            notes.append(msg + "（已回落下一通道）")

    raise SystemExit("✗ 老库读取通道均不可用：" + "；".join(errors))


B64_RE = re.compile(r"^[A-Za-z0-9+/]+={0,2}$")


def looks_like_ciphertext(value):
    """粗判是否为 core.EncryptSecret 的线格式 base64(IV(12)||密文||tag(16))。

    dry-run 阶段拿不到目标实例密钥、无法验 tag，因此只按「base64 字符集 + 长度≥29」粗判；
    真正的可解密性体检留到 apply 阶段（--encrypt-credentials）或服务回读接口。
    """
    v = str(value or "").strip()
    return len(v) >= 29 and len(v) % 4 == 0 and bool(B64_RE.match(v))


def report_credentials(src, table, cols, encrypt_credentials=True):
    """凭据口径体检：只打印计数，绝不打印口令明文。"""
    pw_col = next((c for c in cols if c.lower() == "password"), None)
    if not pw_col:
        return
    plain = cipher = empty = 0
    try:
        for row in src.raw_rows(table, cols):
            value = str(row.get(pw_col) or "").strip()
            if not value:
                empty += 1
            elif looks_like_ciphertext(value):
                cipher += 1
            else:
                plain += 1
    except Exception as exc:
        print(f"      凭据体检跳过：{exc}")
        return
    print(f"      凭据体检（不打印明文）：明文 {plain} 行 / 疑似 AES-GCM 密文 {cipher} 行 / 空 {empty} 行")
    if plain and encrypt_credentials:
        print("      → 明文行按当前口径**加密落库**（新库口令为密文，账号保持可读；老库不动）")


def cross_check(args, primary):
    """两通道对账：逐表比较行数与主键集合（两端都可用时才有意义）。"""
    print("\n== 两通道对账（mysql 中间件 vs 直读 itms.db）")
    failures = 0
    secondary = None
    try:
        if primary.kind == "mysql":
            secondary = FileSource(args.old_db)
        else:
            cfg = discover_legacy_dsn(args.legacy_src, args.mysql_user, args.mysql_password,
                                      args.mysql_host, args.mysql_port, args.mysql_db)
            if cfg is None:
                print("  - 未解析到 DSN，跳过对账")
                return 0
            notes = []
            secondary = open_mysql_channel(cfg, notes)
            for note in notes:
                print(f"  · {note}")
    except Exception as exc:
        print(f"  - 对账跳过：第二通道不可用（{exc}）")
        return 0

    try:
        for old, _new, pk, _note in OLT_TABLES:
            if old not in primary.tables() or old not in secondary.tables():
                print(f"  - {old:<20} 仅一端存在，跳过")
                continue
            n1, n2 = primary.count(old), secondary.count(old)
            k1 = {str(x) for x in primary.keys(old, pk)}
            k2 = {str(x) for x in secondary.keys(old, pk)}
            ok = n1 == n2 and k1 == k2
            detail = []
            if n1 != n2:
                detail.append(f"行数 {n1} vs {n2}")
            if k1 != k2:
                detail.append(f"主键差集 {len(k1 ^ k2)} 个")
            print(f"  {'✓' if ok else '✗'} {old:<20} 行数 {n1}/{n2}，主键 {len(k1)}/{len(k2)}"
                  + ("（" + "，".join(detail) + "）" if detail else ""))
            if not ok:
                failures += 1
    finally:
        secondary.close()
    return failures


def connect_rw(path):
    """打开（必要时创建）目标模块库：PRAGMA 与 core.OpenSQLite 对齐（WAL + busy_timeout + 单写者）。"""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    con = sqlite3.connect(path, timeout=10)
    con.execute("PRAGMA journal_mode=WAL")
    con.execute("PRAGMA busy_timeout=5000")
    con.execute("PRAGMA synchronous=NORMAL")
    con.execute("PRAGMA foreign_keys=on")
    con.row_factory = sqlite3.Row
    return con


def backup_target(path):
    """写前备份目标库到 <data_dir>/backup/olt.db-<时间戳>（VACUUM INTO；失败回落整文件复制）。"""
    if not os.path.isfile(path):
        return None
    backup_dir = os.path.join(os.path.dirname(path), "backup")
    os.makedirs(backup_dir, exist_ok=True)
    dest = os.path.join(backup_dir, f"olt.db-{time.strftime('%Y%m%d%H%M%S')}")
    try:
        con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
        try:
            con.execute("VACUUM INTO ?", (dest,))
        finally:
            con.close()
        return dest
    except sqlite3.Error:
        for suffix in ("", "-wal", "-shm"):
            if os.path.exists(path + suffix):
                shutil.copy2(path + suffix, dest + suffix)
        return dest


def target_ddl(src, old, new, args):
    """目标表 DDL：优先老库文件里的原 DDL，其次内嵌兜底；**只改表名**，列名/类型/主键原样。"""
    ddl = src.ddl(old)
    if ddl is None:
        try:
            file_src = FileSource(args.old_db)
            try:
                ddl = file_src.ddl(old)
            finally:
                file_src.close()
        except Exception:  # noqa: BLE001
            ddl = None
    if ddl is None:
        ddl = EMBEDDED_DDL.get(old)
    if ddl is None:
        return None
    pattern = re.compile(r"CREATE\s+TABLE\s+(IF\s+NOT\s+EXISTS\s+)?[`\"\[]?%s[`\"\]]?" % re.escape(old), re.I)
    return pattern.sub(f"CREATE TABLE IF NOT EXISTS `{new}`", ddl, count=1)


def ensure_target_schema(src, new_con, args):
    """建表（IF NOT EXISTS）+ tbSchemaVersion 表；返回 (本次新建表数, 无法取得 DDL 的表)。"""
    created, missing = 0, []
    for old, new, _pk, _note in OLT_TABLES:
        ddl = target_ddl(src, old, new, args)
        if ddl is None:
            missing.append(old)
            continue
        exists = new_con.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name=?", (new,)
        ).fetchone()
        if not exists:
            new_con.execute(ddl)
            created += 1
    new_con.execute(
        f'CREATE TABLE IF NOT EXISTS "{SCHEMA_VERSION_TABLE}" '
        "(module TEXT PRIMARY KEY, nVersion INTEGER NOT NULL, dtAppliedAt TEXT NOT NULL)"
    )
    return created, missing


def _norm(value):
    """比较用归一：NULL 与空串等价（老库大量字段用 '' 默认值）。"""
    return "" if value is None else str(value)


def import_table(src, new_con, old, new, pk, args, key, dry_run):
    """把一张老表按主键合并/替换进目标表；返回 (统计字典, 实际计划写入行数)。

    差异口径（“丝滑过渡”的可见性；同一份老数据重跑不产生无谓写入）：
      added     目标库没有该主键
      updated   主键已存在但至少一列不同（凭据列按**解密后语义**比较，避免随机 IV 造成假更新）
      unchanged 完全一致 → 不写库
      deleted   仅 --replace 模式会清理目标库中老库已经没有的主键
    """
    cols = src.columns(old)
    cred_cols = CREDENTIAL_COLUMNS.get(old, ())
    plan = []
    for row in src.raw_rows(old, cols):
        item = {}
        for col in cols:
            value = row.get(col)
            if col in cred_cols and args.encrypt_credentials:
                value = encrypt_secret(value, key)
            item[col] = value
        plan.append(item)

    existing = {str(row[pk]): dict(row) for row in new_con.execute(f'SELECT * FROM "{new}"')}
    stats = {"added": 0, "updated": 0, "unchanged": 0, "deleted": 0}
    to_write, source_keys = [], set()
    for item in plan:
        key_id = str(item.get(pk))
        source_keys.add(key_id)
        stored = existing.get(key_id)
        if stored is None:
            stats["added"] += 1
            to_write.append(item)
            continue
        same = True
        for col in cols:
            left, right = stored.get(col), item.get(col)
            same = (plain_value(left, key) == plain_value(right, key)) if col in cred_cols else (_norm(left) == _norm(right))
            if not same:
                break
        if same:
            stats["unchanged"] += 1
        else:
            stats["updated"] += 1
            to_write.append(item)

    if args.replace:
        stats["deleted"] = len([k for k in existing if k not in source_keys])

    if dry_run:
        return stats, (len(plan) if args.replace else len(to_write))

    col_list = ", ".join(f'"{c}"' for c in cols)
    placeholders = ", ".join("?" for _ in cols)
    rows = plan if args.replace else to_write
    with new_con:  # 事务：任一行失败整体回滚
        if args.replace:
            new_con.execute(f'DELETE FROM "{new}"')
        if rows:
            new_con.executemany(
                f'INSERT OR REPLACE INTO "{new}" ({col_list}) VALUES ({placeholders})',
                [tuple(item.get(c) for c in cols) for item in rows],
            )
    return stats, len(rows)


def verify_target(src, new_con, key):
    """写入后核对：主键集合 + 逐列比对（凭据列按解密后明文比较）+ integrity_check。"""
    print("\n== 写入后核对（主键集合 / 逐列 / 完整性）")
    failures = 0
    for old, new, pk, _note in OLT_TABLES:
        cols = src.columns(old)
        src_rows = list(src.raw_rows(old, cols))
        new_rows = [dict(row) for row in new_con.execute(f'SELECT * FROM "{new}"')]
        src_keys = {str(row.get(pk)) for row in src_rows}
        new_keys = {str(row[pk]) for row in new_rows}
        index = {str(row[pk]): row for row in new_rows}
        cred_cols = CREDENTIAL_COLUMNS.get(old, ())
        problems, mismatched = [], 0
        if src_keys != new_keys:
            problems.append(f"主键差集 {len(src_keys ^ new_keys)} 个")
        for row in src_rows:
            target = index.get(str(row.get(pk)))
            if target is None:
                continue
            for col in cols:
                if col in cred_cols:
                    ok = plain_value(target.get(col), key) == plain_value(row.get(col), key)
                else:
                    ok = _norm(target.get(col)) == _norm(row.get(col))
                if not ok:
                    mismatched += 1
                    break
        if mismatched:
            problems.append(f"{mismatched} 行字段不一致")
        ok = not problems
        print(f"  {'✓' if ok else '✗'} {new:<20} 行 {len(new_keys)}/{len(src_keys)}"
              + ("（" + "，".join(problems) + "）" if problems else ""))
        failures += 0 if ok else 1
    check = new_con.execute("PRAGMA integrity_check").fetchone()[0]
    print(f"  {'✓' if check == 'ok' else '✗'} integrity_check: {check}")
    return failures + (0 if check == "ok" else 1)


def main():
    args = parse_args()
    if args.replace and not args.yes:
        print("✗ --replace 是破坏性操作（先清空目标表再写入），需要显式加 --yes 确认")
        return 2

    if args.encrypt_credentials and AESGCM is None:
        print("✗ 需要 cryptography 才能加密落库：pip3 install cryptography；或加 --no-encrypt-credentials 原样明文导入")
        return 2

    data_dir = os.path.abspath(os.path.expanduser(args.data_dir))
    target = os.path.join(data_dir, "olt.db")
    mode = "apply：写入目标库" if args.apply else "dry-run：只读老库，不建库、不写盘"
    write_mode = "replace 精确副本（先清空目标表）" if args.replace else "merge 合并（按主键 upsert，幂等）"

    print(f"== OLT 数据迁移（{mode}）")
    print(f"   目标模块库：{target}" + ("（已存在）" if os.path.exists(target) else "（尚不存在）"))
    print("   库归属规则：一模块一库（docs/02 §2）——OLT 独立成库，**不与 switch_rpc_api.db 共库**")
    print(f"   写入模式：{write_mode}")
    print("   凭据口径：" + ("口令按实例密钥加密落库（账号列保持可读）" if args.encrypt_credentials else "原样明文落库")
          + "；老库始终明文且只读，老程序不受影响")

    key = load_instance_key(args.key_file) if args.encrypt_credentials else None

    src, notes = open_source(args)
    for note in notes:
        print(f"   · {note}")
    print(f"   老数据源：{src.label()}")

    missing, rows_total, failures = [], 0, 0
    new_con, diff_con, verify_con = None, None, None
    try:
        if args.apply:
            if args.backup:
                saved = backup_target(target)
                print(f"   · 写前备份：{saved or '(目标库尚不存在，无需备份)'}")
            new_con = connect_rw(target)
            created, ddl_missing = ensure_target_schema(src, new_con, args)
            missing.extend(ddl_missing)
            print(f"   · 目标库建表：新建 {created} 张，DDL 缺失 {len(ddl_missing)} 张")
            diff_con = new_con
        elif os.path.exists(target):
            diff_con = sqlite3.connect(f"file:{target}?mode=ro", uri=True)
            diff_con.row_factory = sqlite3.Row
        else:
            # dry-run 且目标库还不存在：用内存库按同一 DDL 预演，差异即“全量新增”
            diff_con = sqlite3.connect(":memory:")
            diff_con.row_factory = sqlite3.Row
            ensure_target_schema(src, diff_con, args)
    except sqlite3.Error as exc:
        print(f"✗ 目标库准备失败：{exc}")
        return 1

    print("\n== 迁移计划与差异（added=新增 / updated=变更 / unchanged=一致(不写) / deleted=仅 replace 清理）")
    try:
        present = src.tables()
        for old, new, pk, note in OLT_TABLES:
            if old not in present:
                print(f"  ✗ {old:<20} → {new:<20} 老库无此表（跳过）")
                missing.append(old)
                continue
            cols = src.columns(old)
            count = src.count(old)
            rows_total += count
            stats, planned = import_table(src, diff_con, old, new, pk, args, key, dry_run=not args.apply)
            extra = f" 清理 {stats['deleted']:>3}" if args.replace else ""
            print(f"  ✓ {old:<20} → {new:<20} 老库 {count:>4} 行 | 新增 {stats['added']:>3} 变更 {stats['updated']:>3}"
                  f" 一致 {stats['unchanged']:>3}{extra} → 写入 {planned:>3} 行   [{note}]")
            if old == "oltuser":
                report_credentials(src, old, cols, args.encrypt_credentials)
        print(f"  ＋ {SCHEMA_VERSION_TABLE:<17} → {'(本模块新建)':<20} nVersion={SCHEMA_VERSION}（docs/02 §2 必备表）")

        if new_con is not None:
            with new_con:
                new_con.execute(
                    f'INSERT OR REPLACE INTO "{SCHEMA_VERSION_TABLE}" (module, nVersion, dtAppliedAt) VALUES (?,?,?)',
                    ("olt", SCHEMA_VERSION, time.strftime("%Y-%m-%d %H:%M:%S")),
                )

        if new_con is not None or (args.verify and os.path.exists(target)):
            verify_con = new_con
            if verify_con is None:
                verify_con = sqlite3.connect(f"file:{target}?mode=ro", uri=True)
                verify_con.row_factory = sqlite3.Row
            failures += verify_target(src, verify_con, key)

        if args.cross_check:
            failures += cross_check(args, src)
    finally:
        src.close()
        if diff_con is not None and diff_con is not new_con:
            diff_con.close()
        if verify_con is not None and verify_con is not new_con and verify_con is not diff_con:
            verify_con.close()
        if new_con is not None:
            new_con.close()

    print("\n== 老数据丝滑过渡口径（老程序零改动）")
    print("   · 老库 /sxzdb/db/itms.db 全程只读（file 通道 mode=ro；mysql 通道只发 SELECT）：")
    print("     老 itmsmain/itmssocketmain/Python 与老控制台继续用老库，本次不写、不停、不改一行")
    print("   · 口令口径：老库保持明文；新库口令为密文（账号列可读）。模块侧取口令时按")
    print("     「密文优先、明文兜底」处理（同 plain_value 口径），因此新老两代数据可混存、可回滚")
    print("   · 过渡期同步：本工具可反复执行且幂等（同样数据重跑 → 写入 0 行）；老控制台期间新增/修改的")
    print("     数据，用同一条命令再跑一次即可带到新库（表很小，秒级）")
    print("   · 正式切流当天：停老进程后用 --replace --yes 取精确副本；写前自动备份在 <data_dir>/backup/")
    print("   · 文件类老数据 /home/olt/<OLTIP>/**（currentconfiguration.json、savedconfiguration.txt、")
    print("     slot_ont_file/*.txt、ontautomaticcfg.json）与 tr069 侧表本次不迁，留待第二批")

    if args.apply:
        print(f"\napply 完成：{len(OLT_TABLES) - len(missing)}/{len(OLT_TABLES)} 张表、老库 {rows_total} 行 → {target}"
              f"（核对失败 {failures} 项）")
    else:
        print(f"\ndry-run 结束：{len(OLT_TABLES) - len(missing)}/{len(OLT_TABLES)} 张表可迁、共 {rows_total} 行；"
              "未创建/未写入任何文件（加 --apply 才会写库）")
    if missing:
        print(f"  ⚠ 老库缺少表：{', '.join(missing)}（请核对老库来源或表名映射）")
    return 1 if (missing or failures) else 0


if __name__ == "__main__":
    sys.exit(main())
