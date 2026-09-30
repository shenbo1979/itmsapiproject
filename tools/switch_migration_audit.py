#!/usr/bin/env python3
"""交换机功能（含注册）迁入审计。

做两件事：
  A. 对账：把老壳 /switchweb/src/{api.js,app.js} 会发出的每个 (端点, nAct) 组合，
     用“空载荷”打统一工程一遍——返回“尚未迁移（过渡期）”说明还是兼容层兜底，
     其它返回（业务参数错误也算）说明已由真实处理器接管 → 用于证明功能已迁入。
  B. 注册链路 E2E：另起一个隔离实例（未注册）→ 按老壳注册弹窗的字段提交注册 →
     校验业务接口由 -3 解锁为 0 → 校验注册信息回读 → 收尾清进程。

用法：
  make switch-audit                      # 对账打运行中的界面/骨架（默认 18094）
  BASE=http://127.0.0.1:18092 python3 tools/switch_migration_audit.py
  SKIP_REGISTRATION=1 python3 tools/switch_migration_audit.py   # 只跑对账
"""
import binascii
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request

BASE = os.environ.get("BASE", "http://127.0.0.1:18094")
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SWITCHWEB = os.environ.get("SWITCHWEB_ROOT", "/switchweb")
AUDIT_PORT = int(os.environ.get("AUDIT_PORT", "18098"))
BIN = os.path.join(REPO_ROOT, "dist", "txapi")

# 老壳 switchweb 调用的路径 → 统一工程里的职责说明。
OWNER = {
    "/itms/handleRegistrationInfo/": "平台层：注册信息（含 AES 密钥落盘与轮换）",
    "/itms/validateThirdToken/": "平台层：登录令牌校验",
    "/itms/handleSwitchVenue/": "交换模块：场景基础资料",
    "/itms/handleSwitchManufacturer/": "交换模块：制造商基础资料",
    "/itms/handleSwitchPurpose/": "交换模块：用途基础资料",
    "/itms/handleSwitchProtocol/": "交换模块：协议基础资料",
    "/itms/handleSwitchModel/": "交换模块：型号基础资料",
    "/itms/handleSwitchInfo/": "交换模块：设备台账（凭据加解密）",
    "/itms/handleDeviceInformation/": "交换模块：设备面板（缓存 / 实时采集）",
    "/itms/handleSwitchPortDetail/": "交换模块：端口详情与启停 / 描述",
    "/itms/handleSwitchVlan/": "交换模块：端口 VLAN 查询与下发、设备 VLAN 增删改",
    "/itms/handleSwitchTerminal/": "交换模块：SSH 交互终端（会话 / 命令 / 关闭）",
    "/itms/getTerminalMAC/": "交换模块：终端 MAC 定位",
    "/itms/handleSwitchCollectionSettings/": "交换模块：周期采集配置",
    "/itms/handleSwitchSnifferConfig/": "交换模块：在线嗅探配置",
    "/itms/handleVlanInformation/": "交换模块：VLAN 信息",
    "/itms/handleVlanClassifier/": "交换模块：VLAN 分类",
    "/itms/handleRpcApiConfig/": "交换模块：RPC-API 接口设置",
}

# 老壳 api.js 里出现、但属 M2 范围（olt / tr069）的路径：不算本次「交换机功能迁入」的缺口。
DEFERRED_PATHS = {
    "/itms/handleOltConnect/",
    "/itms/handleBoardAndSlotInfo/",
}

# 审计矩阵：老壳（switchweb）会发出的 (端点, nAct) 组合。
# nAct 一律用「非法 JSON 载荷」探测：任何写分支都会在参数校验处失败，不会改数据。
MATRIX = [
    ("/itms/handleRegistrationInfo/", [0, 1], False),
    ("/itms/validateThirdToken/", [], False),
    ("/itms/handleSwitchVenue/", [0, 1], False),
    ("/itms/handleSwitchManufacturer/", [0, 1], False),
    ("/itms/handleSwitchPurpose/", [0, 1], False),
    ("/itms/handleSwitchProtocol/", [0, 1], False),
    ("/itms/handleSwitchModel/", [0, 1], False),
    ("/itms/handleSwitchInfo/", [0, 1, 2, 3], False),
    ("/itms/handleDeviceInformation/", [0, 1], True),
    ("/itms/handleSwitchPortDetail/", [0, 1, 2], False),
    ("/itms/handleSwitchVlan/", [0, 1, 2, 3, 4, 5, 6], False),
    ("/itms/handleSwitchTerminal/", [1, 2, 3], False),
    ("/itms/getTerminalMAC/", [0], False),
    ("/itms/handleSwitchCollectionSettings/", [0, 1], False),
    ("/itms/handleSwitchSnifferConfig/", [0, 1], False),
    ("/itms/handleVlanInformation/", [0, 1], False),
    ("/itms/handleVlanClassifier/", [0, 1], False),
    ("/itms/handleRpcApiConfig/", [0, 1], False),
]

failures = 0


def report(ok, name, extra=""):
    global failures
    print(f"  {'✓' if ok else '✗'} {name}{f'（{extra}）' if extra else ''}")
    if not ok:
        failures += 1


def post(base, path, fields, timeout=25):
    body = urllib.parse.urlencode(fields).encode("utf-8")
    req = urllib.request.Request(
        base + path,
        data=body,
        headers={"Content-Type": "application/x-www-form-urlencoded"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read().decode("utf-8")
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return {"code": None, "message": raw[:120], "data": None}


def is_transitional(payload):
    """兼容层「尚未迁移」过渡响应判定：message 命中，或 data.stage 存在。"""
    message = str(payload.get("message") or "")
    data = payload.get("data")
    if "尚未迁移" in message or "过渡期" in message:
        return True
    return isinstance(data, dict) and "stage" in data


# ── A. 功能迁入对账 ──
def audit_matrix():
    print(f"== A. 功能迁入对账（老壳调用面 → 统一工程实现）目标 {BASE} ==")
    covered = set()
    for path, acts, uses_conn in MATRIX:
        covered.add(path)
        probes = acts if acts else [None]
        for act in probes:
            fields = {}
            if act is not None:
                fields["nAct"] = act
                fields["connData" if uses_conn else "saveData"] = "{"
            payload = post(BASE, path, fields)
            transitional = is_transitional(payload)
            label = f"{path}（{OWNER.get(path, '—')}）" if act is None else f"{path} nAct={act}"
            report(
                not transitional,
                label,
                f"code={payload.get('code')} {str(payload.get('message'))[:38]}",
            )
    # 与老壳源码比对：老壳 api.js 里的路径分两类——交换机/平台层必须全在矩阵内；
    # olt 两条（handleOltConnect / handleBoardAndSlotInfo）属 M2 范围，只做提示不算失败。
    api_js = os.path.join(SWITCHWEB, "src", "api.js")
    if os.path.exists(api_js):
        text = open(api_js, encoding="utf-8").read()
        paths = set(re.findall(r'["\'](/itms/[A-Za-z0-9_]+/)["\']', text))
        missing = sorted(p for p in paths if p not in covered)
        deferred = [p for p in missing if p in DEFERRED_PATHS]
        report(
            not [p for p in missing if p not in DEFERRED_PATHS],
            "审计矩阵覆盖老壳 api.js 的全部交换机/平台层路径",
            ", ".join(p for p in missing if p not in DEFERRED_PATHS) or f"{len(paths)} 条中 {len(deferred)} 条属 M2",
        )
        if deferred:
            print(f"  - M2 范围（olt/tr069，本次不算迁入缺口）：{', '.join(deferred)}")
    else:
        print(f"  - 跳过老壳源码比对：未找到 {api_js}")

# ── B. 注册链路 E2E（未注册 → 注册 → 业务解锁）──
def free_port(port, attempts=12, delay=0.5):
    """端口是否可绑定（SO_REUSEADDR + 重试，容忍上一次实例刚退出的短暂占用）。"""
    for _ in range(attempts):
        with socket.socket() as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind(("127.0.0.1", port))
                return True
            except OSError:
                time.sleep(delay)
    return False


def wait_port_released(port, attempts=20, delay=0.5):
    return free_port(port, attempts=attempts, delay=delay)


def registration_flow():
    print(f"\n== B. 注册链路 E2E（隔离实例 :{AUDIT_PORT}）==")
    if not os.path.exists(BIN):
        report(False, "找到骨架二进制", f"{BIN}（先 make build）")
        return
    if not free_port(AUDIT_PORT):
        report(False, f"端口 {AUDIT_PORT} 空闲", "被占用，可 AUDIT_PORT=18099 重试")
        return

    workdir = tempfile.mkdtemp(prefix="switch-audit.")
    data_dir = os.path.join(workdir, "data")
    reg_dir = os.path.join(workdir, "reg")
    os.makedirs(data_dir, exist_ok=True)
    os.makedirs(reg_dir, exist_ok=True)
    env = dict(os.environ)
    env.update(
        {
            "TXAPI_PORT": str(AUDIT_PORT),
            "TXAPI_MODULES": env.get("TXAPI_MODULES", "switch,olt,tr069"),
            "TXAPI_DATA_DIR": data_dir,
            "TXAPI_REGISTRATION_DIR": reg_dir,
        }
    )
    log = open(os.path.join(workdir, "txapi.log"), "w+", encoding="utf-8")
    proc = subprocess.Popen([BIN], env=env, stdout=log, stderr=subprocess.STDOUT)
    base = f"http://127.0.0.1:{AUDIT_PORT}"
    try:
        ready = False
        for _ in range(40):
            try:
                with urllib.request.urlopen(base + "/healthz", timeout=1) as resp:
                    if resp.status == 200:
                        ready = True
                        break
            except Exception:
                time.sleep(0.5)
        if not ready:
            log.seek(0)
            report(False, "隔离实例启动就绪", str(log.read())[-200:])
            return
        report(True, "隔离实例启动就绪（未注册环境）", workdir)

        # 1) 未注册：业务接口 -3，注册接口可读
        guarded = post(base, "/itms/handleSwitchInfo/", {"nAct": 0})
        report(guarded.get("code") == -3, "未注册时业务接口返回 -3（注册守卫）", f"code={guarded.get('code')}")
        draft = post(base, "/itms/handleRegistrationInfo/", {"nAct": 0})
        report(
            draft.get("code") == 0 and draft.get("data", {}).get("registered") is False,
            "未注册时注册信息接口可用（草稿记录）",
            f"registered={draft.get('data', {}).get('registered')}",
        )
        record = draft.get("data", {}).get("record", {}) or {}

        # 2) 按老壳注册弹窗的字段集提交注册
        #    szAesKey：与老壳 generateRegistrationAESKey() 一致——16 字节随机 → 32 位十六进制字符串；
        #    nBrowserTime：毫秒时间戳（后端用它做浏览器时钟偏差校验）。
        aes_key = binascii.hexlify(os.urandom(16)).decode("ascii")
        payload = {
            "nId": record.get("nId", 0),
            "szCompanyName": "迁入审计",
            "szMachineId": record.get("szMachineId", ""),
            "szSerialNumber": "",
            "szAesKey": aes_key,
            "szRandomCode": record.get("szRandomCode", ""),
            "dtRegistrationDate": record.get("dtRegistrationDate", ""),
            "nValidDuration": record.get("nValidDuration", 30) or 30,
            "szRemarks": "switch_migration_audit",
            "nBrowserTime": int(time.time() * 1000),
        }
        saved = post(
            base,
            "/itms/handleRegistrationInfo/",
            {"nAct": 1, "saveData": json.dumps(payload, ensure_ascii=False)},
        )
        report(saved.get("code") == 0, "提交注册（老壳同款字段集）", f"code={saved.get('code')} {str(saved.get('message'))[:40]}")

        # 3) 注册后：业务接口解锁、注册信息回读、AES 密钥落盘
        unlocked = post(base, "/itms/handleSwitchInfo/", {"nAct": 0})
        report(unlocked.get("code") == 0, "注册后业务接口解锁（-3 → 0）", f"code={unlocked.get('code')}")
        info = post(base, "/itms/handleRegistrationInfo/", {"nAct": 0}).get("data", {}) or {}
        report(
            info.get("registered") is True and not info.get("expired"),
            "注册信息回读（已注册 / 未过期）",
            f"remainingDays={info.get('remainingDays')} expiresAt={info.get('expiresAt')}",
        )
        key_file = os.path.join(reg_dir, "aeskey.key")
        key_ok = False
        if os.path.exists(key_file):
            stored = open(key_file, encoding="utf-8").read().strip()
            key_ok = stored == aes_key or stored.lower() == aes_key.lower()
        report(key_ok, "注册写入的 AES 密钥已落盘（交换机凭据加密链路依赖它）", key_file)

        # 4) 令牌校验接口可用（登录链路现状：只做方法校验并返回成功）
        token = post(base, "/itms/validateThirdToken/", {})
        report(token.get("code") == 0, "登录令牌校验接口可用", f"code={token.get('code')}")
    finally:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)
        log.close()
        shutil.rmtree(workdir, ignore_errors=True)
        # 释放端口，保证连续多次运行（CI/本地反复跑）不被上一次实例挡住。
        wait_port_released(AUDIT_PORT)


# ── 入口 ──
def main():
    audit_matrix()
    if os.environ.get("SKIP_REGISTRATION") not in ("1", "true", "yes"):
        registration_flow()
    else:
        print("\n== B. 注册链路 E2E 已跳过（SKIP_REGISTRATION=1）==")
    print(f"\n结果：{'全部通过（交换机功能 + 注册链路均已由统一工程承担）' if failures == 0 else f'{failures} 项失败'}")
    return 0 if failures == 0 else 1


if __name__ == "__main__":
    sys.exit(main())

