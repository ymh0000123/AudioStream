package logx

import (
	"log"
	"strings"
)

var categories = map[string]bool{}

// Init 解析 -log 参数，空串或 "all" 启用全部类别
func Init(flagVal string) {
	flagVal = strings.TrimSpace(flagVal)
	if flagVal == "all" {
		categories["wasapi"] = true
		categories["ffmpeg"] = true
		categories["webplayer"] = true
		categories["media"] = true
		categories["capture"] = true
		return
	}
	for _, c := range strings.Split(flagVal, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			categories[c] = true
		}
	}
}

// Enabled 返回指定类别是否启用
func Enabled(category string) bool {
	return categories[category]
}

// ListCategories 返回所有支持的类别名
func ListCategories() []string {
	return []string{"wasapi", "ffmpeg", "webplayer", "media", "capture"}
}

// Debugf 在类别启用时输出调试日志
func Debugf(category, format string, args ...interface{}) {
	if categories[category] {
		log.Printf("[DEBUG][%s] "+format, append([]interface{}{category}, args...)...)
	}
}
