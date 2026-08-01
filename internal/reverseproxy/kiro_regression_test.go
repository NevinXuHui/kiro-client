package reverseproxy

// 回归验证（接入适配阶段）：新旧链路表现对照 + 灰度决策。
// 运行：go test ./internal/reverseproxy/ -run TestRegression -v

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// ===== 工具：构造 AWS EventStream 二进制帧（解析器不校验 CRC，占位即可） =====

func kiroTestFrame(eventType string, payload string) []byte {
	name := []byte(":event-type")
	val := []byte(eventType)
	header := []byte{byte(len(name))}
	header = append(header, name...)
	header = append(header, 7) // string header type
	header = append(header, byte(len(val)>>8), byte(len(val)))
	header = append(header, val...)

	headersLen := len(header)
	totalLen := 12 + headersLen + len(payload) + 4

	frame := make([]byte, 0, totalLen)
	var b4 [4]byte
	binary.BigEndian.PutUint32(b4[:], uint32(totalLen))
	frame = append(frame, b4[:]...)
	binary.BigEndian.PutUint32(b4[:], uint32(headersLen))
	frame = append(frame, b4[:]...)
	frame = append(frame, 0, 0, 0, 0) // prelude CRC（占位）
	frame = append(frame, header...)
	frame = append(frame, payload...)
	frame = append(frame, 0, 0, 0, 0) // message CRC（占位）
	return frame
}

// ===== 场景 1：流式对话全场景（内容/推理/工具/用量/结束） =====

func TestRegressionStreamingDialogue(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	stream.Write(kiroTestFrame("assistantResponseEvent", `{"content":"Hello"}`))
	stream.Write(kiroTestFrame("reasoningContentEvent", `{"reasoningContentEvent":{"content":"think"}}`))
	stream.Write(kiroTestFrame("toolUseEvent", `{"toolUseId":"tu1","name":"get_weather","input":{"q":"x"}}`))
	stream.Write(kiroTestFrame("metricsEvent", `{"metricsEvent":{"inputTokens":10,"outputTokens":5}}`))
	stream.Write(kiroTestFrame("contextUsageEvent", `{"contextUsagePercentage":10}`))
	stream.Write(kiroTestFrame("meteringEvent", `{}`))
	stream.Write(kiroTestFrame("messageStopEvent", `{}`))

	chunks := make(chan SSEEvent, 16)
	go parseEventStream(io.NopCloser(stream), chunks, "claude-sonnet-4.5")

	var got []SSEEvent
	for c := range chunks {
		got = append(got, c)
	}
	if len(got) != 6 {
		t.Fatalf("chunk 数=%d, want 6: %+v", len(got), got)
	}

	// 1. assistant 内容 + 首块 role
	c0 := got[0]
	if c0.Choices[0].Delta.Content != "Hello" || c0.Choices[0].Delta.Role != "assistant" {
		t.Errorf("c0=%+v", c0.Choices[0])
	}
	// 2. reasoning_content
	if got[1].Choices[0].Delta.ReasoningContent != "think" {
		t.Errorf("c1=%+v", got[1].Choices[0])
	}
	// 3. 工具起始块（index 0 + name + 空 arguments）
	tc0 := got[2].Choices[0].Delta.ToolCalls[0]
	if tc0.Index != 0 || tc0.ID != "tu1" || tc0.Function.Name != "get_weather" {
		t.Errorf("c2 tool start=%+v", got[2].Choices[0])
	}
	// 4. 工具参数块（index 稳定 0）
	tc1 := got[3].Choices[0].Delta.ToolCalls[0]
	if tc1.Index != 0 || tc1.Function.Arguments != `{"q":"x"}` {
		t.Errorf("c3 tool args=%+v", got[3].Choices[0])
	}
	// 5. metering+context 双到 → 终块（usage 附上，finish=tool_calls）
	c4 := got[4]
	if c4.Choices[0].FinishReason == nil || *c4.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("c4 finish=%v", c4.Choices[0].FinishReason)
	}
	if c4.Usage == nil || c4.Usage.PromptTokens != 10 || c4.Usage.CompletionTokens != 5 || c4.Usage.TotalTokens != 15 {
		t.Errorf("c4 usage=%+v", c4.Usage)
	}
	// 6. messageStop（JS 无 finishEmitted 守卫）→ 再发 finish，无 usage
	c5 := got[5]
	if c5.Choices[0].FinishReason == nil || *c5.Choices[0].FinishReason != "tool_calls" || c5.Usage != nil {
		t.Errorf("c5=%+v", c5)
	}
}

// ===== 场景 2：请求翻译核心字段（新链路 1:1，legacy 已删除） =====

func TestRegressionTranslationParity(t *testing.T) {
	ClearKiroSessionReplayStore()
	ClearSessionStore()

	req := &ChatRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 100, // 按 Bug① 忽略 → 32000
		Messages: []ChatMessage{
			{Role: "user", Content: json.RawMessage(`"hi"`)},
		},
	}
	cred := &KiroCredential{
		ConnectionId: "c1",
		ProviderSpecificData: KiroProviderSpecificData{
			AuthMethod: "builder-id",
			Region:     "us-east-1",
		},
	}
	tr, err := OpenAIToKiroRequest("claude-sonnet-4.5", ChatRequestToBody(req), &KiroTranslatorCredentials{
		ConnectionID:         "c1",
		ProviderSpecificData: cred.ProviderSpecificData,
	})
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	nw := tr.Payload

	if nw.AgentMode != "vibe" {
		t.Errorf("agentMode=%q", nw.AgentMode)
	}
	if nw.ConversationState.ChatTriggerType != "MANUAL" {
		t.Errorf("chatTriggerType=%q", nw.ConversationState.ChatTriggerType)
	}
	if nw.InferenceConfig == nil || nw.InferenceConfig.MaxTokens != 32000 {
		t.Errorf("Bug① maxTokens=%+v", nw.InferenceConfig)
	}
	nc := nw.ConversationState.CurrentMessage.UserInputMessage
	if nc.ModelId != "claude-sonnet-4.5" || nc.Origin != "AI_EDITOR" {
		t.Errorf("modelId/origin=%+v", nc)
	}
	if !strings.Contains(nc.Content, "hi") {
		t.Errorf("current content=%q", nc.Content)
	}
	// builder-id → 默认 ARN
	if nw.ProfileArn != KiroDefaultProfileARNs["builder-id"] {
		t.Errorf("profileArn=%q", nw.ProfileArn)
	}
}

// ===== 场景 3：限流冷却一致性（legacy 已删除，MarkUnavailable 与网关新链路
// 共用同一分类器 KiroCheckFallbackError → 全量一致） =====

func TestRegressionCooldownParity(t *testing.T) {
	pool := &AccountPool{}

	cases := []struct {
		name   string
		status int
		text   string
		wantMS int64 // 统一分类器冷却（ms）
	}{
		{"429 状态", 429, "upstream error", 2000},            // 退避 level1
		{"401 状态", 401, "unauthorized", 120000},            // 固定 2min
		{"500 未匹配", 500, "boom", 30000},                    // 瞬态 30s
		{"限流文本", 0, "rate limit exceeded", 2000},           // 文本→退避
		{"配额文本", 0, "quota exceeded for model", 2000},      // 文本→退避
		{"403+限流文本", 403, "quota exceeded", 2000},          // 文本优先于状态
		{"no credentials 文本", 0, "no credentials", 120000}, // ERROR_RULES 文本规则
		{"request not allowed", 0, "request not allowed", 5000},
	}
	for _, c := range cases {
		acc := &Account{modelLocks: map[string]int64{}}
		poolDur := pool.MarkUnavailable(acc, "m", c.status, c.text)
		res := KiroCheckFallbackError(c.status, c.text, 0)

		if int64(poolDur/time.Millisecond) != c.wantMS || res.CooldownMs != c.wantMS {
			t.Errorf("%s: MarkUnavailable=%dms KiroCheckFallbackError=%dms (期望 %d)",
				c.name, int64(poolDur/time.Millisecond), res.CooldownMs, c.wantMS)
		}
	}
	// ERROR_RULES 全量文本规则生效（legacy 四类文本时代的差异已随整合消失）
	if KiroCheckFallbackError(0, "no credentials", 0).CooldownMs != 120000 {
		t.Errorf("no credentials 应为 120000（ERROR_RULES 文本规则）")
	}
}

// ===== 场景 4：鉴权轮换（令牌确保失败 → 冷却 → 下一账号） =====

func TestRegressionAuthRotation(t *testing.T) {
	now := time.Now()

	// 账号 1：令牌过期且无法刷新 → 标记错误
	acc1 := &Account{Email: "a1", RefreshToken: "", modelLocks: map[string]int64{}}
	cred1 := KiroCredentialFromAccount(acc1)
	cred1.ExpiresAtMs = now.UnixMilli() - 1000 // 已过期
	if err := KiroEnsureAccessToken(cred1, ""); err == nil {
		t.Errorf("过期且无 refreshToken 应刷新失败")
	}
	KiroApplyGatewayError(acc1, "claude-sonnet-4.5", 401, "token refresh failed", now)
	if acc1.modelLocks["claude-sonnet-4.5"] == 0 {
		t.Errorf("账号1 未上锁")
	}

	// 轮换：把网关账号映射为 fallback 账号做可用性过滤
	fa1 := &KiroFallbackAccount{ID: "a1", RateLimitedUntil: now.Add(time.Minute)}
	fa2 := &KiroFallbackAccount{ID: "a2"}
	available := KiroFilterAvailableAccounts([]*KiroFallbackAccount{fa1, fa2}, "", now)
	if len(available) != 1 || available[0].ID != "a2" {
		t.Errorf("轮换应只剩账号2: %+v", available)
	}
	if !KiroIsAccountUnavailable(fa1.RateLimitedUntil, now) {
		t.Errorf("账号1 应处于冷却")
	}
	// 冷却到期后恢复
	after := KiroIsAccountUnavailable(fa1.RateLimitedUntil, now.Add(2*time.Minute))
	if after {
		t.Errorf("冷却到期应恢复")
	}
	// 令牌刷新去重：同 refreshToken 并发仅一次（新链路）
	kiroRefreshDedupMu.Lock()
	kiroRefreshDedupMap = map[string]*kiroRefreshDedupEntry{}
	kiroRefreshDedupMu.Unlock()
	calls := 0
	kiroDedupRefresh("kiro:rt", func() (*KiroRefreshResult, error) {
		calls++
		return &KiroRefreshResult{AccessToken: "at2", ExpiresIn: 3600}, nil
	})
	kiroDedupRefresh("kiro:rt", func() (*KiroRefreshResult, error) {
		calls++
		return &KiroRefreshResult{AccessToken: "at3", ExpiresIn: 3600}, nil
	})
	if calls != 1 {
		t.Errorf("刷新去重失败: calls=%d", calls)
	}
}

// ===== 场景 5：工具调用全流程（结构化翻译 + 事件流 + kiro→claude 块） =====

func TestRegressionToolCalls(t *testing.T) {
	ClearKiroSessionReplayStore()
	// 结构化翻译
	body := kiroTestBody(
		kiroU("weather?"),
		map[string]interface{}{
			"role": "assistant",
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id":   "call_1",
					"type": "function",
					"function": map[string]interface{}{
						"name":      "get_weather",
						"arguments": `{"city":"tokyo"}`,
					},
				},
			},
		},
	)
	body["tools"] = []interface{}{
		map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "get_weather"}},
	}
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	var toolUses []cwToolUse
	for _, h := range req.Payload.ConversationState.History {
		if h.AssistantResponseMessage != nil {
			toolUses = h.AssistantResponseMessage.ToolUses
		}
	}
	if len(toolUses) != 1 || toolUses[0].Name != "get_weather" {
		t.Fatalf("toolUses=%+v", toolUses)
	}
	input, ok := toolUses[0].Input.(map[string]interface{})
	if !ok || input["city"] != "tokyo" {
		t.Errorf("tool input=%#v", toolUses[0].Input)
	}

	// 事件流工具块（沿用场景 1 已验证）+ kiro→claude 工具块
	state := &KiroClaudeRespState{}
	res := KiroToClaudeResponse(buildKiroChunk("chatcmpl-1", 1, "m", SSEDelta{
		ToolCalls: []SSEToolCall{{Index: 0, ID: "tu1", Type: "function", Function: &SSEToolFunction{Name: "get_weather", Arguments: `{"city":"tokyo"}`}}},
	}, nil), state)
	if res == nil {
		t.Fatalf("claude 工具块 nil")
	}
	var sawToolUse bool
	for _, r := range res {
		if stringOf(r["type"]) == "content_block_start" {
			if cb := asMap(r["content_block"]); cb != nil && stringOf(cb["type"]) == "tool_use" {
				sawToolUse = true
				if stringOf(cb["id"]) != "tu1" {
					t.Errorf("tool_use id=%v", cb["id"])
				}
			}
		}
	}
	if !sawToolUse {
		t.Errorf("未找到 tool_use 块: %+v", res)
	}
}

// ===== 场景 6：灰度决策（开关/比例/熔断） =====

func TestRegressionProviderSwitch(t *testing.T) {
	ResetKiroCircuitBreaker()
	SetKiroProviderEnabled(false)
	SetKiroGrayPercent(0)

	// 基线：全 legacy
	if UseNewKiroProvider() {
		t.Errorf("默认应走 legacy")
	}
	// 全局开关
	SetKiroProviderEnabled(true)
	if !UseNewKiroProvider() {
		t.Errorf("开关开启应走新链路")
	}
	// 灰度边界
	SetKiroProviderEnabled(false)
	SetKiroGrayPercent(100)
	if !UseNewKiroProvider() {
		t.Errorf("100%% 应全走新链路")
	}
	SetKiroGrayPercent(0)
	if UseNewKiroProvider() {
		t.Errorf("0%% 应全走 legacy")
	}
	// 灰度随机：50% 两态均出现
	SetKiroGrayPercent(50)
	sawNew, sawLegacy := false, false
	for i := 0; i < 2000; i++ {
		if UseNewKiroProvider() {
			sawNew = true
		} else {
			sawLegacy = true
		}
		if sawNew && sawLegacy {
			break
		}
	}
	if !sawNew || !sawLegacy {
		t.Errorf("50%% 灰度应两态均现: new=%v legacy=%v", sawNew, sawLegacy)
	}

	// 熔断：连续失败达阈值 → 强制 legacy（即使全局开关开启）
	SetKiroGrayPercent(0)
	SetKiroProviderEnabled(true)
	SetKiroFailThreshold(3)
	KiroProviderFailure()
	KiroProviderFailure()
	KiroProviderFailure()
	if UseNewKiroProvider() {
		t.Errorf("熔断期应强制 legacy（否决全局开关）")
	}
	// 成功复位 → 恢复
	KiroProviderSuccess()
	if !UseNewKiroProvider() {
		t.Errorf("成功复位后应恢复新链路")
	}
	// 阈值钳位
	SetKiroFailThreshold(0)
	ResetKiroCircuitBreaker()
	SetKiroProviderEnabled(false)
	SetKiroGrayPercent(0)
	if UseNewKiroProvider() {
		t.Errorf("复位后应回基线")
	}
}
