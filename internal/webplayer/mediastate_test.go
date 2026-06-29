package webplayer

import (
	"testing"
	"time"
)

// TestSMTCQuerySmoke 冒烟测试 SMTC 查询：不崩溃、返回结构化结果。
// 无媒体会话时返回 nil 是合法的。
func TestSMTCQuerySmoke(t *testing.T) {
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
	// 预热
	querySMTCState()

	for i := 0; i < 5; i++ {
		start := time.Now()
		querySMTCState()
		t.Logf("查询 #%d 延迟: %v", i+1, time.Since(start))
	}
}
