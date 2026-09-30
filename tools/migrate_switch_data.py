#!/usr/bin/env python3
"""交换机数据迁移（M2）：老库 /home/switch_rpc_api.db → 统一工程模块库。

为什么需要它：M1 只迁了「功能」（接口 + 库表结构），老设备上的**台账与历史采集数据仍在老库里**；
本工具把老库行搬到统一工程的一模块一库（`<data-dir>/switch.db`）里，使升级后界面直接看到原数据。

设计要点（与 docs/02 第 4 节一致）：
  * 老库**只读**打开（file:...?mode=ro），永不写入；
  * 表结构与老库完全一致（同一批代码生成），因此用 `INSERT OR REPLACE ... SELECT` 直搬，
    设备凭据（szRpcUsername/szRpcPassword）是密文，**无需也不会重新加密**——
    生产环境新旧使用同一把 `/home/switchfile/aeskey.key`，搬完即可解密；
  * 幂等：按主键 nId 收敛，重复执行结果一致；`--replace` 先清空目标表（让老库数据成为唯一来源）；
  * 目标库不存在时按老库 DDL 建表；启用 WAL + busy_timeout，服务在跑也能迁；
  * 注册记录（tbValidation，位于平台库）默认**不动**，需要时用 `--include-registration` 显式迁。

用法：
  make migrate-switch-data                 # = 迁到 $DEV_DIR/data，替换演示台账
  python3 tools/migrate_switch_data.py --dry-run
  python3 tools/migrate_switch_data.py --data-dir /tmp/txapi-demo/data --replace --yes
  python3 tools/migrate_switch_data.py --include-registration            # 连注册记录一起搬
"""
import argparse
import base64
import binascii
import os
import re
import shutil
import sqlite3
import sys
import time

try:  # 仅“凭据体检 / 重写”两个可选功能需要；缺失时给出明确提示
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM
except ImportError:  # pragma: no cover
    AESGCM = None

# 与 internal/core/aeskey.go 一致的线格式：base64( IV(12) || 密文 || tag(16) )，AES-256-GCM，无 AAD。
aes_gcm_iv_size = 12
aes_gcm_tag_size = 16

OLD_DB_DEFAULT = "/home/switch_rpc_api.db"
OLD_SHARE_DIR_DEFAULT = "/home/switchfile"
DATA_DIR_DEFAULT = os.environ.get("TXAPI_DATA_DIR") or "/tmp/txapi-dev/data"

# 交换机模块表 → 目标库文件（一模块一库；平台表在 platform.db）
SWITCH_TABLES = [
    "tbVenue",
    "tbManufacturer",
    "tbPurpose",
    "tbProtocol",
    "tbModel",
    "tbDeviceInformation",
    "tbSwitchCollectionSnapshot",
    "tbSwitchCollectionInterfaceStat",
]
PLATFORM_TABLES = ["tbValidation"]

# 采集明细表可能很大，迁移时按批提交，避免一次性占用内存/长事务。
BATCH = 2000


def connect_ro(path):
    if not os.path.exists(path):
        raise SystemExit(f"老库不存在：{path}")
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    return con


def connect_rw(path):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    con = sqlite3.connect(path, timeout=10)
    con.execute("PRAGMA journal_mode=WAL")
    con.execute("PRAGMA busy_timeout=5000")
    con.row_factory = sqlite3.Row
    return con


def table_names(con):
    if con is None:
        return set()
    return {
        row[0]
        for row in con.execute("SELECT name FROM sqlite_master WHERE type='table'")
    }


def table_ddl(con, table):
    row = con.execute(
        "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", (table,)
    ).fetchone()
    if row and row[0]:
        # 老库 DDL 里可能带库名限定，统一去掉
        return re.sub(r"\s", " ", row[0] if isinstance(row[0], str) else row["sql"]), row[0]
    return None, None


def columns(con, table):
    return [row[1] for row in con.execute(f'PRAGMA table_info("{table}")')]


def copy_table(old_con, new_con, table, replace, dry_run):
    """把一张表的全部行搬过去；返回 (老库行数, 搬迁行数, 跳过原因或空串)。"""
    old_tables = table_names(old_con)
    new_tables = table_names(new_con)
    if table not in old_tables:
        return 0, 0, "老库无此表"
    old_cols = columns(old_con, table)
    if table not in new_tables:
        if dry_run:
            return (
                old_con.execute(f'SELECT COUNT(*) FROM "{table}"').fetchone()[0],
                0,
                "目标库无此表（将按老库 DDL 建表）",
            )
        ddl, _ = table_ddl(old_con, table)
        if not ddl:
            return 0, 0, "无法取得建表语句"
        new_con.execute(ddl)
        new_con.commit()
    new_cols = columns(new_con, table)
    cols = [c for c in old_cols if c in new_cols]
    dropped = [c for c in old_cols if c not in new_cols]
    total = old_con.execute(f'SELECT COUNT(*) FROM "{table}"').fetchone()[0]
    if dry_run:
        return total, 0, f"将搬迁 {len(cols)} 列" + (f"，忽略目标库没有的列 {dropped}" if dropped else "")

    if replace:
        new_con.execute(f'DELETE FROM "{table}"')
        new_con.commit()
    placeholders = ", ".join("?" for _ in cols)
    insert_sql = (
        f'INSERT OR REPLACE INTO "{table}" ({", ".join(chr(34) + c + chr(34) for c in cols)}) '
        f"VALUES ({placeholders})"
    )
    moved = 0
    cursor = old_con.execute(f'SELECT {", ".join(chr(34) + c + chr(34) for c in cols)} FROM "{table}"')
    batch = []
    for row in cursor:
        batch.append(tuple(row))
        if len(batch) >= BATCH:
            new_con.executemany(insert_sql, batch)
            moved += len(batch)
            batch.clear()
    if batch:
        new_con.executemany(insert_sql, batch)
        moved += len(batch)
    new_con.commit()
    note = f"搬迁 {len(cols)} 列" + (f"，忽略 {dropped}" if dropped else "")
    return total, moved, note


# 文件类老数据（老目录 → 模块数据目录）：设备采集缓存 + 采集/嗅探配置。
# 注意：绝不复制 *.key（设备密钥属注册平台，路径不变）；登录记录默认不迁（--include-login）。
FILE_DIR_SOURCES = ["devicefile"]
FILE_SINGLE_SOURCES = ["collectionsettings.json", "sniffinginfocfg.json"]
NEVER_COPY_SUFFIXES = (".key",)

def default_key_file(data_dir):
    """目标实例的 AES 密钥文件默认位置：<DEV_DIR>/reg/aeskey.key（DEV_DIR 是数据目录的父目录）。"""
    return os.path.join(os.path.dirname(os.path.abspath(data_dir)), "reg", "aeskey.key")


def load_instance_key(data_dir, key_file=None, aes_key=None):
    """按 core.NormalizeAESKey 的规则解析目标实例的 AES-256 密钥：32 字节原文 / 64 位十六进制 / base64。"""
    raw = str(aes_key or "").strip()
    path = key_file or default_key_file(data_dir)
    if not raw:
        if not os.path.isfile(path):
            raise SystemExit(f"AES 密钥文件不存在：{path}（可用 --key-file / --aes-key 指定）")
        raw = open(path, encoding="utf-8").read().strip()
        print(f"密钥来源：{path}")
    if len(raw) == 32:
        return raw.encode("utf-8")
    if len(raw) == 64:
        try:
            decoded = binascii.unhexlify(raw)
            if len(decoded) == 32:
                return decoded
        except binascii.Error:
            pass
    try:
        decoded = base64.b64decode(raw, validate=False)
    except Exception as exc:  # noqa: BLE001
        raise SystemExit(f"AES 密钥无法解析：{exc}")
    if len(decoded) == 32:
        return decoded
    raise SystemExit("AES 密钥格式无效：需要 32 字节原文 / 64 位十六进制 / base64(32 字节)")


def decrypt_secret(blob, key):
    """解密本工程线格式的凭据：base64( IV(12) || 密文 || tag(16) )。"""
    data = base64.b64decode(blob, validate=False)
    if len(data) < aes_gcm_iv_size + aes_gcm_tag_size:
        raise ValueError("密文长度不足")
    return AESGCM(key).decrypt(data[:aes_gcm_iv_size], data[aes_gcm_iv_size:], None).decode("utf-8")


def encrypt_secret(plain, key):
    """加密为本工程线格式（与 core.EncryptSecret 完全一致）；空串返回空串。"""
    text = str(plain or "")
    if not text:
        return ""
    iv = os.urandom(aes_gcm_iv_size)
    return base64.b64encode(iv + AESGCM(key).encrypt(iv, text.encode("utf-8"), None)).decode("ascii")


def credential_verdict(value, key):
    """判断一条凭据在目标实例下能否使用：可解密 / 不可解密（异密钥密文）/ 明文 / 空。"""
    text = str(value or "").strip()
    if not text:
        return "空"
    try:
        decrypt_secret(text, key)
        return "可解密"
    except Exception:  # noqa: BLE001
        pass
    try:
        blob = base64.b64decode(text, validate=False)
    except Exception:  # noqa: BLE001
        return "明文"
    if len(blob) >= aes_gcm_iv_size + aes_gcm_tag_size:
        return "不可解密（异密钥密文）"
    return "明文"


def check_credentials(db_path, key):
    """体检：逐台设备报告凭据在目标实例下能否解密（切流前用它确认哪些设备需要重存密码）。"""
    print("\n== 凭据可解密性体检")
    if not os.path.isfile(db_path):
        print(f"  - 目标库不存在：{db_path}")
        return 0
    con = connect_ro(db_path)
    rows = list(con.execute("SELECT nId, szName, szManageIP, szRpcUsername, szRpcPassword FROM tbDeviceInformation ORDER BY nId"))
    con.close()
    needs_fix = []
    for row in rows:
        user_verdict = credential_verdict(row["szRpcUsername"], key)
        pass_verdict = credential_verdict(row["szRpcPassword"], key)
        usable = "不可解密" not in user_verdict and "不可解密" not in pass_verdict
        print(f"  {'✓' if usable else '✗'} #{row['nId']:<3} {row['szName'] or '(未命名)':<10} {row['szManageIP'] or '--':<16} 账号={user_verdict:<12} 密码={pass_verdict}")
        if not usable:
            needs_fix.append(row["szName"] or f"#{row['nId']}")
    if needs_fix:
        print(f"  → 需要重存密码的设备：{', '.join(needs_fix)}（用 --device-credential 名称:账号:密码 重写）")
    else:
        print("  → 全部设备的凭据在目标实例下均可使用。")
    return len(needs_fix)


def rewrite_credentials(db_path, key, assignments, dry_run):
    """用目标实例的密钥重写指定设备的凭据（仅开发/演示需要；生产环境新旧同密钥，无需此步）。"""
    print("\n== 凭据重写（用目标实例密钥重新加密）")
    if not os.path.isfile(db_path):
        print(f"  - 目标库不存在：{db_path}")
        return 1
    con = connect_rw(db_path)
    failures = 0
    for item in assignments:
        parts = item.split(":", 2)
        if len(parts) != 3:
            print(f"  ✗ 参数格式应为 名称:账号:密码，收到 {item!r}")
            failures += 1
            continue
        name, username, password = (part.strip() for part in parts)
        if dry_run:
            print(f"  · 将重写 {name} 的账号/密码（dry-run）")
            continue
        cursor = con.execute(
            "UPDATE tbDeviceInformation SET szRpcUsername = ?, szRpcPassword = ? WHERE szName = ?",
            (encrypt_secret(username, key), encrypt_secret(password, key), name),
        )
        if cursor.rowcount == 0:
            print(f"  ✗ 未找到设备 {name}")
            failures += 1
            continue
        con.commit()
        # 立即回读校验：能解出提交的明文即证明格式与模块一致
        row = con.execute("SELECT szRpcUsername, szRpcPassword FROM tbDeviceInformation WHERE szName = ?", (name,)).fetchone()
        ok = decrypt_secret(row["szRpcUsername"], key) == username and decrypt_secret(row["szRpcPassword"], key) == password
        print(f"  {'✓' if ok else '✗'} {name}：凭据已用目标实例密钥重新加密（回读校验{'通过' if ok else '失败'}）")
        if not ok:
            failures += 1
    con.close()
    return failures


def primary_key(con, table):
    rows = [row for row in con.execute(f'PRAGMA table_info("{table}")') if row[5]]
    if len(rows) == 1:
        return rows[0][1]
    return None


def migrate_files(old_share_dir, data_dir, include_login, dry_run):
    """把老目录里的采集缓存与配置 JSON 复制到模块数据目录（*.key 永不复制）。

    对应关系（老实现共用 /home/switchfile，新实现一模块一目录）：
      devicefile/*.json            → <data>/devicefile/            （设备面板“读取缓存”用）
      collectionsettings.json      → <data>/collectionsettings.json（周期采集配置）
      sniffinginfocfg.json         → <data>/sniffinginfocfg.json   （在线嗅探配置）
      loginfile.json (+ .key)      → <data>/loginfile.json         （仅 --include-login）
    """
    copied_total = 0
    for name in FILE_DIR_SOURCES:
        src_dir = os.path.join(old_share_dir, name)
        dst_dir = os.path.join(data_dir, name)
        if not os.path.isdir(src_dir):
            print(f"  - {name}/：老目录不存在，跳过")
            continue
        files = sorted(f for f in os.listdir(src_dir) if f.endswith(".json") and not f.endswith(NEVER_COPY_SUFFIXES))
        if not dry_run:
            os.makedirs(dst_dir, exist_ok=True)
        for name_file in files:
            if not dry_run:
                shutil.copy2(os.path.join(src_dir, name_file), os.path.join(dst_dir, name_file))
        copied_total += len(files)
        print(f"  ✓ {name + '/':<26} 复制 {len(files):>4} 个 json → {dst_dir}")
    for name in FILE_SINGLE_SOURCES:
        src = os.path.join(old_share_dir, name)
        if not os.path.isfile(src):
            print(f"  - {name}：老文件不存在，跳过")
            continue
        if not dry_run:
            os.makedirs(data_dir, exist_ok=True)
            shutil.copy2(src, os.path.join(data_dir, name))
        copied_total += 1
        print(f"  ✓ {name:<26} 复制 {os.path.getsize(src):>5} 字节 → {os.path.join(data_dir, name)}")
    if include_login:
        for name in ("loginfile.json", "loginfile.key"):
            src = os.path.join(old_share_dir, name)
            if not os.path.isfile(src):
                print(f"  - {name}：老文件不存在，跳过")
                continue
            if not dry_run:
                shutil.copy2(src, os.path.join(data_dir, name))
            copied_total += 1
            print(f"  ✓ {name:<26} 复制 {os.path.getsize(src):>5} 字节 → {os.path.join(data_dir, name)}（--include-login）")
    for entry in sorted(os.listdir(old_share_dir)) if os.path.isdir(old_share_dir) else []:
        if entry.endswith(NEVER_COPY_SUFFIXES):
            print(f"  · {entry}：密钥文件，按约定不迁移（注册平台负责，路径不变）")
    return copied_total


def main():
    parser = argparse.ArgumentParser(description="交换机数据迁移：老库 → 统一工程模块库（老库只读）")
    parser.add_argument("--old-db", default=OLD_DB_DEFAULT, help=f"老库路径（只读），默认 {OLD_DB_DEFAULT}")
    parser.add_argument("--data-dir", default=DATA_DIR_DEFAULT, help=f"统一工程数据目录，默认 {DATA_DIR_DEFAULT}")
    parser.add_argument("--tables", default=",".join(SWITCH_TABLES), help="要迁移的表（逗号分隔）")
    parser.add_argument("--replace", action="store_true", help="先清空目标表再搬（让老库成为唯一来源）")
    parser.add_argument("--include-registration", action="store_true", help="同时迁移注册记录 tbValidation（平台库）")
    parser.add_argument("--old-share-dir", default=OLD_SHARE_DIR_DEFAULT, help=f"老文件目录（设备采集缓存/配置），默认 {OLD_SHARE_DIR_DEFAULT}")
    parser.add_argument("--skip-files", action="store_true", help="只迁数据库表，不迁文件类数据")
    parser.add_argument("--include-login", action="store_true", help="同时迁移登录记录 loginfile.json(+.key)")
    parser.add_argument("--key-file", default="", help="目标实例 AES 密钥文件（默认 <data-dir>/../reg/aeskey.key）")
    parser.add_argument("--aes-key", default="", help="直接指定目标实例 AES 密钥（32 字节 / 64 位 hex / base64）")
    parser.add_argument("--check-credentials", action="store_true", help="体检：报告每台设备凭据在目标实例下能否解密")
    parser.add_argument(
        "--device-credential",
        action="append",
        default=[],
        metavar="名称:账号:密码",
        help="用目标实例密钥重写该设备凭据（可重复；仅开发/演示需要）",
    )
    parser.add_argument("--dry-run", action="store_true", help="只报告将做什么，不写目标库")
    parser.add_argument("--yes", action="store_true", help="跳过写库前的确认提示（供 make/CI 使用）")
    args = parser.parse_args()

    requested = [t.strip() for t in args.tables.split(",") if t.strip()]
    switch_tables = [t for t in requested if t in SWITCH_TABLES]
    platform_tables = [t for t in requested if t in PLATFORM_TABLES]
    if args.include_registration and "tbValidation" not in platform_tables:
        platform_tables.append("tbValidation")
    unknown = [t for t in requested if t not in SWITCH_TABLES + PLATFORM_TABLES]
    if unknown:
        raise SystemExit(f"未知表：{', '.join(unknown)}")

    old_path = os.path.abspath(args.old_db)
    data_dir = os.path.abspath(args.data_dir)
    targets = []
    if switch_tables:
        targets.append((os.path.join(data_dir, "switch.db"), switch_tables))
    if platform_tables:
        targets.append((os.path.join(data_dir, "platform.db"), platform_tables))

    print(f"老库（只读）：{old_path}")
    print(f"目标目录：{data_dir}")
    for path, tables in targets:
        print(f"  → {path}：{', '.join(tables)}")
    if not args.skip_files:
        print(f"  → {data_dir}/devicefile/ 等文件类数据 ← {args.old_share_dir}")
    print(f"模式：{'dry-run（不写库）' if args.dry_run else '真实迁移'}{'，先清空目标表' if args.replace else '，按主键合并'}")

    if not args.dry_run and not args.yes:
        answer = input("确认迁移？服务运行中也可以（WAL），但建议先低峰操作 [y/N] ").strip().lower()
        if answer not in ("y", "yes"):
            print("已取消。")
            return 0

    started = time.time()
    old_con = connect_ro(old_path)
    summary = []
    failures = 0
    for path, tables in targets:
        if args.dry_run:
            new_con = connect_ro(path) if os.path.exists(path) else None
        else:
            new_con = connect_rw(path)
        print(f"\n== {path}")
        for table in tables:
            try:
                total, moved, note = copy_table(old_con, new_con, table, args.replace, args.dry_run)
            except sqlite3.Error as exc:
                print(f"  ✗ {table:<34} 失败：{exc}")
                failures += 1
                summary.append((table, "失败", 0, 0))
                continue
            mark = "✓"
            print(f"  {mark} {table:<34} 老库 {total:>6} 行 → 搬迁 {moved:>6} 行（{note}）")
            summary.append((table, note, total, moved))
        if new_con is not None:
            # 迁移后做一次轻量自检：完整性 + 计数核对
            check = new_con.execute("PRAGMA integrity_check").fetchone()[0]
            if check != "ok":
                print(f"  ✗ 完整性检查：{check}")
                failures += 1
            new_con.close()
    old_con.close()

    if not args.dry_run:
        # 行核对：按主键比对（比计数更准）——合并模式要求“老库的键都在新库里”，
        # 替换模式还要求“新库里没有多余键”；老库表为空时（如基础资料）不算失败。
        print("\n== 行核对（按主键）")
        old_con = connect_ro(old_path)
        for path, tables in targets:
            new_con = connect_rw(path)
            for table in tables:
                pk = primary_key(old_con, table)
                if pk is None:
                    print(f"  - {table}：无单列主键，跳过键核对")
                    continue
                old_keys = {row[0] for row in old_con.execute(f'SELECT "{pk}" FROM "{table}"')}
                new_keys = {row[0] for row in new_con.execute(f'SELECT "{pk}" FROM "{table}"')}
                missing = old_keys - new_keys
                extra = new_keys - old_keys
                ok = not missing and (not args.replace or not extra)
                note = []
                if missing:
                    note.append(f"缺失 {len(missing)} 键")
                if extra:
                    note.append(f"额外 {len(extra)} 键（本库原有行，合并模式允许）")
                print(
                    f"  {'✓' if ok else '✗'} {table:<34} 老库 {len(old_keys):>6} 键 → 新库 {len(new_keys):>6} 键"
                    + ("（" + "，".join(note) + "）" if note else "")
                )
                if not ok:
                    failures += 1
            new_con.close()
        old_con.close()

    print(f"\n完成：{len(summary)} 张表，用时 {time.time() - started:.1f}s，失败 {failures} 项")
    if not args.skip_files:
        print("\n== 文件类老数据（设备采集缓存 / 采集与嗅探配置）")
        try:
            copied = migrate_files(args.old_share_dir, data_dir, args.include_login, args.dry_run)
            print(f"完成：复制 {copied} 个文件" + ("（dry-run，未落盘）" if args.dry_run else ""))
        except OSError as exc:
            print(f"  ✗ 复制失败：{exc}")
            failures += 1

    # 凭据体检 / 重写（可选）：解决“迁移过来的密文用本地实例密钥解不开”的问题。
    if args.check_credentials or args.device_credential:
        if AESGCM is None:
            print("\n✗ 需要 cryptography 才能做凭据体检/重写：pip3 install cryptography")
            failures += 1
        else:
            try:
                key = load_instance_key(data_dir, args.key_file or None, args.aes_key or None)
                switch_db = os.path.join(data_dir, "switch.db")
                if args.check_credentials:
                    check_credentials(switch_db, key)
                if args.device_credential:
                    failures += rewrite_credentials(switch_db, key, args.device_credential, args.dry_run)
            except SystemExit as exc:
                print(f"✗ {exc}")
                failures += 1

    if not args.dry_run:
        print("\n提示：生产环境新旧使用同一把 /home/switchfile/aeskey.key，设备凭据密文搬完即可解密；")
        print("      本地演示若用隔离注册目录，则该密钥与老库密文不匹配，界面里密码字段会解不开（属预期）。")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
