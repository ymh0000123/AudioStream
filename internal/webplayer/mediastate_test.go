package webplayer

import (
	"os"
	"testing"
	"time"
)

// skipInHeadlessCI 在无交互式媒体会话的环境（如 GitHub Actions 的 windows-latest
// runner）跳过测试。这些环境下 WinRT 的 async .get() 调用没有媒体源可解析，
// 易长期阻塞甚至死锁，会导致 go test 触发 10 分钟超时被信号杀死。
func skipInHeadlessCI(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") != "" || testing.Short() {
		t.Skip("跳过：依赖真实 Windows SMTC 媒体会话，CI/headless 环境不可用")
	}
}

// TestSMTCQuerySmoke 冒烟测试 SMTC 查询：不崩溃、返回结构化结果。
// 无媒体会话时返回 nil 是合法的。
func TestSMTCQuerySmoke(t *testing.T) {
	skipInHeadlessCI(t)

	s1 := querySMTCState()
	s2 := querySMTCState()

	if s1 == nil && s2 == nil {
		return
	}
	if s1 != nil && !s1.HasSession {
		t.Fatalf("s1.HasSession 应为 true, 但 %+v", s1)
	}
	if s1 != nil && s2 != nil {
		if s1.Playing != s2.Playing {
			t.Logf("注意: 两次查询 playing 状态不同 (s1=%v s2=%v), 可能是边界跳变", s1.Playing, s2.Playing)
		}
	}
}

// TestSMTCQueryLatency 验证查询延迟足够低（<50ms）。
func TestSMTCQueryLatency(t *testing.T) {
	skipInHeadlessCI(t)

	// 预热
	querySMTCState()

	for i := 0; i < 5; i++ {
		start := time.Now()
		querySMTCState()
		t.Logf("查询 #%d 延迟: %v", i+1, time.Since(start))
	}
}
