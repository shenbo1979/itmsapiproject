// Package devcli 提供设备 CLI（Telnet / SSH 交互会话）的共用判定。
//
// 这里**统一**「登录提示」的识别口径，避免各模块各写一套后再次出现同类缺陷：
// 2026-09-22 的 OLT 故障就是「Huawei OLT 的用户名提示 `>>User name:` 中间带空格，
// 而判定只匹配 `username`/`login:`」→ 永远不发送用户名 → 空等到登录超时报
// 「登录后未取得命令提示符（已读 20 字节）」（详见 docs/09-olt-m2.md §5.2 #18）。
//
// 两条口径约定：
//   - 先做**空白归一化**（去掉空格/制表/换行并转小写）再匹配，容忍 `User name:`、
//     `User  name :`、`Password :` 这类排版差异；
//   - 口令提示**要求带冒号**（归一化后含 `password:`）。只匹配裸词 `password` 会把
//     「The initial password poses security risks」这类欢迎语误判成口令提示，
//     从而把口令提前发出去（Huawei/Centec 的登录欢迎语里就有这类句子）。
package devcli

import (
	"strings"
	"unicode"
)

// LooksLikeUsernamePrompt 判断回显是否停在用户名提示上。
//
// 命中示例：`Username:`、`Login:`、`Login name:`、Huawei OLT 的 `>>User name:`。
// 判定按**回显结尾**做（真正的登录提示总是最后一屏的结尾），因此
// `Last login: 2026-09-22 15:50:00` 这类登录横幅不会被误判成提示；
// 不带冒号的 `username`（老 OLT 口径）同样接受。
func LooksLikeUsernamePrompt(output string) bool {
	compact := compact(output)
	for _, suffix := range []string{"username:", "username", "loginname:", "loginname", "login:"} {
		if strings.HasSuffix(compact, suffix) {
			return true
		}
	}
	return false
}

// LooksLikePasswordPrompt 判断回显是否停在口令提示上。
//
// 命中示例：`Password:`、`Password :`、`>>User password:`、`Enter password:`。
// 冒号是必需的（见包注释：避免把欢迎语里的 "password" 当成提示）。
func LooksLikePasswordPrompt(output string) bool {
	return strings.Contains(compact(output), "password:")
}

// compact 去掉回显里的空白字符（空格/制表/换行）并转小写。
// 各厂商提示符的排版差异（`User name:` / `User  name :`）在这一步被抹平。
func compact(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return unicode.ToLower(r)
	}, text)
}
