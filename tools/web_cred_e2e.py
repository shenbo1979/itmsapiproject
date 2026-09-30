#!/usr/bin/env python3
"""前端加密 → 台账保存 → 回读解密 的端到端校验（走界面 18094 的反代，密钥用隔离注册目录）。

用途：确认「采用 switchweb 的 JS」后，设备凭据的加密/解密链路与本工程骨架完全兼容，
      且开发/演示环境不会读写设备的真实密钥文件。
"""
import json
import os
import urllib.parse
import urllib.request

BASE = os.environ.get("BASE", "http://127.0.0.1:18094")
NAME = "界面自检设备"


def post(path, fields):
    body = urllib.parse.urlencode(fields).encode("utf-8")
    req = urllib.request.Request(
        BASE + path, data=body, headers={"Content-Type": "application/x-www-form-urlencoded"}
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode("utf-8"))


def main():
    failures = 0

    enc = post("/switchweb/api/encrypt-switch-credentials",
               {"szUsername": "demo-user", "szPassword": "Demo@12345"})
    ok = enc.get("code") == 0 and enc.get("data", {}).get("szPassword")
    print(f"{'✓' if ok else '✗'} 界面端点加密凭据（code={enc.get('code')}）")
    failures += 0 if ok else 1
    if not ok:
        return failures
    print(f"   密文示例：{enc['data']['szPassword'][:32]}...")

    # 先清掉可能残留的同名设备，保证可重复运行
    listing = post("/itms/handleSwitchInfo/", {"nAct": 0}).get("data") or []
    stale = [item["nId"] for item in listing if item.get("szName") == NAME]
    if stale:
        post("/itms/handleSwitchInfo/", {"nAct": 3, "saveData": json.dumps(stale)})

    payload = {
        "szName": NAME,
        "szManageIP": "192.0.2.250",
        "szRpcHostip": "192.0.2.250",
        "nRpcPort": 22,
        "nRpcType": 3,
        "szRpcUsername": enc["data"]["szUsername"],
        "szRpcPassword": enc["data"]["szPassword"],
        "szRemarks": "switchweb 界面端到端自检",
    }
    saved = post("/itms/handleSwitchInfo/", {"nAct": 1, "saveData": json.dumps(payload, ensure_ascii=False)})
    ok = saved.get("code") == 0
    print(f"{'✓' if ok else '✗'} 台账保存密文凭据（code={saved.get('code')}，{saved.get('message')}）")
    failures += 0 if ok else 1

    listing = post("/itms/handleSwitchInfo/", {"nAct": 0}).get("data") or []
    row = next((item for item in listing if item.get("szName") == NAME), None)
    if row is None:
        print("✗ 回读设备（未找到刚保存的设备）")
        return failures + 1
    ok = row.get("szRpcUsername") == "demo-user" and row.get("szRpcPassword") == "Demo@12345"
    print(f"{'✓' if ok else '✗'} 回读明文凭据（后端用同一把 aeskey.key 解开：{row.get('szRpcUsername')} / {row.get('szRpcPassword')}）")
    failures += 0 if ok else 1

    removed = post("/itms/handleSwitchInfo/", {"nAct": 3, "saveData": json.dumps([row["nId"]])})
    ok = removed.get("code") == 0
    print(f"{'✓' if ok else '✗'} 清理自检数据（code={removed.get('code')}）")
    failures += 0 if ok else 1
    return failures


if __name__ == "__main__":
    code = main()
    print(f"\n结果：{'全部通过' if code == 0 else f'{code} 项失败'}")
    raise SystemExit(0 if code == 0 else 1)
