package reverseproxy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ===== kiro_fallback.go（accountFallback.js） =====

func TestKiroQuotaCooldown(t *testing.T) {
	cases := []struct {
		level int
		want  int64
	}{
		{0, 2000},    // max(0,-1)=0 → 2000*2^0
		{1, 2000},    // max(0,0)=0
		{2, 4000},    // max(0,1)=1 → 2000*2
		{3, 8000},    // 2^2
		{15, 300000}, // 2^14*2000 超上限 → 300000
	}
	for _, c := range cases {
		if got := KiroQuotaCooldown(c.level); got != c.want {
			t.Errorf("KiroQuotaCooldown(%d)=%d, want %d", c.level, got, c.want)
		}
	}
}

func TestKiroCheckFallbackError(t *testing.T) {
	// 文本规则（顺序即优先级）
	if r := KiroCheckFallbackError(200, "no credentials", 0); r.CooldownMs != 120000 || r.HasNewBackoffLevel {
		t.Errorf("no credentials=%+v", r)
	}
	if r := KiroCheckFallbackError(200, "request not allowed", 0); r.CooldownMs != 5000 {
		t.Errorf("request not allowed=%+v", r)
	}
	// 大小写不敏感
	if r := KiroCheckFallbackError(200, "RATE LIMIT exceeded", 0); r.CooldownMs != 2000 || !r.HasNewBackoffLevel || r.NewBackoffLevel != 1 {
		t.Errorf("rate limit=%+v", r)
	}
	// 文本优先于状态：429 + quota exceeded 文本 → 文本规则（level3→newLevel4→16000）
	if r := KiroCheckFallbackError(429, "quota exceeded for model", 3); r.NewBackoffLevel != 4 || r.CooldownMs != 16000 {
		t.Errorf("quota exceeded level4=%+v", r)
	}
	// 状态规则兜底
	if r := KiroCheckFallbackError(401, "random message", 0); r.CooldownMs != 120000 || r.HasNewBackoffLevel {
		t.Errorf("401=%+v", r)
	}
	if r := KiroCheckFallbackError(404, "x", 0); r.CooldownMs != 120000 {
		t.Errorf("404=%+v", r)
	}
	// 429 状态 → 退避级别 1
	if r := KiroCheckFallbackError(429, "x", 0); !r.HasNewBackoffLevel || r.NewBackoffLevel != 1 || r.CooldownMs != 2000 {
		t.Errorf("429=%+v", r)
	}
	// 未匹配 → 瞬态 30s
	if r := KiroCheckFallbackError(500, "boom", 5); r.CooldownMs != 30000 || r.HasNewBackoffLevel {
		t.Errorf("500=%+v", r)
	}
	// 空文案 + 空状态 → 瞬态
	if r := KiroCheckFallbackError(0, "", 0); r.CooldownMs != 30000 {
		t.Errorf("空=%+v", r)
	}
}

func TestKiroFormatRetryAfter(t *testing.T) {
	now := time.Now()
	if got := KiroFormatRetryAfter(time.Time{}, now); got != "" {
		t.Errorf("空=%q", got)
	}
	if got := KiroFormatRetryAfter(now.Add(150*time.Second), now); got != "reset after 2m 30s" {
		t.Errorf("150s=%q", got)
	}
	if got := KiroFormatRetryAfter(now.Add(30*time.Second), now); got != "reset after 30s" {
		t.Errorf("30s=%q", got)
	}
	if got := KiroFormatRetryAfter(now.Add(-5*time.Second), now); got != "reset after 0s" {
		t.Errorf("已过期=%q", got)
	}
	if got := KiroFormatRetryAfter(now.Add(3700*time.Second), now); got != "reset after 1h 1m 40s" {
		t.Errorf("3700s=%q", got)
	}
}

func TestKiroModelLocks(t *testing.T) {
	now := time.Now()
	if KiroGetModelLockKey("m1") != "modelLock_m1" || KiroGetModelLockKey("") != "modelLock___all" {
		t.Errorf("lock key 错误")
	}
	locks := map[string]time.Time{
		"modelLock_m1": now.Add(time.Minute),
		"modelLock_m2": now.Add(-time.Minute),
	}
	if !KiroIsModelLockActive(locks, "m1", now) {
		t.Errorf("m1 锁应生效")
	}
	if KiroIsModelLockActive(locks, "m2", now) {
		t.Errorf("m2 锁应过期")
	}
	if KiroIsModelLockActive(locks, "m3", now) {
		t.Errorf("m3 无锁")
	}
	// 特定键优先于 MODEL_LOCK_ALL
	locks2 := map[string]time.Time{
		"modelLock_m1":    now.Add(-time.Minute),
		"modelLock___all": now.Add(time.Minute),
	}
	if KiroIsModelLockActive(locks2, "m1", now) {
		t.Errorf("特定键已过期应失效（不读 all 兜底）")
	}
	if !KiroIsModelLockActive(locks2, "m9", now) {
		t.Errorf("无特定键应读 all 兜底")
	}
	earliest, found := KiroGetEarliestModelLockUntil(locks, now)
	if !found || earliest.Sub(now) < 30*time.Second {
		t.Errorf("earliest=%v found=%v", earliest, found)
	}
	upd := KiroBuildModelLockUpdate("m1", 5000, now)
	if upd["modelLock_m1"].Sub(now) != 5*time.Second {
		t.Errorf("update=%+v", upd)
	}
	cleared := KiroBuildClearModelLocksUpdate(locks)
	if _, ok := cleared["modelLock_m1"]; !ok {
		t.Errorf("clear 缺失 modelLock_m1: %+v", cleared)
	}
	if _, ok := cleared["other"]; ok {
		t.Errorf("clear 不应含非锁键")
	}
}

func TestKiroApplyErrorState(t *testing.T) {
	now := time.Now()
	acc := &KiroFallbackAccount{ID: "a1", Status: "active"}
	out := KiroApplyErrorState(acc, 429, "rate limit exceeded", now)
	if out.Status != "error" || out.BackoffLevel != 1 || out.RateLimitedUntil.IsZero() {
		t.Errorf("429 错误态=%+v", out)
	}
	if out.LastError == nil || out.LastError.Status != 429 || out.LastError.Message != "rate limit exceeded" {
		t.Errorf("lastError=%+v", out.LastError)
	}
	// 固定规则：退避级别不递增
	acc2 := &KiroFallbackAccount{BackoffLevel: 7}
	KiroApplyErrorState(acc2, 401, "unauthorized", now)
	if acc2.BackoffLevel != 7 {
		t.Errorf("401 不应递增退避级别: %+v", acc2)
	}
	// 成功重置
	KiroResetAccountState(acc)
	if acc.Status != "active" || acc.BackoffLevel != 0 || !acc.RateLimitedUntil.IsZero() || acc.LastError != nil {
		t.Errorf("重置失败=%+v", acc)
	}
	// 过滤
	acc3 := &KiroFallbackAccount{ID: "a3", RateLimitedUntil: now.Add(time.Minute)}
	acc4 := &KiroFallbackAccount{ID: "a4"}
	filtered := KiroFilterAvailableAccounts([]*KiroFallbackAccount{acc, acc3, acc4}, "", now)
	if len(filtered) != 2 {
		t.Errorf("过滤结果=%d", len(filtered))
	}
	filtered2 := KiroFilterAvailableAccounts([]*KiroFallbackAccount{acc4}, "a4", now)
	if len(filtered2) != 0 {
		t.Errorf("excludeId 失效")
	}
}

// ===== kiro_models.go（kiroModels.js） =====

func TestKiroRegionFromProfileArn(t *testing.T) {
	if got := kiroRegionFromProfileArn(""); got != "us-east-1" {
		t.Errorf("空=%s", got)
	}
	if got := kiroRegionFromProfileArn("arn:aws:codewhisperer:eu-central-1:123:profile/p"); got != "eu-central-1" {
		t.Errorf("arn=%s", got)
	}
	if got := kiroRegionFromProfileArn("no-colons"); got != "us-east-1" {
		t.Errorf("畸形=%s", got)
	}
}

func TestKiroBuildVariants(t *testing.T) {
	vs := kiroBuildVariants("claude-sonnet-4.5", "Kiro claude-sonnet-4.5")
	if len(vs) != 4 {
		t.Fatalf("变体数=%d", len(vs))
	}
	ids := []string{}
	for _, v := range vs {
		ids = append(ids, v.ID)
	}
	want := "claude-sonnet-4.5,claude-sonnet-4.5-thinking,claude-sonnet-4.5-agentic,claude-sonnet-4.5-thinking-agentic"
	if strings.Join(ids, ",") != want {
		t.Errorf("ids=%v", ids)
	}
	if vs[3].Name != "Kiro claude-sonnet-4.5 (Thinking + Agentic)" {
		t.Errorf("name=%q", vs[3].Name)
	}
	// auto → 仅 2 变体
	autos := kiroBuildVariants("auto", "Kiro auto")
	if len(autos) != 2 {
		t.Errorf("auto 变体数=%d", len(autos))
	}
}

func TestKiroFormatDisplayName(t *testing.T) {
	if got := kiroFormatDisplayName("sonnet", "sonnet", float64(1.0)); got != "Kiro sonnet" {
		t.Errorf("rate1=%q", got)
	}
	if got := kiroFormatDisplayName("sonnet", "sonnet", float64(2.4)); got != "Kiro sonnet (2.4x credit)" {
		t.Errorf("rate2.4=%q", got)
	}
	if got := kiroFormatDisplayName("", "m1", "bad"); got != "Kiro m1" {
		t.Errorf("坏 rate=%q", got)
	}
}

func TestKiroFingerprintHeaders(t *testing.T) {
	c := &KiroCredential{AccessToken: "at", RefreshToken: "rt"}
	h1 := BuildKiroFingerprintHeaders(c)
	ua := h1["User-Agent"]
	if !strings.Contains(ua, "aws-sdk-js/1.0.0") || !strings.Contains(ua, "os/windows#10.0.26200") ||
		!strings.Contains(ua, "md/nodejs#22.21.1") || !strings.Contains(ua, "KiroIDE-0.10.32-") {
		t.Errorf("UA=%q", ua)
	}
	if h1["x-amzn-kiro-agent-mode"] != "vibe" || h1["x-amzn-codewhisperer-optout"] != "true" ||
		h1["amz-sdk-request"] != "attempt=1; max=1" || h1["Accept"] != "application/json" {
		t.Errorf("headers=%+v", h1)
	}
	// 同凭证 machineId 稳定
	h2 := BuildKiroFingerprintHeaders(c)
	if h1["User-Agent"] != h2["User-Agent"] {
		t.Errorf("machineId 应稳定")
	}
	// 种子优先级：psd.clientId > refreshToken > profileArn > accessToken > 匿名
	c2 := &KiroCredential{AccessToken: "at", ProviderSpecificData: KiroProviderSpecificData{ClientId: "cid"}}
	if BuildKiroFingerprintHeaders(c2)["User-Agent"] == h1["User-Agent"] {
		t.Errorf("clientId 种子应改变 machineId")
	}
	if kiroCatalogCacheKey(c) != kiroCatalogCacheKey(c) {
		t.Errorf("缓存键应稳定")
	}
	if kiroCatalogCacheKey(&KiroCredential{}) == kiroCatalogCacheKey(&KiroCredential{AccessToken: "x"}) {
		t.Errorf("缓存键应区分凭证")
	}
}

func TestResolveKiroModelsFailOpen(t *testing.T) {
	ClearKiroModelCache()
	// 无 accessToken → nil（fail-open）
	if got := ResolveKiroModels(&KiroCredential{}, "", false, nil); got != nil {
		t.Errorf("无 token 应返回 nil")
	}
	if got := ResolveKiroModels(nil, "", false, nil); got != nil {
		t.Errorf("nil 凭证应返回 nil")
	}
	// 有 token 但网络不可达（短超时）→ nil（fail-open，不崩溃）
	bad := &KiroCredential{AccessToken: "at", RefreshToken: "rt", ProviderSpecificData: KiroProviderSpecificData{ProfileArn: "arn:aws:codewhisperer:us-east-1:1:profile/p"}}
	if got := ResolveKiroModels(bad, "", false, nil); got != nil {
		t.Errorf("网络失败应返回 nil（fail-open）")
	}
}

// ===== kiro.go 事件处理（KiroExecutor 对齐） =====

func kiroTestState() *eventStreamState {
	return &eventStreamState{
		seenToolIDs:   map[string]int{},
		contextWindow: 200000,
	}
}

func drainChunks(t *testing.T, n int, chunks chan SSEEvent) []SSEEvent {
	t.Helper()
	var out []SSEEvent
	for i := 0; i < n; i++ {
		select {
		case c := <-chunks:
			out = append(out, c)
		case <-time.After(time.Second):
			t.Fatalf("chunk 缺失: got %d want %d", len(out), n)
		}
	}
	return out
}

func TestProcessAssistantThinkingStrip(t *testing.T) {
	st := kiroTestState()
	chunks := make(chan SSEEvent, 8)
	// 同 chunk 完整 thinking 块：before + after（首换行剥除）
	processAssistantResponse(st, json.RawMessage(`{"content":"before<thinking>hidden</thinking>\nafter"}`), chunks)
	got := drainChunks(t, 1, chunks)[0]
	if got.Choices[0].Delta.Content != "beforeafter" {
		t.Errorf("同块剥离=%q", got.Choices[0].Delta.Content)
	}
	if got.Choices[0].Delta.Role != "assistant" {
		t.Errorf("首块应有 role")
	}

	// 跨块：开标签后内容丢弃；reasoning 已发时空块跳过
	st2 := kiroTestState()
	st2.hasReasoningContent = true
	chunks2 := make(chan SSEEvent, 8)
	processAssistantResponse(st2, json.RawMessage(`{"content":"<thinking>drop me"}`), chunks2)
	if len(chunks2) != 0 {
		t.Errorf("reasoning 已发时应跳过空块")
	}
	processAssistantResponse(st2, json.RawMessage(`{"content":"more</thinking>\nafter"}`), chunks2)
	got2 := drainChunks(t, 1, chunks2)[0]
	if got2.Choices[0].Delta.Content != "after" {
		t.Errorf("跨块剥离=%q", got2.Choices[0].Delta.Content)
	}
}

func TestProcessReasoningWrapper(t *testing.T) {
	st := kiroTestState()
	chunks := make(chan SSEEvent, 8)
	// reasoningContentEvent 包裹 + 字符串形态
	processReasoningContent(st, json.RawMessage(`{"reasoningContentEvent":{"content":"r1"}}`), chunks)
	processReasoningContent(st, json.RawMessage(`"r2"`), chunks)
	got := drainChunks(t, 2, chunks)
	if got[0].Choices[0].Delta.ReasoningContent != "r1" || got[0].Choices[0].Delta.Role != "assistant" {
		t.Errorf("wrapper=%+v", got[0].Choices[0])
	}
	if got[1].Choices[0].Delta.ReasoningContent != "r2" || got[1].Choices[0].Delta.Role != "" {
		t.Errorf("字符串=%+v", got[1].Choices[0])
	}
	if st.reasoningChunkCount != 2 || !st.hasReasoningContent {
		t.Errorf("计数=%d", st.reasoningChunkCount)
	}
}

func TestProcessToolUseIndexStable(t *testing.T) {
	st := kiroTestState()
	chunks := make(chan SSEEvent, 8)
	// 两个工具（数组载荷）→ 两个起始块，index 0/1
	processToolUse(st, json.RawMessage(`[
		{"toolUseId":"tu1","name":"a","input":{"x":1}},
		{"toolUseId":"tu2","name":"b","input":{"y":2}}
	]`), chunks)
	got := drainChunks(t, 4, chunks)
	if got[0].Choices[0].Delta.ToolCalls[0].Index != 0 || got[0].Choices[0].Delta.ToolCalls[0].ID != "tu1" {
		t.Errorf("tu1 start=%+v", got[0].Choices[0])
	}
	if got[2].Choices[0].Delta.ToolCalls[0].Index != 1 || got[2].Choices[0].Delta.ToolCalls[0].ID != "tu2" {
		t.Errorf("tu2 start=%+v", got[2].Choices[0])
	}
	// tu1 参数块：index 稳定为 0（旧实现会漂移为 1）
	if got[1].Choices[0].Delta.ToolCalls[0].Index != 0 || got[1].Choices[0].Delta.ToolCalls[0].Function.Arguments != `{"x":1}` {
		t.Errorf("tu1 args=%+v", got[1].Choices[0])
	}
	if got[3].Choices[0].Delta.ToolCalls[0].Index != 1 || got[3].Choices[0].Delta.ToolCalls[0].Function.Arguments != `{"y":2}` {
		t.Errorf("tu2 args=%+v", got[3].Choices[0])
	}
	// 已有工具再次到来 → 仅参数块（无起始块）
	chunks2 := make(chan SSEEvent, 8)
	processToolUse(st, json.RawMessage(`{"toolUseId":"tu1","name":"a","input":"plain"}`), chunks2)
	got2 := drainChunks(t, 1, chunks2)[0]
	tc := got2.Choices[0].Delta.ToolCalls[0]
	if tc.Index != 0 || tc.ID != "" || tc.Function.Arguments != "plain" {
		t.Errorf("重复 tu1=%+v", got2.Choices[0])
	}
	// 数字输入 → 跳过（JS continue）
	chunks3 := make(chan SSEEvent, 8)
	processToolUse(st, json.RawMessage(`{"toolUseId":"tu9","name":"c","input":42}`), chunks3)
	got3 := drainChunks(t, 1, chunks3)[0]
	if got3.Choices[0].Delta.ToolCalls[0].ID != "tu9" {
		t.Errorf("数字输入应只发起始块: %+v", got3.Choices[0])
	}
	if len(chunks3) != 0 {
		t.Errorf("数字输入不应发参数块")
	}
}

func TestProcessMetricsAndFinish(t *testing.T) {
	st := kiroTestState()
	// 0/0 → 不设 usage
	processMetrics(st, json.RawMessage(`{"metricsEvent":{"inputTokens":0,"outputTokens":0}}`))
	if st.usage != nil {
		t.Errorf("0/0 不应设 usage")
	}
	// 有值 → usage + snake cache 字段
	processMetrics(st, json.RawMessage(`{"metricsEvent":{"inputTokens":10,"outputTokens":5,"cache_read_input_tokens":3}}`))
	if st.usage == nil || st.usage.PromptTokens != 10 || st.usage.CompletionTokens != 5 || st.usage.TotalTokens != 15 {
		t.Errorf("usage=%+v", st.usage)
	}
	if st.usage.CacheReadInputTokens == nil || *st.usage.CacheReadInputTokens != 3 {
		t.Errorf("cache 字段缺失: %+v", st.usage)
	}

	// metering + contextUsage 双到 → 估算终块
	st2 := kiroTestState()
	st2.totalContentLength = 100
	chunks := make(chan SSEEvent, 8)
	processContextUsage(st2, json.RawMessage(`{"contextUsagePercentage":20.0}`), chunks)
	processMetering(st2, chunks)
	got := drainChunks(t, 1, chunks)[0]
	if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "stop" {
		t.Errorf("finish=%v", got.Choices[0].FinishReason)
	}
	// 估算：output = max(1, floor(100/4))=25；input = floor(20*200000/100)=40000
	if got.Usage == nil || got.Usage.PromptTokens != 40000 || got.Usage.CompletionTokens != 25 || got.Usage.TotalTokens != 40025 {
		t.Errorf("估算 usage=%+v", got.Usage)
	}
	// 单事件不到 → 不发
	st3 := kiroTestState()
	chunks3 := make(chan SSEEvent, 8)
	processMetering(st3, chunks3)
	if len(chunks3) != 0 {
		t.Errorf("仅 metering 不应发终块")
	}

	// messageStop：工具后 → tool_calls；无 usage 附带
	st4 := kiroTestState()
	st4.hasToolCalls = true
	chunks4 := make(chan SSEEvent, 8)
	processMessageStop(st4, chunks4)
	got4 := drainChunks(t, 1, chunks4)[0]
	if got4.Choices[0].FinishReason == nil || *got4.Choices[0].FinishReason != "tool_calls" || got4.Usage != nil {
		t.Errorf("messageStop=%+v", got4)
	}
	if !st4.finishEmitted {
		t.Errorf("finishEmitted 未置位")
	}
}
