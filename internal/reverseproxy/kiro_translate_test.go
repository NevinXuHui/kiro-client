package reverseproxy

import (
	"strings"
	"testing"
)

// ===== kiro_translate_req.go（openai-to-kiro / claude-to-kiro） =====

func kiroTestCred(authMethod string) *KiroTranslatorCredentials {
	return &KiroTranslatorCredentials{
		RawHeaders:   map[string]string{"x-session-id": "sess-test"},
		ConnectionID: "conn-test",
		ProviderSpecificData: KiroProviderSpecificData{
			AuthMethod: authMethod,
			Region:     "us-east-1",
		},
	}
}

func kiroTestBody(msgs ...interface{}) map[string]interface{} {
	return map[string]interface{}{"messages": msgs}
}

func kiroU(text string) map[string]interface{} {
	return map[string]interface{}{"role": "user", "content": text}
}

func kiroA(text string) map[string]interface{} {
	return map[string]interface{}{"role": "assistant", "content": text}
}

func TestOpenAIToKiroRequestBasic(t *testing.T) {
	ClearKiroSessionReplayStore()
	ClearSessionStore()
	body := kiroTestBody(
		map[string]interface{}{"role": "system", "content": "You are helpful."},
		kiroU("hello"),
	)
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	p := req.Payload
	if req.UpstreamModel != "claude-sonnet-4.5" {
		t.Errorf("UpstreamModel=%q", req.UpstreamModel)
	}
	if p.AgentMode != "vibe" || p.ConversationState.ChatTriggerType != "MANUAL" ||
		p.ConversationState.AgentTaskType != "vibe" {
		t.Errorf("payload 头部=%+v", p)
	}
	cur := p.ConversationState.CurrentMessage.UserInputMessage
	if cur.ModelId != "claude-sonnet-4.5" || cur.Origin != "AI_EDITOR" {
		t.Errorf("currentMessage=%+v", cur)
	}
	// system 与 user 连续同角色 → 合并为单条用户消息并弹出为 currentMessage；
	// system 以 <instructions> 块进入内容（顶层 systemPrompt 仅含 thinking/agentic 前缀）
	if !strings.Contains(cur.Content, "<instructions>") || !strings.Contains(cur.Content, "hello") {
		t.Errorf("current content=%q", cur.Content)
	}
	if p.ConversationState.ConversationID != "sess-test" {
		t.Errorf("conversationId=%q", p.ConversationState.ConversationID)
	}
}

// 遗留 Bug ①：maxTokens 硬编码 32000，忽略客户端 max_tokens
func TestOpenAIToKiroRequestBugMaxTokens(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := kiroTestBody(kiroU("hi"))
	body["max_tokens"] = 100
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	if req.Payload.InferenceConfig == nil || req.Payload.InferenceConfig.MaxTokens != 32000 {
		t.Errorf("InferenceConfig=%+v, want maxTokens 32000（Bug ① 复刻）", req.Payload.InferenceConfig)
	}
	// temperature/topP 透传
	body["temperature"] = 0.7
	body["top_p"] = 0.9
	req2, _ := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if req2.Payload.InferenceConfig == nil ||
		req2.Payload.InferenceConfig.Temperature == nil || *req2.Payload.InferenceConfig.Temperature != 0.7 ||
		req2.Payload.InferenceConfig.TopP == nil || *req2.Payload.InferenceConfig.TopP != 0.9 {
		t.Errorf("temp/topP 透传失败: %+v", req2.Payload.InferenceConfig)
	}
}

// 遗留 Bug ②：http 图片降级为文本 [Image: url]
func TestOpenAIToKiroRequestBugHTTPImage(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := kiroTestBody(map[string]interface{}{
		"role": "user",
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "look"},
			map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "http://evil.example/x.png"}},
		},
	})
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	cur := req.Payload.ConversationState.CurrentMessage.UserInputMessage
	if !strings.Contains(cur.Content, "[Image: http://evil.example/x.png]") {
		t.Errorf("http 图片未降级: %q", cur.Content)
	}
	if len(cur.Images) != 0 {
		t.Errorf("images 应为空: %+v", cur.Images)
	}
}

// 遗留 Bug ③：畸形 tool arguments 经 safeJSONParse 兜底不崩溃。
// 注意：仅当客户端发了 tools 数组才走结构化路径（safeJSONParse 在此触发）；
// 无 tools 时 JS 先拍平为文本，不触发解析。
func TestOpenAIToKiroRequestBugToolArgs(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := kiroTestBody(
		kiroU("what's the weather"),
		map[string]interface{}{
			"role": "assistant",
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id":   "call_1",
					"type": "function",
					"function": map[string]interface{}{
						"name":      "get_weather",
						"arguments": "{not json",
					},
				},
			},
		},
	)
	body["tools"] = []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "get_weather",
				"description": "Get weather",
			},
		},
	}
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest 应不崩溃: %v", err)
	}
	// toolUses 挂在 history 的 assistant 消息上
	var found bool
	for _, h := range req.Payload.ConversationState.History {
		if h.AssistantResponseMessage != nil && len(h.AssistantResponseMessage.ToolUses) > 0 {
			found = true
			tu := h.AssistantResponseMessage.ToolUses[0]
			if tu.Name != "get_weather" {
				t.Errorf("tool name=%q", tu.Name)
			}
			// 畸形参数 → {} 兜底
			if m, ok := tu.Input.(map[string]interface{}); !ok || len(m) != 0 {
				t.Errorf("畸形 arguments 应兜底为 {}: %#v", tu.Input)
			}
		}
	}
	if !found {
		t.Errorf("未找到 toolUses")
	}
}

func TestOpenAIToKiroRequestToolsStructured(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := kiroTestBody(kiroU("hi"))
	body["tools"] = []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				},
			},
		},
	}
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	cur := req.Payload.ConversationState.CurrentMessage.UserInputMessage
	if cur.UserInputMessageContext == nil || len(cur.UserInputMessageContext.Tools) != 1 {
		t.Fatalf("tools 未注入 currentMessage: %+v", cur)
	}
	spec := cur.UserInputMessageContext.Tools[0].ToolSpecification
	if spec.Name != "get_weather" || spec.Description != "Get weather" {
		t.Errorf("toolSpec=%+v", spec)
	}
	// schema 归一化：补 required:[]
	if spec.InputSchema == nil {
		t.Fatalf("inputSchema 缺失")
	}
	if _, ok := spec.InputSchema.JSON["required"]; !ok {
		t.Errorf("required 未补: %+v", spec.InputSchema.JSON)
	}
	// history 中的 tools 应已清理
	for _, h := range req.Payload.ConversationState.History {
		if h.UserInputMessage != nil && h.UserInputMessage.UserInputMessageContext != nil &&
			len(h.UserInputMessage.UserInputMessageContext.Tools) > 0 {
			t.Errorf("history 残留 tools")
		}
	}
}

// 无 tools → 工具交互拍平为文本（400-guard）
func TestOpenAIToKiroRequestFlatten(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := kiroTestBody(
		kiroU("hi"),
		map[string]interface{}{
			"role": "assistant",
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "checking"},
				map[string]interface{}{"type": "tool_use", "name": "read_file", "input": map[string]interface{}{"path": "/a"}},
			},
		},
		map[string]interface{}{
			"role":         "tool",
			"tool_call_id": "call_1",
			"content":      "file contents here",
		},
		kiroU("thanks"),
	)
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	// 全库无结构化 toolUses/toolResults
	for _, h := range req.Payload.ConversationState.History {
		if h.AssistantResponseMessage != nil && len(h.AssistantResponseMessage.ToolUses) > 0 {
			t.Errorf("无 tools 时应无 toolUses")
		}
		if h.UserInputMessage != nil && h.UserInputMessage.UserInputMessageContext != nil &&
			len(h.UserInputMessage.UserInputMessageContext.ToolResults) > 0 {
			t.Errorf("无 tools 时应无 toolResults")
		}
	}
	all := req.Payload.ConversationState.CurrentMessage.UserInputMessage.Content
	for _, h := range req.Payload.ConversationState.History {
		if h.UserInputMessage != nil {
			all += h.UserInputMessage.Content
		}
		if h.AssistantResponseMessage != nil {
			all += h.AssistantResponseMessage.Content
		}
	}
	if !strings.Contains(all, "[Tool call: read_file(") || !strings.Contains(all, "[Tool result: file contents here]") {
		t.Errorf("工具交互未拍平: %s", all)
	}
}

// thinking 预算注入 + additionalModelRequestFields
func TestOpenAIToKiroRequestThinking(t *testing.T) {
	ClearKiroSessionReplayStore()
	// reasoning_effort low + claude-sonnet-5 → 1024 + adaptive fields
	body := kiroTestBody(kiroU("hi"))
	body["reasoning_effort"] = "low"
	req, err := OpenAIToKiroRequest("claude-sonnet-5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	if !strings.Contains(req.Payload.SystemPrompt, "<max_thinking_length>1024</max_thinking_length>") {
		t.Errorf("systemPrompt=%q", req.Payload.SystemPrompt)
	}
	if req.Payload.AdditionalModelRequestFields == nil {
		t.Fatalf("additionalModelRequestFields 缺失")
	}
	thinking := asMap(req.Payload.AdditionalModelRequestFields["thinking"])
	oc := asMap(req.Payload.AdditionalModelRequestFields["output_config"])
	if stringOf(thinking["type"]) != "adaptive" || stringOf(thinking["display"]) != "summarized" ||
		stringOf(oc["effort"]) != "low" {
		t.Errorf("fields=%+v", req.Payload.AdditionalModelRequestFields)
	}
	// 单用户消息：无 history user 可放 sessionStart，整个 contentPrefix
	// （systemPrompt + 时间）都进当前消息（JS 语义：nextCurrent = clone(sessionStart)）
	cur := req.Payload.ConversationState.CurrentMessage.UserInputMessage
	if !strings.Contains(cur.Content, "[Context: Current time is ") {
		t.Errorf("current 无时间上下文: %q", cur.Content)
	}
	if !strings.Contains(cur.Content, "<thinking_mode>") {
		t.Errorf("单消息时 thinking 前缀应进当前消息: %q", cur.Content)
	}

	// legacy claude-sonnet-4.5 + effort high → 注入 24576，但无 additionalModelRequestFields
	body2 := kiroTestBody(kiroU("hi"))
	body2["reasoning_effort"] = "high"
	req2, _ := OpenAIToKiroRequest("claude-sonnet-4.5", body2, kiroTestCred("builder-id"))
	if !strings.Contains(req2.Payload.SystemPrompt, "<max_thinking_length>24576</max_thinking_length>") {
		t.Errorf("legacy 模型应注入 24576: %q", req2.Payload.SystemPrompt)
	}
	if req2.Payload.AdditionalModelRequestFields != nil {
		t.Errorf("legacy 4.5 不应有 additionalModelRequestFields: %+v", req2.Payload.AdditionalModelRequestFields)
	}

	// reasoning_effort none → 无注入
	body3 := kiroTestBody(kiroU("hi"))
	body3["reasoning_effort"] = "none"
	req3, _ := OpenAIToKiroRequest("claude-sonnet-5", body3, kiroTestCred("builder-id"))
	if req3.Payload.SystemPrompt != "" || req3.Payload.AdditionalModelRequestFields != nil {
		t.Errorf("none 应无注入: %q %+v", req3.Payload.SystemPrompt, req3.Payload.AdditionalModelRequestFields)
	}
}

func TestOpenAIToKiroRequestProfileArn(t *testing.T) {
	ClearKiroSessionReplayStore()
	// 社交 authMethod → 默认 social ARN
	req, _ := OpenAIToKiroRequest("claude-sonnet-4.5", kiroTestBody(kiroU("hi")), kiroTestCred("google"))
	if req.Payload.ProfileArn != KiroDefaultProfileARNs["social"] {
		t.Errorf("social profileArn=%q", req.Payload.ProfileArn)
	}
	// api_key → 绝不注入默认占位
	req2, _ := OpenAIToKiroRequest("claude-sonnet-4.5", kiroTestBody(kiroU("hi")), kiroTestCred("api_key"))
	if req2.Payload.ProfileArn != "" {
		t.Errorf("api_key 不应有默认 ARN: %q", req2.Payload.ProfileArn)
	}
	// 账号绑定 + 已解析 ARN → 透传
	cred := kiroTestCred("api_key")
	cred.ProviderSpecificData.ProfileArn = "arn:aws:codewhisperer:us-east-1:1:profile/own"
	req3, _ := OpenAIToKiroRequest("claude-sonnet-4.5", kiroTestBody(kiroU("hi")), cred)
	if req3.Payload.ProfileArn != "arn:aws:codewhisperer:us-east-1:1:profile/own" {
		t.Errorf("api_key 已解析 ARN 应透传: %q", req3.Payload.ProfileArn)
	}
}

func TestOpenAIToKiroRequestImages(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := kiroTestBody(map[string]interface{}{
		"role": "user",
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "see"},
			map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/png;base64,AAAA"}},
		},
	})
	req, err := OpenAIToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	cur := req.Payload.ConversationState.CurrentMessage.UserInputMessage
	if len(cur.Images) != 1 || cur.Images[0].Format != "png" || cur.Images[0].Source.Bytes != "AAAA" {
		t.Errorf("images=%+v", cur.Images)
	}
}

// ===== claude-to-kiro =====

func TestClaudeToKiroRequest(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := map[string]interface{}{
		"model":      "claude-sonnet-4.5",
		"max_tokens": 100,
		"system":     "Be concise.",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	req, err := ClaudeToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("ClaudeToKiroRequest: %v", err)
	}
	p := req.Payload
	// claude 尊重 body.max_tokens（与 Bug ① 的 openai 路径不同）
	if p.InferenceConfig == nil || p.InferenceConfig.MaxTokens != 100 {
		t.Errorf("claude maxTokens=%+v", p.InferenceConfig)
	}
	// system → 顶层 systemPrompt（与 openai 的 <instructions> 不同）
	if !strings.Contains(p.SystemPrompt, "Be concise.") {
		t.Errorf("systemPrompt=%q", p.SystemPrompt)
	}
	cur := p.ConversationState.CurrentMessage.UserInputMessage
	if cur.ModelId != "claude-sonnet-4.5" || !strings.Contains(cur.Content, "hi") {
		t.Errorf("current=%+v", cur)
	}
}

// claude guard 1：无 tools → 拍平
func TestClaudeToKiroRequestGuard1(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "do it"},
			map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "tool_use", "id": "tu1", "name": "bash", "input": map[string]interface{}{"cmd": "ls"}},
				},
			},
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "tool_result", "tool_use_id": "tu1", "content": "out"},
				},
			},
		},
	}
	req, err := ClaudeToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("ClaudeToKiroRequest: %v", err)
	}
	all := ""
	for _, h := range req.Payload.ConversationState.History {
		if h.AssistantResponseMessage != nil {
			all += h.AssistantResponseMessage.Content
		}
	}
	all += req.Payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !strings.Contains(all, "[Tool call: bash(") || !strings.Contains(all, "[Tool result: out]") {
		t.Errorf("claude 拍平失败: %s", all)
	}
	// 无结构化引用
	for _, h := range req.Payload.ConversationState.History {
		if h.AssistantResponseMessage != nil && len(h.AssistantResponseMessage.ToolUses) > 0 {
			t.Errorf("guard1 应无 toolUses")
		}
	}
}

// claude guard 2：有 tools + 孤儿 tool_result → 折回文本
func TestClaudeToKiroRequestGuard2(t *testing.T) {
	ClearKiroSessionReplayStore()
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "bash", "input_schema": map[string]interface{}{"type": "object"}},
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "run"},
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					// 孤儿 tool_result：无对应 tool_use
					map[string]interface{}{"type": "tool_result", "tool_use_id": "ghost", "content": "important orphaned output"},
				},
			},
		},
	}
	req, err := ClaudeToKiroRequest("claude-sonnet-4.5", body, kiroTestCred("builder-id"))
	if err != nil {
		t.Fatalf("ClaudeToKiroRequest: %v", err)
	}
	cur := req.Payload.ConversationState.CurrentMessage.UserInputMessage
	if !strings.Contains(cur.Content, "[Tool result: important orphaned output]") {
		t.Errorf("孤儿 tool_result 未折回: %q", cur.Content)
	}
	if cur.UserInputMessageContext != nil && len(cur.UserInputMessageContext.ToolResults) > 0 {
		t.Errorf("孤儿引用应清除")
	}
}

// ===== kiro_translate_resp.go（kiro-to-openai / kiro-to-claude） =====

func TestKiroToOpenAIResponse(t *testing.T) {
	state := &KiroRespStateOpenAI{}
	// 原样透传已 OpenAI 形状
	existing := SSEEvent{Object: "chat.completion.chunk", Choices: []SSEChoice{{}}}
	if got := KiroToOpenAIResponse(existing, state); got == nil {
		t.Errorf("已 OpenAI 形状应原样返回")
	}

	// assistantResponseEvent
	got := KiroToOpenAIResponse(map[string]interface{}{
		"_eventType":             "assistantResponseEvent",
		"assistantResponseEvent": map[string]interface{}{"content": "Hi"},
	}, state)
	ev, ok := got.(SSEEvent)
	if !ok {
		t.Fatalf("类型=%T", got)
	}
	if ev.Choices[0].Delta.Content != "Hi" || ev.Choices[0].Delta.Role != "assistant" {
		t.Errorf("chunk=%+v", ev.Choices[0])
	}

	// reasoningContentEvent
	got2 := KiroToOpenAIResponse(map[string]interface{}{
		"_eventType":            "reasoningContentEvent",
		"reasoningContentEvent": map[string]interface{}{"content": "thinking"},
	}, state)
	ev2 := got2.(SSEEvent)
	if ev2.Choices[0].Delta.ReasoningContent != "thinking" {
		t.Errorf("reasoning chunk=%+v", ev2.Choices[0])
	}

	// toolUseEvent
	got3 := KiroToOpenAIResponse(map[string]interface{}{
		"_eventType": "toolUseEvent",
		"toolUseEvent": map[string]interface{}{
			"toolUseId": "tu1", "name": "get_weather", "input": map[string]interface{}{"q": "x"},
		},
	}, state)
	ev3 := got3.(SSEEvent)
	tc := ev3.Choices[0].Delta.ToolCalls[0]
	if tc.ID != "tu1" || tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"q":"x"}` {
		t.Errorf("tool chunk=%+v", ev3.Choices[0])
	}

	// messageStopEvent（有 tool）→ finish_reason tool_calls
	got4 := KiroToOpenAIResponse(map[string]interface{}{"_eventType": "messageStopEvent"}, state)
	ev4 := got4.(SSEEvent)
	if ev4.Choices[0].FinishReason == nil || *ev4.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish=%v", ev4.Choices[0].FinishReason)
	}

	// usageEvent → state.Usage，无 chunk
	if got5 := KiroToOpenAIResponse(map[string]interface{}{
		"_eventType": "usageEvent",
		"usageEvent": map[string]interface{}{"inputTokens": 10, "outputTokens": 5},
	}, state); got5 != nil {
		t.Errorf("usageEvent 不应产 chunk")
	}
	if state.Usage == nil || state.Usage.PromptTokens != 10 || state.Usage.CompletionTokens != 5 || state.Usage.TotalTokens != 15 {
		t.Errorf("usage=%+v", state.Usage)
	}

	// 未知事件 → nil
	if got6 := KiroToOpenAIResponse(map[string]interface{}{"_eventType": "contextUsageEvent"}, state); got6 != nil {
		t.Errorf("未知事件应跳过")
	}
}

func TestKiroToClaudeResponse(t *testing.T) {
	state := &KiroClaudeRespState{}
	mk := func(content string) SSEEvent {
		return buildKiroChunk("chatcmpl-1", 1, "claude-sonnet-4.5", SSEDelta{Content: content}, nil)
	}

	// 首文本块 → message_start + text block
	res := KiroToClaudeResponse(mk("Hi"), state)
	if res == nil || len(res) != 3 {
		t.Fatalf("首块结果=%+v", res)
	}
	if res[0]["type"] != "message_start" {
		t.Errorf("首事件=%v", res[0]["type"])
	}
	if res[2]["type"] != "content_block_delta" {
		t.Errorf("末事件=%v", res[2]["type"])
	}
	delta := asMap(res[2]["delta"])
	if stringOf(delta["type"]) != "text_delta" || stringOf(delta["text"]) != "Hi" {
		t.Errorf("text delta=%+v", res[2])
	}

	// reasoning → 先关文本块 + thinking 块（JS：stopTextBlock 先发 content_block_stop）
	res2 := KiroToClaudeResponse(buildKiroChunk("chatcmpl-1", 1, "claude-sonnet-4.5", SSEDelta{ReasoningContent: "r"}, nil), state)
	if res2 == nil || len(res2) != 3 {
		t.Fatalf("thinking 块=%+v", res2)
	}
	if res2[0]["type"] != "content_block_stop" {
		t.Errorf("首事件应为文本块关闭: %v", res2[0]["type"])
	}
	if res2[1]["type"] != "content_block_start" {
		t.Errorf("次事件=%v", res2[1]["type"])
	}
	cb := asMap(res2[1]["content_block"])
	if stringOf(cb["type"]) != "thinking" {
		t.Errorf("thinking block=%+v", res2[1])
	}

	// tool_calls 流 → tool_use 块 + 缓冲 arguments
	toolChunk := buildKiroChunk("chatcmpl-1", 1, "claude-sonnet-4.5", SSEDelta{
		ToolCalls: []SSEToolCall{{Index: 0, ID: "tu1", Type: "function", Function: &SSEToolFunction{Name: "get_weather", Arguments: `{"q":"x"}`}}},
	}, nil)
	res3 := KiroToClaudeResponse(toolChunk, state)
	if res3 == nil {
		t.Fatalf("tool 块 nil")
	}
	// 先关 thinking 块（JS：stopThinkingBlock 前置），再开 tool_use 块
	var toolStart map[string]interface{}
	for _, r := range res3 {
		if stringOf(r["type"]) == "content_block_start" {
			if cb2 := asMap(r["content_block"]); cb2 != nil && stringOf(cb2["type"]) == "tool_use" {
				toolStart = r
			}
		}
	}
	if toolStart == nil {
		t.Fatalf("未找到 tool_use start: %+v", res3)
	}
	cb2 := asMap(toolStart["content_block"])
	if stringOf(cb2["id"]) != "tu1" || stringOf(cb2["name"]) != "get_weather" {
		t.Errorf("tool block=%+v", toolStart)
	}

	// finish → input_json_delta(缓冲参数) + stop + message_delta + message_stop
	finishReason := "tool_calls"
	finishChunk := SSEEvent{Object: "chat.completion.chunk", Model: "claude-sonnet-4.5",
		Choices: []SSEChoice{{Index: 0, Delta: SSEDelta{}, FinishReason: &finishReason}}}
	res4 := KiroToClaudeResponse(finishChunk, state)
	if res4 == nil {
		t.Fatalf("finish 块 nil")
	}
	types := []string{}
	var sawInputJSON bool
	for _, r := range res4 {
		types = append(types, stringOf(r["type"]))
		if stringOf(r["type"]) == "content_block_delta" {
			d := asMap(r["delta"])
			if stringOf(d["type"]) == "input_json_delta" && stringOf(d["partial_json"]) == `{"q":"x"}` {
				sawInputJSON = true
			}
		}
	}
	joined := strings.Join(types, ",")
	if !sawInputJSON {
		t.Errorf("input_json_delta 缺失: %s", joined)
	}
	if !strings.Contains(joined, "message_delta") || !strings.Contains(joined, "message_stop") {
		t.Errorf("finish 事件序列=%s", joined)
	}
	// stop_reason tool_use
	for _, r := range res4 {
		if stringOf(r["type"]) == "message_delta" {
			d := asMap(r["delta"])
			if stringOf(d["stop_reason"]) != "tool_use" {
				t.Errorf("stop_reason=%v", d)
			}
		}
	}
}

// ===== kiro_session.go（sessionManager / kiroSessionReplay） =====

func TestResolveSessionIdentityKiro(t *testing.T) {
	ClearSessionStore()
	// 无头请求 → ephemeral 二进制 ID，每次不同
	id1 := ResolveSessionIdentity(nil, kiroTestBody(kiroU("hi")), "c1", "", "kiro")
	id2 := ResolveSessionIdentity(nil, kiroTestBody(kiroU("hi")), "c1", "", "kiro")
	if !id1.Ephemeral || id1.SessionID == "" {
		t.Errorf("kiro 无头应 ephemeral: %+v", id1)
	}
	if id1.SessionID == id2.SessionID {
		t.Errorf("无头会话应每次不同")
	}

	// x-session-id → 会话粘性 + 非 ephemeral
	id3 := ResolveSessionIdentity(map[string]string{"x-session-id": "sess-9"}, kiroTestBody(kiroU("hi")), "c1", "", "kiro")
	if id3.Ephemeral || id3.SessionID != "sess-9" {
		t.Errorf("x-session-id=%+v", id3)
	}

	// kiro scope 跳过 x-client-request-id / metadata.user_id
	id4 := ResolveSessionIdentity(map[string]string{"x-client-request-id": "crid"}, kiroTestBody(kiroU("hi")), "c1", "", "kiro")
	if !id4.Ephemeral {
		t.Errorf("kiro 应跳过 x-client-request-id: %+v", id4)
	}
	bodyWithMeta := kiroTestBody(kiroU("hi"))
	bodyWithMeta["metadata"] = map[string]interface{}{"user_id": "u1"}
	id5 := ResolveSessionIdentity(nil, bodyWithMeta, "c1", "", "kiro")
	if !id5.Ephemeral {
		t.Errorf("kiro 应跳过 metadata.user_id: %+v", id5)
	}
	// 非 kiro scope 则 user_id 生效
	id6 := ResolveSessionIdentity(nil, bodyWithMeta, "c1", "", "other")
	if id6.Ephemeral || id6.SessionID != "u1" {
		t.Errorf("非 kiro 应接受 user_id: %+v", id6)
	}
}

func TestResolveContinuationId(t *testing.T) {
	ClearSessionStore()
	a := ResolveContinuationId("sess-1", "c1", "kiro", false)
	b := ResolveContinuationId("sess-1", "c1", "kiro", false)
	if a != b {
		t.Errorf("同会话续延应稳定: %s vs %s", a, b)
	}
	c := ResolveContinuationId("sess-2", "c1", "kiro", false)
	if a == c {
		t.Errorf("异会话续延应不同")
	}
	// ephemeral → 每次新
	d := ResolveContinuationId("sess-1", "c1", "kiro", true)
	e := ResolveContinuationId("sess-1", "c1", "kiro", true)
	if d == e {
		t.Errorf("ephemeral 续延应每次新")
	}
}

func TestApplyKiroSessionReplay(t *testing.T) {
	ClearKiroSessionReplayStore()
	cred := kiroTestCred("builder-id")
	// 多消息体：history 保留首条 user（msg0 冻结源），currentMessage 为最后 user
	body1 := kiroTestBody(kiroU("hello"), kiroA("ok"), kiroU("second"))
	req1, err := OpenAIToKiroRequest("claude-sonnet-4.5", body1, cred)
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	if len(req1.Payload.ConversationState.History) == 0 {
		t.Fatalf("history 为空")
	}
	frozen := req1.Payload.ConversationState.History[0].UserInputMessage.Content
	if !strings.Contains(frozen, "hello") {
		t.Fatalf("msg0 冻结内容异常: %q", frozen)
	}

	// 第二轮：同会话同 model 同 systemPrompt → 重放 msg0
	body2 := kiroTestBody(kiroU("world"))
	req2, err := OpenAIToKiroRequest("claude-sonnet-4.5", body2, cred)
	if err != nil {
		t.Fatalf("OpenAIToKiroRequest: %v", err)
	}
	replayed := req2.Payload.ConversationState.History[0].UserInputMessage.Content
	if replayed != frozen {
		t.Errorf("msg0 未重放:\nold=%q\nnew=%q", frozen, replayed)
	}
	// 当前轮只含当前时间上下文 + 新内容，不含冻结的 msg0
	cur2 := req2.Payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !strings.Contains(cur2, "world") || strings.Contains(cur2, "hello") {
		t.Errorf("当前轮内容异常: %q", cur2)
	}
	// 续延 ID 稳定
	if req1.Payload.ConversationState.AgentContinuationID != req2.Payload.ConversationState.AgentContinuationID {
		t.Errorf("续延 ID 应稳定")
	}

	// systemPrompt 变化 → 不重放
	body3 := kiroTestBody(kiroU("again"), kiroA("ok2"), kiroU("final"))
	body3["reasoning_effort"] = "high"
	req3, _ := OpenAIToKiroRequest("claude-sonnet-4.5", body3, cred)
	if req3.Payload.ConversationState.History[0].UserInputMessage.Content == frozen {
		t.Errorf("systemPrompt 变化应重新冻结")
	}
}
