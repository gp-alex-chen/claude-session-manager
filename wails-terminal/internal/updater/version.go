package updater

// 本项目的版本号约定：v<major>[.<minor>[.<patch>]][-<预发布后缀>]
// （例如 v0.1、v0.2-wails、v0.2.3-wails-pre）。
// 刻意不引入外部语义版本库：保持零额外依赖，在离线环境也能构建/测试。
// 语义足够覆盖本项目：数字按段比较，后缀（-wails/-pre/-rc）在稳定版本
// 比较中不参与（预发布与否由 GitHub 的 prerelease 标志单独判断）。

import (
	"strconv"
	"strings"
)

// parseNumVersion 把 "vA[.B[.C]][-suffix]" 解析成数字段切片；不合法返回 ok=false。
// 忽略大/小写与前导零（比较时按数值）。
func parseNumVersion(tag string) ([]int, bool) {
	t := strings.TrimSpace(tag)
	if t == "" || t[0] != 'v' {
		return nil, false
	}
	t = t[1:]
	if i := strings.IndexByte(t, '-'); i >= 0 {
		t = t[:i] // 丢弃预发布/构建后缀，稳定版本比较不看它
	}
	if t == "" {
		return nil, false
	}
	parts := strings.Split(t, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, false
		}
		// 只允许纯数字
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil, false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	return nums, true
}

// compareTags 按数字段比较两个版本 tag；缺段视为 0（v0.2 == v0.2.0）。
// 返回 -1 / 0 / 1。
func compareTags(a, b string) int {
	na, oka := parseNumVersion(a)
	nb, okb := parseNumVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	maxLen := len(na)
	if len(nb) > maxLen {
		maxLen = len(nb)
	}
	for i := 0; i < maxLen; i++ {
		var va, vb int
		if i < len(na) {
			va = na[i]
		}
		if i < len(nb) {
			vb = nb[i]
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

// isValidTag 是否为合法版本 tag（用于在筛选时跳过非法项）。
func isValidTag(tag string) bool {
	_, ok := parseNumVersion(tag)
	return ok
}
