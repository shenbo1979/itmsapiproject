package devcli

import "testing"

// TestLooksLikeUsernamePrompt 覆盖用户名提示的识别口径（含真机排版）。
func TestLooksLikeUsernamePrompt(t *testing.T) {
	hit := []string{
		">>User name:", // Huawei OLT 真机（172.20.247.88:23，User 与 name 之间有空格）
		"\xff\xfb\x01\xff\xfb\x03\r\n>>User name:", // 带 Telnet 协商字节的完整欢迎信息
		"User  name :", // 多余空格
		"Username:",
		"username:",
		"Login:",
		"login:",
		"Login name:",
		"Please enter your username", // 不带冒号的老口径排版
	}
	for _, text := range hit {
		if !LooksLikeUsernamePrompt(text) {
			t.Fatalf("用户名提示应被识别：%q", text)
		}
	}
	miss := []string{
		"MA5800-X7>", // 命令提示符
		"MA5800-X7#",
		"display board 0\r\n  ok",         // 普通回显
		"Password:",                       // 口令提示不是用户名提示
		"Last login: 2026-09-22 15:50:00", // 登录横幅（"login:" 出现在行中间）
		"Last login",
	}
	for _, text := range miss {
		if LooksLikeUsernamePrompt(text) {
			t.Fatalf("非用户名提示不应被识别：%q", text)
		}
	}
}

// TestLooksLikePasswordPrompt 覆盖口令提示的识别口径：
// 带冒号（含空格排版）命中；欢迎语里只出现裸词 password 不命中。
func TestLooksLikePasswordPrompt(t *testing.T) {
	hit := []string{
		">>User password:", // Huawei OLT 真机
		"Password:",
		"password:",
		"Password :",
		"Enter password:",
	}
	for _, text := range hit {
		if !LooksLikePasswordPrompt(text) {
			t.Fatalf("口令提示应被识别：%q", text)
		}
	}
	miss := []string{
		"Warning: The initial password poses security risks.", // 欢迎语里的裸词
		"Username:",
		"MA5800-X7>",
		"",
	}
	for _, text := range miss {
		if LooksLikePasswordPrompt(text) {
			t.Fatalf("非口令提示不应被识别：%q", text)
		}
	}
}
