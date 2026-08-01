package reverseproxy

import "testing"

// 阶段 2 验收：kiro_const.go / kiro_err.go 与 9router JS 原版数值、顺序、分支对齐。

func TestStage2ConstantsAlignJS(t *testing.T) {
	// errorConfig.js
	if Backoff.Base != 2000 || Backoff.Max != 300000 || Backoff.MaxLevel != 15 {
		t.Errorf("Backoff=%+v, want {2000 300000 15}", Backoff)
	}
	if TransientCooldownMs != 30000 || MaxRateLimitCooldownMs != 1800000 {
		t.Errorf("cooldowns: transient=%d maxRate=%d", TransientCooldownMs, MaxRateLimitCooldownMs)
	}
	if got := len(ErrorRules); got != 13 {
		t.Fatalf("ErrorRules len=%d, want 13", got)
	}
	// 顺序与数值锁定（ERROR_RULES 自上而下）
	want := []ErrorRule{
		{Text: "no credentials", CooldownMs: 120000},
		{Text: "request not allowed", CooldownMs: 5000},
		{Text: "improperly formed request", CooldownMs: 120000},
		{Text: "rate limit", Backoff: true},
		{Text: "too many requests", Backoff: true},
		{Text: "quota exceeded", Backoff: true},
		{Text: "capacity", Backoff: true},
		{Text: "overloaded", Backoff: true},
		{Status: 401, CooldownMs: 120000},
		{Status: 402, CooldownMs: 120000},
		{Status: 403, CooldownMs: 120000},
		{Status: 404, CooldownMs: 120000},
		{Status: 429, Backoff: true},
	}
	for i, w := range want {
		if ErrorRules[i] != w {
			t.Errorf("ErrorRules[%d]=%+v, want %+v", i, ErrorRules[i], w)
		}
	}
	if len(ErrorTypes) != 11 || len(DefaultErrorMessages) != 11 {
		t.Errorf("ErrorTypes=%d DefaultErrorMessages=%d, want 11/11", len(ErrorTypes), len(DefaultErrorMessages))
	}
	if DefaultErrorMessages[429] != "Rate limit exceeded" || ErrorTypes[429].Type != "rate_limit_error" {
		t.Errorf("429 mapping mismatch: %+v %q", ErrorTypes[429], DefaultErrorMessages[429])
	}

	// runtimeConfig.js
	if DefaultRetryConfig[429] != (RetryEntry{0, 0}) ||
		DefaultRetryConfig[502] != (RetryEntry{3, 3000}) ||
		DefaultRetryConfig[503] != (RetryEntry{3, 2000}) ||
		DefaultRetryConfig[504] != (RetryEntry{2, 3000}) {
		t.Errorf("DefaultRetryConfig=%+v", DefaultRetryConfig)
	}
	if DefaultMaxTokens != 64000 || DefaultMinTokens != 32000 || TokenSaverHeader != "x-9router-token-saver" {
		t.Errorf("token/header consts mismatch")
	}
	if StreamStallTimeoutMs != 360000 || StreamFirstChunkTimeoutMs != 200000 || FetchConnectTimeoutMs != 60000 {
		t.Errorf("timeouts: stall=%d first=%d connect=%d", StreamStallTimeoutMs, StreamFirstChunkTimeoutMs, FetchConnectTimeoutMs)
	}
	if CacheTTLConfig != (CacheTTL{300, 3600}) || MemoryConfigSettings.SessionTtlMs != 7200000 {
		t.Errorf("cache/memory mismatch: %+v %+v", CacheTTLConfig, MemoryConfigSettings)
	}
	if HTTPStatusRateLimited != 429 || HTTPStatusRequestTimeout != 408 || HTTPStatusGatewayTimeout != 504 {
		t.Errorf("HTTP status enum mismatch")
	}

	// kiroConstants.js
	if KiroAgenticSuffix != "-agentic" || KiroThinkingSuffix != "-thinking" || KiroThinkingBudgetDefault != 16000 {
		t.Errorf("suffix/budget consts mismatch")
	}
	if KiroDefaultProfileARN != KiroDefaultProfileARNs["builder-id"] {
		t.Errorf("KiroDefaultProfileARN != builder-id")
	}
	if got := ResolveDefaultProfileArn("github"); got != KiroDefaultProfileARNs["social"] {
		t.Errorf("ResolveDefaultProfileArn(github)=%s, want social", got)
	}
	if got := ResolveDefaultProfileArn("builder-id"); got != KiroDefaultProfileARNs["builder-id"] {
		t.Errorf("ResolveDefaultProfileArn(builder-id)=%s", got)
	}
	// JS .trim()：首尾无空白
	p := KiroAgenticSystemPrompt
	if p[0] == '\n' || p[0] == ' ' || p[len(p)-1] == '\n' || p[len(p)-1] == ' ' {
		t.Errorf("KiroAgenticSystemPrompt 未按 .trim() 处理")
	}
	for _, marker := range []string{"# CRITICAL: CHUNKED WRITE PROTOCOL (MANDATORY)", "MAXIMUM 350 LINES", "Multiple small operations > one large operation."} {
		if !containsStr(p, marker) {
			t.Errorf("KiroAgenticSystemPrompt 缺少原版内容: %q", marker)
		}
	}
}

func TestResolveRetryEntry(t *testing.T) {
	if got := ResolveRetryEntry(nil); got != (RetryEntry{0, 2000}) {
		t.Errorf("nil → %+v, want {0 2000}", got)
	}
	if got := ResolveRetryEntry(3); got != (RetryEntry{3, 2000}) {
		t.Errorf("number → %+v, want {3 2000}", got)
	}
	// 对象：attempts 缺失/为 0 → 0；delayMs 缺失 → 默认 2000
	if got := ResolveRetryEntry(map[string]interface{}{"attempts": float64(5), "delayMs": float64(1500)}); got != (RetryEntry{5, 1500}) {
		t.Errorf("obj → %+v, want {5 1500}", got)
	}
	if got := ResolveRetryEntry(map[string]interface{}{"attempts": float64(2)}); got != (RetryEntry{2, 2000}) {
		t.Errorf("obj no delayMs → %+v, want {2 2000}", got)
	}
	if got := ResolveRetryEntry(map[string]interface{}{"delayMs": float64(999)}); got != (RetryEntry{0, 999}) {
		t.Errorf("obj attempts=0 → %+v, want {0 999}", got)
	}
	if got := ResolveRetryEntry(map[string]interface{}{"attempts": float64(1), "delayMs": nil}); got != (RetryEntry{1, 2000}) {
		t.Errorf("obj delayMs=nil → %+v, want {1 2000}", got)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
