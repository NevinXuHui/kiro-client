package reverseproxy

import (
	"testing"
	"time"
)

// TestBackoffCooldown 9router errorConfig 指数退避数学：2000*2^(n-1) ms 上限 300s。
// legacy backoffCooldown 已删除，统一走 KiroQuotaCooldown（kiro_fallback.go）。
func TestBackoffCooldown(t *testing.T) {
	cases := []struct {
		level int
		want  time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{8, 256 * time.Second},
		{9, 300 * time.Second}, // cap 5min
		{15, 300 * time.Second},
	}
	for _, c := range cases {
		if got := KiroQuotaCooldown(c.level); time.Duration(got)*time.Millisecond != c.want {
			t.Errorf("level %d: got %v want %v", c.level, time.Duration(got)*time.Millisecond, c.want)
		}
	}
}

// TestModelLockRotation 429 → 模型锁退避 → Acquire 跳过 → 全锁后 EarliestModelLock
// → MarkSuccess 清零退避级别。
func TestModelLockRotation(t *testing.T) {
	p := NewAccountPool("", 2, 10, nil)
	p.accounts = []*Account{{Email: "a"}, {Email: "b"}}
	p.nextIdx = 0

	acc := p.Acquire("claude-sonnet-4.5")
	if acc == nil || acc.Email != "a" {
		t.Fatalf("acquire first: %v", acc)
	}
	p.Release(acc)

	cd := p.MarkUnavailable(acc, "claude-sonnet-4.5", 429, "HTTP 429 too many requests")
	if cd != 2*time.Second {
		t.Fatalf("first 429 cooldown = %v, want 2s", cd)
	}
	// 同模型应跳过 a，轮到 b
	acc2 := p.Acquire("claude-sonnet-4.5")
	if acc2 == nil || acc2.Email != "b" {
		t.Fatalf("acquire after lock: %v", acc2)
	}
	// 其他模型不受锁影响
	if acc3 := p.Acquire("glm-5"); acc3 == nil {
		t.Fatalf("other model should not be locked")
	}
	p.Release(acc2)

	// 每账号独立退避级别（9router per-connection）：b 首败 2s，再败升级 4s
	cd2 := p.MarkUnavailable(acc2, "claude-sonnet-4.5", 429, "HTTP 429")
	if cd2 != 2*time.Second {
		t.Fatalf("fresh account first 429 cooldown = %v, want 2s", cd2)
	}
	cd2b := p.MarkUnavailable(acc2, "claude-sonnet-4.5", 429, "HTTP 429")
	if cd2b != 4*time.Second {
		t.Fatalf("second 429 cooldown = %v, want 4s", cd2b)
	}
	if e := p.EarliestModelLock("claude-sonnet-4.5"); e <= time.Now().UnixNano() {
		t.Fatalf("earliest lock not in future: %v", e)
	}
	if a := p.Acquire("claude-sonnet-4.5"); a != nil {
		t.Fatalf("both accounts locked, acquire must be nil")
	}

	// 成功清零：b 的锁清掉后退避级别归零，下次 429 从 2s 起
	p.MarkSuccess(acc2, "claude-sonnet-4.5")
	if p.Acquire("claude-sonnet-4.5") == nil {
		t.Fatalf("account should be usable after success")
	}
	// 清掉 a 的锁（时间未到期，直接删）
	p.MarkSuccess(acc, "claude-sonnet-4.5")
	cd3 := p.MarkUnavailable(acc, "claude-sonnet-4.5", 429, "HTTP 429")
	if cd3 != 2*time.Second {
		t.Fatalf("backoff level not reset: got %v want 2s", cd3)
	}
}
