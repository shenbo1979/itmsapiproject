"""本地假 OLT（Huawei MA5800 风格）Telnet 服务：仅用于统一工程的只读联调/验证。

用法：python3 tools/fake_olt_telnet.py <port> [bind_host]
  - bind_host 默认 127.0.0.1；联调工具需要走非 127.0.0.1 的本地地址（如 127.0.0.2）时传 0.0.0.0。
只回只读命令（display ... / screen-length ...），不实现任何写操作。
"""
import os
import socket
import sys
import threading

PROMPT = "\r\nMA5800-X7>"

# 可选模型切换：FAKE_OLT_MODEL=ma5603 时按 MA5603T（MA5600 系）风格回显——
#   主控槽 6（非 0）、裸 `display version` 先弹交互提示（{ <cr>|backplane|frameid/slotid }:）、
#   补回车后回显完整版本文本（大小写混合 `Software Version:` + PRODUCT + Uptime）。
# 默认（空）保持 MA5800 风格，不影响 `make olt-ui`/`olt-dom` 对 MA5800 的断言。
MODEL = os.getenv("FAKE_OLT_MODEL", "")
MA5603_PROMPT = "\r\nMA5603T>"
MA5800_CFG_PROMPT = "\r\nMA5800-X7(config)#"
MA5603_CFG_PROMPT = "\r\nMA5603T(config)#"

# 假 OLT 的终端登录账号集合（跨连接共享，模拟设备上真实存在的账号；读配置时逐行回显）。
USER_SET = {"admin", "olt_operator"}
# 假 OLT 的 DBA 模板集合（跨连接共享，模拟设备上真实存在的 DBA 模板；读配置时逐行回显）。
# 与 parse_profile_test.go 的 dbaSampleConfig 同一套 5 种 type；`dba-profile add/delete` 写命令会更新它，
# 使 handleDBAProfile Act=1/2 后的 `display current-configuration` 重读能反映增删（真实设备同口径）。
DBA_PROFILES = {
    10: {"name": "DBA Main", "type": "type3", "fix": 0, "assure": 20032, "max": 1000000},
    20: {"name": "Fixed", "type": "type1", "fix": 512, "assure": 0, "max": 0},
    30: {"name": "Assured", "type": "type2", "fix": 0, "assure": 1024, "max": 0},
    40: {"name": "Maxed", "type": "type4", "fix": 0, "assure": 0, "max": 4096},
    50: {"name": "Full", "type": "type5", "fix": 128, "assure": 256, "max": 512},
}
LOCK = threading.Lock()


def dba_profiles_config() -> str:
    """把当前 DBA 模板集合渲染成 `[global-config]` 段的 `dba-profile add` 行（读配置时回显）。"""
    lines = []
    for pid in sorted(DBA_PROFILES):
        p = DBA_PROFILES[pid]
        band = " ".join(
            ["%s %d" % (key, p[key]) for key in ("fix", "assure", "max") if p.get(key, 0) > 0]
        )
        # profile-name 无空格时不加引号（parseOLTDBAProfiles 两种形式都支持）。
        lines.append('  dba-profile add profile-id %d profile-name "%s" %s %s\r\n' % (pid, p["name"], p["type"], band))
    return "".join(lines)


def parse_dba_profile(cmd: str):
    """解析 `dba-profile add/delete` 命令并更新全局 DBA_PROFILES（真实设备写操作后配置随之变化）。"""
    import re
    with LOCK:
        if cmd.startswith("dba-profile delete"):
            match = re.search(r"profile-id (\d+)", cmd)
            if match:
                DBA_PROFILES.pop(int(match.group(1)), None)
            return
        if not cmd.startswith("dba-profile add"):
            return
        match = re.search(r"profile-id (\d+)\s+profile-name \"([^\"]+)\"|profile-id (\d+)\s+profile-name (\S+)", cmd)
        if not match:
            return
        pid = int(match.group(1) or match.group(3))
        name = match.group(2) or match.group(4)
        profile = {"name": name, "type": "type1", "fix": 0, "assure": 0, "max": 0}
        type_match = re.search(r"\b(type[1-5])\b", cmd)
        if type_match:
            profile["type"] = type_match.group(1)
        for key in ("fix", "assure", "max"):
            value_match = re.search(r"\b%s (\d+)\b" % key, cmd)
            if value_match:
                profile[key] = int(value_match.group(1))
        DBA_PROFILES[pid] = profile


# —— ONT 线路模板（ont-lineprofile）全局状态 ——
# 与 DBA_PROFILES 同一模式：`display current-configuration` 的 [global-config] 段按当前集合渲染，
# `ont-lineprofile`/`undo ont-lineprofile` 写命令实时更新，使 handleLineProfile Act=1/2 后的重读
# 能反映增删（真实设备同口径）。初始两条与 Go 假 OLT 的 lineProfileSampleConfig 一致。
LINE_PROFILES = {
    10: {
        "type": "gpon",
        "name": "ClientA",
        "dba": 10,
        "lines": [
            "  tcont 1 dba-profile-id 10\r\n",
            "  gem add 0 eth tcont 1\r\n",
            "  gem mapping 0 0 vlan 100\r\n",
            "  gem mapping 0 1 vlan 200\r\n",
            "  commit\r\n",
        ],
    },
    20: {
        "type": "gpon",
        "name": "ClientB",
        "dba": 20,
        "lines": [
            "  tcont 1 dba-profile-id 20\r\n",
            "  gem add 1 eth tcont 1\r\n",
            "  gem mapping 1 0 vlan 300\r\n",
            "  commit\r\n",
        ],
    },
}
# 新增线路模板的进行中状态：收到 `ont-lineprofile ...` 后开启，`commit` 时固化进 LINE_PROFILES。
LINE_PENDING = {}


def line_profiles_config() -> str:
    """把当前线路模板集合渲染成 [global-config] 段的 ont-lineprofile 子树（读配置时回显）。"""
    lines = []
    for pid in sorted(LINE_PROFILES):
        p = LINE_PROFILES[pid]
        lines.append('  ont-lineprofile %s profile-id %d profile-name "%s"\r\n' % (p["type"], pid, p["name"]))
        lines.extend(p["lines"])
    return "".join(lines)


def parse_line_profile(cmd: str):
    """解析线路模板写命令并更新全局 LINE_PROFILES（真实设备写操作后配置随之变化）。

    命令序列（后端 applyLineProfileMapping 逐条下发）：
      ont-lineprofile <type> profile-id <id> profile-name <name>
      tr069-management enable|disable   tcont <i> dba-profile-id <dba>
      gem add <i> eth tcont <t>          gem mapping <i> <j> vlan <v>
      commit                              quit
    """
    import re
    with LOCK:
        if cmd.startswith("undo ont-lineprofile"):
            match = re.search(r"profile-id (\d+)", cmd)
            if match:
                LINE_PROFILES.pop(int(match.group(1)), None)
            return
        if cmd.startswith("ont-lineprofile gpon") or cmd.startswith("ont-lineprofile epon"):
            match = re.search(
                r"ont-lineprofile (\S+) profile-id (\d+) profile-name \"([^\"]+)\"|ont-lineprofile (\S+) profile-id (\d+) profile-name (\S+)",
                cmd,
            )
            if match:
                LINE_PENDING.clear()
                LINE_PENDING["type"] = match.group(1) or match.group(4)
                LINE_PENDING["id"] = int(match.group(2) or match.group(5))
                LINE_PENDING["name"] = match.group(3) or match.group(6)
                LINE_PENDING["dba"] = 0
                LINE_PENDING["lines"] = []
            return
        if not LINE_PENDING:
            return
        if cmd.startswith("tcont "):
            m = re.search(r"tcont (\d+) dba-profile-id (\d+)", cmd)
            if m:
                LINE_PENDING["dba"] = int(m.group(2))
                LINE_PENDING["lines"].append("  " + cmd + "\r\n")
        elif cmd.startswith("gem add") or cmd.startswith("gem mapping"):
            LINE_PENDING["lines"].append("  " + cmd + "\r\n")
        elif cmd == "commit":
            pid = LINE_PENDING.get("id")
            if pid is not None and pid not in LINE_PROFILES:
                LINE_PROFILES[pid] = {
                    "type": LINE_PENDING.get("type", "gpon"),
                    "name": LINE_PENDING.get("name", "Line"),
                    "dba": LINE_PENDING.get("dba", 0),
                    "lines": LINE_PENDING.get("lines", []) + ["  commit\r\n"],
                }
            LINE_PENDING.clear()


# —— ONT 业务模板（ont-srvprofile）全局状态 ——
# 与 LINE_PROFILES 同一模式：`display current-configuration` 的 [global-config] 段按当前集合渲染，
# `ont-srvprofile`/`undo ont-srvprofile` 写命令实时更新。初始两条（SrvA / SrvB）供
# handleSrvProfile Act=0 解析与「业务/TR069模板」TAB 断言。
SRV_PROFILES = {
    10: {
        "type": "gpon",
        "name": "SrvA",
        "pots": 2,
        "eths": 4,
        "vlans": [
            "  port vlan eth 1 translation 100\r\n",
            "  port vlan eth 2 translation 200\r\n",
            "  port vlan eth 3 translation 300\r\n",
        ],
    },
    20: {
        "type": "epon",
        "name": "SrvB",
        "pots": 0,
        "eths": 1,
        "vlans": [
            "  port vlan eth 1 translation 400\r\n",
        ],
    },
}
# 新增业务模板的进行中状态：收到 `ont-srvprofile ...` 后开启，`commit` 时固化进 SRV_PROFILES。
SRV_PENDING = {}

# —— TR069 管理模板（ont tr069-server-profile）全局状态 ——
# 与 DBA_PROFILES 同一模式：读配置时回显（不含密码），`ont tr069-server-profile add/delete` 写命令
# 实时更新。初始两条（ACS-A / ACS-B）供 handleTr069Profile Act=0 解析与「业务/TR069模板」TAB 断言。
TR069_PROFILES = {
    1: {"name": "ACS-A", "url": "http://acs.example.com", "user": "acsuser"},
    2: {"name": "ACS-B", "url": "https://acs2.example.com", "user": "admin"},
}


def srv_profiles_config() -> str:
    """把当前业务模板集合渲染成 [global-config] 段的 ont-srvprofile 子树（读配置时回显）。"""
    lines = []
    for pid in sorted(SRV_PROFILES):
        p = SRV_PROFILES[pid]
        lines.append('  ont-srvprofile %s profile-id %d profile-name "%s"\r\n' % (p["type"], pid, p["name"]))
        lines.append("  ont-port pots %d eth %d\r\n" % (p["pots"], p["eths"]))
        lines.extend(p["vlans"])
        lines.append("  commit\r\n")
    return "".join(lines)


def tr069_profiles_config() -> str:
    """把当前 TR069 管理模板集合渲染成 [global-config] 段的 ont tr069-server-profile 行（读配置时回显，不含密码）。"""
    lines = []
    for pid in sorted(TR069_PROFILES):
        p = TR069_PROFILES[pid]
        # 与老实现命令同构：`ont tr069-server-profile add profile-id <id> profile-name <name> url <url> user <user> <pass> auth-realm auth`
        lines.append('  ont tr069-server-profile add profile-id %d profile-name "%s" url %s user %s ****** auth-realm auth\r\n'
                     % (pid, p["name"], p["url"], p["user"]))
    return "".join(lines)


def parse_srv_profile(cmd: str):
    """解析业务模板写命令并更新全局 SRV_PROFILES（真实设备写操作后配置随之变化）。

    命令序列（后端 applySrvProfileCommand 逐条下发）：
      ont-srvprofile <type> profile-id <id> profile-name <name>
      ont-port pots <N> eth <M>   port vlan eth <i> <vlan>   commit
    """
    import re
    with LOCK:
        if cmd.startswith("undo ont-srvprofile"):
            match = re.search(r"profile-id (\d+)", cmd)
            if match:
                SRV_PROFILES.pop(int(match.group(1)), None)
            return
        if cmd.startswith("ont-srvprofile gpon") or cmd.startswith("ont-srvprofile epon"):
            match = re.search(
                r"ont-srvprofile (\S+) profile-id (\d+) profile-name \"([^\"]+)\"|ont-srvprofile (\S+) profile-id (\d+) profile-name (\S+)",
                cmd,
            )
            if match:
                SRV_PENDING.clear()
                SRV_PENDING["type"] = match.group(1) or match.group(4)
                SRV_PENDING["id"] = int(match.group(2) or match.group(5))
                SRV_PENDING["name"] = match.group(3) or match.group(6)
                SRV_PENDING["pots"] = 0
                SRV_PENDING["eths"] = 0
                SRV_PENDING["vlans"] = []
            return
        if not SRV_PENDING:
            return
        if cmd.startswith("ont-port "):
            m = re.search(r"ont-port pots (\S+) eth (\S+)", cmd)
            if m:
                def to_int(v):
                    try:
                        return int(v)
                    except ValueError:
                        return 0
                SRV_PENDING["pots"] = to_int(m.group(1))
                SRV_PENDING["eths"] = to_int(m.group(2))
        elif cmd.startswith("port vlan eth"):
            SRV_PENDING["vlans"].append("  " + cmd + "\r\n")
        elif cmd == "commit":
            pid = SRV_PENDING.get("id")
            if pid is not None and pid not in SRV_PROFILES:
                SRV_PROFILES[pid] = {
                    "type": SRV_PENDING.get("type", "gpon"),
                    "name": SRV_PENDING.get("name", "Srv"),
                    "pots": SRV_PENDING.get("pots", 0),
                    "eths": SRV_PENDING.get("eths", 0),
                    "vlans": SRV_PENDING.get("vlans", []),
                }
            SRV_PENDING.clear()


def parse_tr069_profile(cmd: str):
    """解析 TR069 管理模板写命令并更新全局 TR069_PROFILES（真实设备写操作后配置随之变化）。"""
    import re
    with LOCK:
        if cmd.startswith("ont tr069-server-profile delete"):
            match = re.search(r"profile-id (\d+)", cmd)
            if match:
                TR069_PROFILES.pop(int(match.group(1)), None)
            return
        if not cmd.startswith("ont tr069-server-profile add"):
            return
        match = re.search(
            r'profile-id (\d+)\s+profile-name "([^"]+)" url (\S+) user (\S+)|profile-id (\d+)\s+profile-name (\S+) url (\S+) user (\S+)',
            cmd,
        )
        if not match:
            return
        # 两个分支的组编号：分支1（带引号）=g1..g4，分支2（无引号）=g5..g8。
        name = match.group(2) or match.group(6)
        url = match.group(3) or match.group(7)
        user = match.group(4) or match.group(8)
        pid = int(match.group(1) or match.group(5))
        TR069_PROFILES[pid] = {
            "name": name,
            "url": url,
            "user": user,
        }

# MA5603T 风格完整版本文本（补回车后回显）：大小写混合 `Software Version:`（区别于 MA5800
# 全大写 VERSION），且同屏含 PCB/BIOS/Logic 等多行版本；另含 PRODUCT（机框）与 Uptime（运行时长）。
def full_ma5603_version_text() -> str:
    return (
        "\r\n  Main Board: H801SCUN\r\n"
        "  ---------------------------------------\r\n"
        "  PCB  Version: H801SCUN  VER B\r\n"
        "  Base     BIOS Version: 216\r\n"
        "  Extended BIOS Version: 222\r\n"
        "  Software      Version: MA5600V800R011C00\r\n"
        "  Logic         Version: (U48)115\r\n"
        "  MAB           Version: 0002\r\n"
        "  PRODUCT : MA5603T\r\n"
        "  Uptime is 42 day(s), 7 hour(s), 30 minute(s), 9 second(s)"
    )


def response(cmd: str, state: dict) -> str:
    """返回命令回显；state 用于维护 terminal user 交互状态（含持久化到全局 USER_SET）。"""
    in_config = bool(state.get("config"))
    if MODEL == "ma5603":
        PROMPT = MA5603_CFG_PROMPT if in_config else MA5603_PROMPT
    else:
        PROMPT = MA5800_CFG_PROMPT if in_config else "\r\nMA5800-X7>"
    if cmd == "enable":
        # 提升到特权模式（#），尚未进配置模式。
        return "\r\nMA5800-X7#" if MODEL != "ma5603" else "\r\nMA5603T#"
    if cmd == "scroll":
        return "\r\n  { <cr>|number<U><10,512> }:\r\nMA5800-X7#" if MODEL != "ma5603" else "\r\n  { <cr>|number<U><10,512> }:\r\nMA5603T#"
    if cmd == "config":
        state["config"] = True
        return (MA5800_CFG_PROMPT if MODEL != "ma5603" else MA5603_CFG_PROMPT)
    if cmd.startswith("screen-length") or cmd.startswith("terminal length"):
        return PROMPT
    # —— MA5603T 风格：主控槽 6，裸 display version 先弹交互提示、补回车后回显完整版本文本 ——
    if MODEL == "ma5603":
        if cmd == "display board 0":
            return (
                "\r\n  ------------------------------------------------------------------\r\n"
                "  SlotID  BoardName  Status          SubType0 SubType1    Online/Offline\r\n"
                "  ------------------------------------------------------------------\r\n"
                "  4       H805GPBD   Normal\r\n"
                "  6       H801SCUN   Normal\r\n"
                "  8       H801GICF   Normal\r\n" + PROMPT
            )
        if cmd == "display board 0/4":
            return (
                "\r\n  Board Name    : H805GPBD\r\n  Board Status  : Normal\r\n"
                "  0      GPON       Online\r\n  1      GPON       Online\r\n  2      GPON       Offline\r\n"
                "  In port 0/4/0 the total of ONTs are: 8, online: 5\r\n"
                "  In port 0/4/1 the total of ONTs are: 2, online: 1\r\n" + PROMPT
            )
        if cmd == "display board 0/8":
            return (
                "\r\n  Board Name    : H801GICF\r\n  Board Status  : Normal\r\n"
                "  0      GE      --     1      --     auto       auto_full   --   --   online\r\n"
                "  1      GE      --     1      --     auto_1000  auto_full   --   --   offline\r\n" + PROMPT
            )
        if cmd == "display temperature 0":
            return "\r\n  SlotID : 4    Temperature : 41 C\r\n  SlotID : 6    Temperature : 38 C\r\n" + PROMPT
        if cmd == "display version":
            # 第一发：裸命令弹交互参数提示（不含版本号）；补回车后的空命令（见 cmd == ""）回显完整版本。
            if not state.get("version_prompted"):
                state["version_prompted"] = True
                return "\r\n  { <cr>|backplane|frameid/slotid }:"
            # 已弹过提示（再次命中）仍回显完整版本，避免重复弹提示。
            return full_ma5603_version_text() + PROMPT
        if cmd == "" and state.get("version_prompted"):
            # RunCommandUntilSignal 在参数提示时补回车 → 空命令 → 回显完整版本文本。
            state["version_prompted"] = False
            return full_ma5603_version_text() + PROMPT
    if cmd == "":
        return PROMPT
    if cmd.startswith("screen-length") or cmd.startswith("terminal length"):
        return PROMPT
    # —— 终端登录用户（写操作交互序列）：新增/修改/删除，持久化到全局 USER_SET ——
    if cmd == "terminal user name":
        state["mode"] = "add"
        state["pending"] = ""
        return "\r\n  Command:\r\n  user name\r\n  User profile name:\r\n" + PROMPT
    if cmd == "terminal user password":
        state["mode"] = "change"
        state["pending"] = ""
        return "\r\n  Save the configuration? (y/n)[n]:" + PROMPT
    if cmd == "undo terminal user name":
        state["mode"] = "delete"
        state["pending"] = ""
        return "\r\n  Are you sure to delete the user? (y/n)[n]:\r\n" + PROMPT
    if state["mode"] == "add":
        # 「terminal user name」后的第一个非命令行是用户名，其余是两次口令，回显带 "user profile name"。
        if not state["pending"] and cmd and not cmd.startswith("root"):
            state["pending"] = cmd
        if cmd == "n":
            if state["pending"]:
                with LOCK:
                    USER_SET.add(state["pending"])
            state["mode"] = ""
        return "\r\n  User profile name:\r\n" + PROMPT
    if state["mode"] in ("change", "delete") and cmd != "":
        if state["mode"] == "delete":
            if not state["pending"] and cmd != "y" and cmd != "n":
                state["pending"] = cmd
            if cmd == "n":
                with LOCK:
                    USER_SET.discard(state["pending"])
                state["mode"] = ""
        else:
            if cmd == "n":  # 修改口令确认后收尾
                state["mode"] = ""
        return "(y/n)" + PROMPT
    if cmd == "display version":
        return (
            "\r\n  VERSION : MA5800V100R019C10\r\n"
            "  PATCH   : SPC200 SPH208\r\n"
            "  PRODUCT : MA5800-X7\r\n"
            "  Uptime is 128 day(s), 5 hour(s), 20 minute(s), 7 second(s)\r\n" + PROMPT
        )
    if cmd == "display temperature 0":
        return "\r\n  SlotID : 0    Temperature : 36 C\r\n  SlotID : 1    Temperature : 41 C\r\n" + PROMPT
    if cmd == "display board 0":
        return (
            "\r\n  ------------------------------------------------------------------\r\n"
            "  SlotID  BoardName  Status          SubType0 SubType1    Online/Offline\r\n"
            "  ------------------------------------------------------------------\r\n"
            # 板卡名必须用"有示意图素材"的真实型号（web/src/assets/olt/olt_<型号>.png），
            # 否则联调只能覆盖"暂无示意图"的占位分支，覆盖不到板卡图片本身。
            "  0       H801SCUN   Normal\r\n"
            "  1       H805GPBD   Normal\r\n"
            "  2       H805GPBD   Offline\r\n"
            "  8       H801GICF   Normal\r\n" + PROMPT
        )
    if cmd == "display board 0/1":
        return (
            "\r\n  Board Name    : H805GPBD\r\n"
            "  Board Status  : Normal\r\n"
            "  0      GPON       Online\r\n"
            "  1      GPON       Online\r\n"
            "  In port 0/1/0 the total of ONTs are: 8, online: 5\r\n"
            "  In port 0/1/1 the total of ONTs are: 2, online: 1\r\n" + PROMPT
        )
    if cmd == "display board 0/8":
        # 上行接口板（GE 口）：列位置按解析器约定（fields[3]=NativeVLAN / [5]=Speed / [6]=Duplex / [9]=Link），
        # 真机的 GE 明细由 parse_board_detail.go 用同一组下标解析（实机数据见 docs/09 §5.2 #22）。
        return (
            "\r\n  Board Name    : H801GICF\r\n"
            "  Board Status  : Normal\r\n"
            "  0      GE      --     1      --     auto       auto_full   --   --   online\r\n"
            "  1      GE      --     1      --     auto_1000  auto_full   --   --   offline\r\n" + PROMPT
        )
    if cmd == "display current-configuration":
        with LOCK:
            users = sorted(USER_SET)
        user_lines = "".join("  terminal user name %s\r\n" % u for u in users)
        return (
            "\r\n  Command:\r\n  display current-configuration\r\n"
            "  !\r\n  hostname OLT-E2E\r\n"
            + user_lines +
            "  interface gpon 0/1\r\n"
            "  ont add 0 0 sn-auth 48575443AABBCCDD omci ont-lineprofile-id 10\r\n"
            "  interface vlanif100\r\n"
            "  ip address 192.168.50.1 255.255.255.0\r\n"
            "  quit\r\n"
            "  [vlan-config]\r\n"
            "  vlan 39 smart\r\n"
            "  vlan 46 smart\r\n"
            "  vlan 100 smart\r\n"
            "  vlan 139 smart\r\n"
            "  vlan 200 smart\r\n"
            "  vlan 300 smart\r\n"
            "  vlan 405 smart\r\n"
            "  vlan 2000 smart\r\n"
            "  vlan 4015 smart\r\n"
            "  vlan 4034 smart\r\n"
            "  vlan attrib 200 q-in-q\r\n"
            "  vlan 300 stacking\r\n"
            "  port vlan 100 0/3 0\r\n"
            "  #\r\n"
            "  [btv-config]\r\n"
            "  multicast-vlan 39\r\n"
            "  multicast-vlan 100\r\n"
            "  #\r\n"
            # —— DBA 模板（handleDBAProfile Act=0 读已保存配置解析）：覆盖全部 5 种 type，
            #    且随写命令（add/delete）实时变化，反映真实设备行为 ——
            "  [global-config]\r\n"
            + dba_profiles_config()
            # —— ONT 线路模板（handleLineProfile Act=0 读已保存配置解析）：随写命令（新增/删除）
            #    实时变化（line_profiles_config 按当前 LINE_PROFILES 渲染），与 DBA 模板同口径 ——
            + line_profiles_config()
            # —— ONT 业务模板（handleSrvProfile Act=0 读已保存配置解析）：随写命令（新增/删除）
            #    实时变化（srv_profiles_config 按当前 SRV_PROFILES 渲染）——
            + srv_profiles_config()
            # —— TR069 管理模板（handleTr069Profile Act=0 读已保存配置解析）：随写命令（新增/删除）
            #    实时变化（tr069_profiles_config 按当前 TR069_PROFILES 渲染，不含密码）——
            + tr069_profiles_config()
            + "  #\r\n"
            "  ip route-static 0.0.0.0 0.0.0.0 172.20.247.254\r\n"
            "  ip route-static 6.6.6.6 255.255.255.255 192.168.46.1\r\n"
            "  #\r\n  return\r\n" + PROMPT
        )
    # —— DBA 模板写操作（handleDBAProfile Act=1/2 下发命令后的回显）：新增/删除各回一条成功提示，
    #    并同步更新全局 DBA_PROFILES（后续 display current-configuration 会反映增删）——
    if cmd.startswith("dba-profile add") or cmd.startswith("dba-profile delete"):
        parse_dba_profile(cmd)
        return "\r\n  dba-profile success\r\n" + PROMPT
    # —— ONT 线路模板写操作（handleLineProfile Act=1/2 下发命令后的回显）——
    # 新增/删除各回一条成功提示，并同步更新全局 LINE_PROFILES（后续 display current-configuration
    # 会反映增删，与 DBA 模板同口径；逐句子命令在 LINE_PENDING 会话状态里收集、commit 时固化）。
    if (cmd.startswith("ont-lineprofile gpon") or cmd.startswith("ont-lineprofile epon")
            or cmd.startswith("undo ont-lineprofile")
            or cmd.startswith("tr069-management") or cmd.startswith("tcont ")
            or cmd.startswith("gem add") or cmd.startswith("gem mapping") or cmd == "quit"):
        parse_line_profile(cmd)
        return "\r\n  ont-lineprofile success\r\n" + PROMPT
    # —— ONT 业务模板写操作（handleSrvProfile Act=1/2 下发命令后的回显）——
    # 新增/删除各回一条成功提示，并同步更新全局 SRV_PROFILES（后续 display current-configuration
    # 会反映增删，与线路模板同口径；逐句子命令在 SRV_PENDING 里收集、commit 时固化）。
    if (cmd.startswith("ont-srvprofile gpon") or cmd.startswith("ont-srvprofile epon")
            or cmd.startswith("undo ont-srvprofile")
            or cmd.startswith("ont-port ") or cmd.startswith("port vlan eth")):
        parse_srv_profile(cmd)
        return "\r\n  ont-srvprofile success\r\n" + PROMPT
    # —— TR069 管理模板写操作（handleTr069Profile Act=1/2 下发命令后的回显）——
    # 新增/删除各回一条成功提示，并同步更新全局 TR069_PROFILES（后续 display current-configuration
    # 会反映增删，与 DBA 模板同口径）。
    if cmd.startswith("ont tr069-server-profile"):
        parse_tr069_profile(cmd)
        return "\r\n  ont tr069-server-profile success\r\n" + PROMPT
    # commit 同时收束线路模板与业务模板的进行中会话（两个 parser 各自只在 PENDING 非空时固化，
    # 互不干扰）；回显统一用线路模板文案（后端只等 `(config)` 提示符，不看回显内容）。
    if cmd == "commit":
        parse_line_profile(cmd)
        parse_srv_profile(cmd)
        return "\r\n  ont-lineprofile success\r\n" + PROMPT
    return PROMPT


def handle(conn: socket.socket) -> None:
    try:
        stream = conn.makefile("rwb", buffering=0)
        # 与真机（172.20.247.88:23，Huawei MA5600/MA5800 系列）逐字节一致：
        # Telnet 协商 IAC WILL ECHO + IAC WILL SGA，再接 "\r\n>>User name:"（合计 20 字节）。
        # 用户名提示里 "User" 与 "name" 之间有空格——联调工具必须覆盖它，
        # 否则会像 2026-09-22 那次一样：本地假 OLT 用 "Username:" 一直通过，真机登录却必然失败。
        conn.sendall(b"\xff\xfb\x01\xff\xfb\x03\r\n>>User name:")
        stream.readline()
        conn.sendall(b"\r\n>>User password:")
        stream.readline()
        login_prompt = MA5603_PROMPT if MODEL == "ma5603" else "\r\nMA5800-X7>"
        conn.sendall(
            b"\r\n  User last login information:\r\n"
            b"    Last login time  : 2026-09-22 15:50:00\r\n"
            b"    Last login IP    : 127.0.0.1\r\n"
            + login_prompt.encode()
            + b"\r\n"
        )
        state = {"mode": "", "pending": "", "config": ""}
        while True:
            line = stream.readline()
            if not line:
                return
            command = line.decode("utf-8", "replace").strip()
            conn.sendall(response(command, state).encode())
    except OSError:
        return
    finally:
        try:
            conn.close()
        except OSError:
            pass


def main() -> None:
    port = int(sys.argv[1])
    host = sys.argv[2] if len(sys.argv) > 2 else "127.0.0.1"
    server = socket.socket()
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind((host, port))
    server.listen(16)
    print("fake-olt listening on %s:%d" % (host, port), flush=True)
    while True:
        conn, _ = server.accept()
        threading.Thread(target=handle, args=(conn,), daemon=True).start()


if __name__ == "__main__":
    main()
