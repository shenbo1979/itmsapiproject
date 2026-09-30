#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""演示数据脚本（M1）：为本地骨架灌入示例的交换机基础资料与设备台账。

用途：拉起 WEB 壳后想立刻看到「设备台账 / 基础资料」页面的实际效果。

安全边界：
  * 只访问本地骨架服务（默认 http://127.0.0.1:18092，可用 --base-url 覆盖），
    非本机地址必须显式加 --allow-remote，避免误伤原系统；
  * 若尚未注册，会先用随机 AES 密钥完成一次 30 天注册（仅写骨架的隔离目录）；
  * 幂等：已存在的同名记录不会重复创建。

用法：
  python3 tools/demo_seed.py                 # 注册（如未注册）+ 灌示例数据
  python3 tools/demo_seed.py --check         # 只打印当前数据概览
"""
from __future__ import annotations

import argparse
import json
import os
import random
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_BASE_URL = "http://127.0.0.1:18092"

VENUES = ["一号机房", "二号机房", "研发实验室"]
MANUFACTURERS = ["华为", "H3C", "锐捷"]
PURPOSES = ["接入层", "汇聚层", "核心层"]
PROTOCOLS = ["STP", "RSTP", "MSTP"]
MODELS = [
    {"szName": "S5720-28X", "nVenueId": 1, "nPurposeId": 1, "nProtocolId": 3, "nManufacturerId": 1, "nEthPort": 24, "szRemarks": "接入交换机"},
    {"szName": "S5730-48C", "nVenueId": 2, "nPurposeId": 2, "nProtocolId": 3, "nManufacturerId": 1, "nEthPort": 48, "szRemarks": "汇聚交换机"},
    {"szName": "S6520X-30C", "nVenueId": 1, "nPurposeId": 3, "nProtocolId": 3, "nManufacturerId": 2, "nEthPort": 24, "szRemarks": "核心交换机"},
]
DEVICES = [
    {"szName": "接入-01", "szManageIP": "192.0.2.101", "nModelId": 1, "nPurposeId": 1, "szRpcHostip": "192.0.2.101", "nRpcPort": 22, "szRpcUsername": "admin", "szRpcPassword": "Admin@123", "nRpcType": 3, "nAutoEnable": 1, "szRemarks": "示例设备"},
    {"szName": "汇聚-01", "szManageIP": "192.0.2.102", "nModelId": 2, "nPurposeId": 2, "szRpcHostip": "192.0.2.102", "nRpcPort": 22, "szRpcUsername": "ops", "szRpcPassword": "Ops@12345", "nRpcType": 3, "nAutoEnable": 1, "szRemarks": "示例设备"},
    {"szName": "核心-01", "szManageIP": "192.0.2.103", "nModelId": 3, "nPurposeId": 3, "szRpcHostip": "192.0.2.103", "nRpcPort": 22, "szRpcUsername": "root", "szRpcPassword": "Root@1234", "nRpcType": 2, "nAutoEnable": 0, "szRemarks": "示例设备"},
]
NAME_ONLY_RESOURCES = [
    ("/itms/handleSwitchVenue/", "场景", VENUES),
    ("/itms/handleSwitchManufacturer/", "制造商", MANUFACTURERS),
    ("/itms/handleSwitchPurpose/", "用途", PURPOSES),
    ("/itms/handleSwitchProtocol/", "协议", PROTOCOLS),
]


def post(base_url: str, path: str, fields: dict, timeout: float = 8.0) -> dict:
    body = urllib.parse.urlencode(fields).encode("utf-8")
    request = urllib.request.Request(base_url.rstrip("/") + path, data=body, method="POST")
    request.add_header("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
    request.add_header("Accept", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return json.loads(response.read().decode("utf-8", errors="replace") or "{}")
    except urllib.error.HTTPError as exc:
        raise SystemExit("HTTP %s：%s" % (exc.code, exc.read().decode("utf-8", errors="replace")))


def call(base_url: str, path: str, act: int, payload=None) -> dict:
    fields = {"nAct": act}
    if payload is not None:
        fields["saveData"] = json.dumps(payload, ensure_ascii=False)
    result = post(base_url, path, fields)
    if result.get("code") != 0:
        raise SystemExit("%s nAct=%s 失败：%s" % (path, act, result.get("message")))
    return result


def ensure_registered(base_url: str) -> None:
    info = post(base_url, "/api/v2/platform/registration/", {"nAct": 0}).get("data") or {}
    if info.get("registered") and not info.get("expired"):
        print("· 已注册（到期 %s，剩余 %s 天）" % (info.get("dtExpiresAt"), info.get("nRemainingDays")))
        return
    aes_key = "".join(random.choice("0123456789abcdef") for _ in range(32))
    record = {
        "szCompanyName": "演示环境",
        "szAesKey": aes_key,
        "szRemarks": "demo_seed",
        "nBrowserTime": int(time.time() * 1000),
    }
    data = call(base_url, "/api/v2/platform/registration/", 1, record).get("data") or {}
    print("· 已注册演示环境（到期 %s，剩余 %s 天）" % (data.get("dtExpiresAt"), data.get("nRemainingDays")))


def seed_name_only(base_url: str, path: str, label: str, names: list) -> None:
    existing = {item.get("szName") for item in (call(base_url, path, 0).get("data") or [])}
    created = 0
    for name in names:
        if name in existing:
            continue
        call(base_url, path, 1, {"szName": name})
        created += 1
    print("· %s：新增 %d 条（已有 %d 条）" % (label, created, len(existing)))


def seed_models(base_url: str) -> None:
    path = "/itms/handleSwitchModel/"
    existing = {item.get("szName") for item in (call(base_url, path, 0).get("data") or [])}
    created = 0
    for model in MODELS:
        if model["szName"] in existing:
            continue
        payload = dict(model)
        payload["szCreationTime"] = time.strftime("%Y-%m-%d %H:%M:%S")
        call(base_url, path, 1, payload)
        created += 1
    print("· 型号：新增 %d 条（已有 %d 条）" % (created, len(existing)))


def seed_devices(base_url: str) -> None:
    path = "/itms/handleSwitchInfo/"
    existing = {item.get("szName") for item in (call(base_url, path, 0).get("data") or [])}
    created = 0
    for device in DEVICES:
        if device["szName"] in existing:
            continue
        payload = {"nId": 0, "szDevMac": "", "szGateway": "", "szSubnetMask": "", "szServerIP": "", "szRemarks": ""}
        payload.update(device)
        call(base_url, path, 1, payload)
        created += 1
    print("· 设备台账：新增 %d 条（已有 %d 条）" % (created, len(existing)))


def summarize(base_url: str) -> None:
    info = post(base_url, "/api/v2/platform/registration/", {"nAct": 0}).get("data") or {}
    if not info.get("registered"):
        print("\n当前数据概览：未注册（业务接口会被拦截，返回 code=-3）——请先完成注册或运行本脚本灌入演示数据。")
        return
    print("\n当前数据概览：")
    resources = NAME_ONLY_RESOURCES + [("/itms/handleSwitchModel/", "型号", None), ("/itms/handleSwitchInfo/", "设备台账", None)]
    for path, label, _ in resources:
        items = call(base_url, path, 0).get("data") or []
        print("  %-6s %d 条" % (label, len(items)))
    print("  注册   %s（剩余 %s 天，到期 %s）" % ("已注册", info.get("nRemainingDays"), info.get("dtExpiresAt")))



def main() -> int:
    parser = argparse.ArgumentParser(description="M1 演示数据灌入脚本")
    parser.add_argument("--base-url", default=os.environ.get("TXAPI_BASE_URL", DEFAULT_BASE_URL))
    parser.add_argument("--allow-remote", action="store_true", help="允许访问非本机地址")
    parser.add_argument("--check", action="store_true", help="只打印当前数据概览")
    args = parser.parse_args()

    host = urllib.parse.urlparse(args.base_url).hostname or ""
    if host not in ("127.0.0.1", "localhost", "::1") and not args.allow_remote:
        print("目标 %s 不是本机地址：为避免影响原系统，请显式加 --allow-remote" % args.base_url, file=sys.stderr)
        return 2

    try:
        if args.check:
            summarize(args.base_url)
            return 0
        ensure_registered(args.base_url)
        for path, label, names in NAME_ONLY_RESOURCES:
            seed_name_only(args.base_url, path, label, names)
        seed_models(args.base_url)
        seed_devices(args.base_url)
        summarize(args.base_url)
    except urllib.error.URLError as exc:
        print("无法连接骨架服务 %s：%s\n请先运行：make run DEV_DIR=/tmp/txapi-demo" % (args.base_url, exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
