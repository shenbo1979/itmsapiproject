// 由 tools/port_switch_batch2c.py 从 switchapi/rpc_client.go 959-1046 搬运
// （VLAN 区间解析/压缩工具，端口 VLAN 页面使用）。
package switchmod

import (
	"fmt"
	"strconv"
	"strings"
)

func parseRange(s string, exclude int) ([]int, error) {
	var result []int
	parts := strings.Split(s, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// 检查是否是范围（包含 '-'）
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range format: %s", part)
			}

			start, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return nil, err
			}
			end, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return nil, err
			}

			if start > end {
				return nil, fmt.Errorf("invalid range: start %d > end %d", start, end)
			}

			for i := start; i <= end; i++ {
				if i != exclude {
					result = append(result, i)
				}
			}
		} else {
			// 单个数字
			num, err := strconv.Atoi(part)
			if err != nil {
				return nil, err
			}
			if num != exclude {
				result = append(result, num)
			}
		}
	}
	return result, nil
}

func compressRanges(nums []int) string {
	if len(nums) == 0 {
		return ""
	}

	var result []string
	start := nums[0]
	end := nums[0]

	for i := 1; i < len(nums); i++ {
		if nums[i] == end+1 {
			// 连续，更新结束值
			end = nums[i]
		} else {
			// 不连续，输出前一段
			result = append(result, formatRange(start, end))
			start = nums[i]
			end = nums[i]
		}
	}
	// 添加最后一段
	result = append(result, formatRange(start, end))
	// 用逗号连接
	final := ""
	for i, s := range result {
		if i > 0 {
			final += ","
		}
		final += s
	}
	return final
}

func formatRange(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

// 将模版信息保存为文件
