package reverseproxy

// 本文件 1:1 迁移自 9router（阶段 5：响应翻译）：
//   - open-sse/translator/response/kiro-to-openai.js（kiroToOpenAIResponse 全量）
//   - open-sse/translator/response/kiro-to-claude.js（kiroToClaudeResponse +
//     kiroToClaudeNonStreaming 全量）
//   - 依赖的 concerns：chunk.js buildChunk、reasoning.js reasoningDelta、
//     finishReason.js toOpenAIFinish（kiro/ollama 分支）、toolCall.js
//     fallbackToolCallId、usage.js toOpenAIUsage（kiro 分支）
//
// 注意：kiro→claude 路径收到的 chunk 是 KiroExecutor 已解析出的 OpenAI 形状
// （chat.completion.chunk），本文件实现 OpenAI chunk → Claude SSE 事件。

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ===== concerns 移植 =====

// buildKiroChunk 构建 OpenAI chat.completion.chunk（concerns/chunk.js buildChunk；
// 调用方决定 id/created/model 语义）。
func buildKiroChunk(id string, created int64, model string, delta SSEDelta, finishReason *string) SSEEvent {
	return SSEEvent{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []SSEChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
	}
}

// reasoningDelta 构建携带 reasoning_content 的 delta（concerns/reasoning.js
// reasoningDelta；可选前置 assistant role）。
func reasoningDelta(text string, withRole bool) SSEDelta {
	if withRole {
		return SSEDelta{Role: "assistant", ReasoningContent: text}
	}
	return SSEDelta{ReasoningContent: text}
}

// toOpenAIFinishKiro 上游 stop 原因 → OpenAI finish_reason（concerns/finishReason.js
// toOpenAIFinish 的 kiro/ollama 分支）。
func toOpenAIFinishKiro(reason string) string {
	switch reason {
	case "tool_calls", "tool_use":
		return "tool_calls"
	case "length", "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

// fallbackToolCallId 流式 tool_call 兜底 ID（concerns/toolCall.js
// fallbackToolCallId：call_{ts}）。
func fallbackToolCallId() string {
	return "call_" + strconv.FormatInt(time.Now().UnixMilli(), 10)
}

// toOpenAIUsageKiro kiro 用量直通（concerns/usage.js toOpenAIUsage kiro 分支：
// inputTokens/outputTokens 直通；cache 字段前向兼容透传——上游今天不暴露
// cache 字段，ponytail 注释保留：未来事件形状长出 cache 字段时成本跟踪无需二次改造）。
func toOpenAIUsageKiro(raw map[string]interface{}) *UsageStats {
	input := asInt(raw["inputTokens"])
	output := asInt(raw["outputTokens"])
	usage := &UsageStats{
		PromptTokens:     input,
		CompletionTokens: output,
		TotalTokens:      input + output,
	}
	cached := asInt(raw["cache_read_input_tokens"])
	if cached == 0 {
		cached = asInt(raw["cachedTokens"])
	}
	if cached == 0 {
		cached = asInt(raw["cached_tokens"])
	}
	cacheCreation := asInt(raw["cache_creation_input_tokens"])
	if cached > 0 || cacheCreation > 0 {
		usage.PromptTokensDetails = &UsageTokenDetails{
			CachedTokens:        cached,
			CacheCreationTokens: cacheCreation,
		}
	}
	return usage
}

// ===== kiro-to-openai.js =====

// KiroRespStateOpenAI kiro→openai 翻译状态（state）。
type KiroRespStateOpenAI struct {
	ResponseID   string
	Created      int64
	ChunkIndex   int
	HadToolUse   bool
	FinishReason string
	Usage        *UsageStats
}

func kiroChunkMeta(state *KiroRespStateOpenAI) (string, int64, string) {
	return state.ResponseID, state.Created, "kiro"
}

// KiroToOpenAIResponse 解析 Kiro SSE 事件并转换为 OpenAI 格式
// （kiroToOpenAIResponse）。入参三种形态：
//   - 已是 OpenAI chunk（KiroExecutor 输出，含 choices）→ 原样返回；
//   - string（原始 SSE 文本，event:/data: 行）→ 解析；
//   - map（已解析事件对象，_eventType 标注类型）。
//
// 返回 SSEEvent；无产出返回 nil。
func KiroToOpenAIResponse(chunk interface{}, state *KiroRespStateOpenAI) interface{} {
	if chunk == nil {
		return nil
	}

	// 已是 OpenAI 格式（executor transform 输出）→ 原样返回
	if ev, ok := chunk.(SSEEvent); ok && ev.Object == "chat.completion.chunk" && len(ev.Choices) > 0 {
		return ev
	}
	if m, ok := chunk.(map[string]interface{}); ok {
		if stringOf(m["object"]) == "chat.completion.chunk" && m["choices"] != nil {
			return chunk
		}
	}

	var data map[string]interface{}
	// string chunk（原始 SSE 数据）
	if s, ok := chunk.(string); ok {
		eventType := ""
		eventData := ""
		for _, line := range strings.Split(s, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				eventType = strings.TrimSpace(line[6:])
			case strings.HasPrefix(line, ":event-type:"):
				eventType = strings.TrimSpace(line[12:])
			case strings.HasPrefix(line, "data:"):
				eventData = strings.TrimSpace(line[5:])
			case strings.HasPrefix(line, ":content-type:"):
				// 跳过 content-type 头
			case strings.TrimSpace(line) != "" && !strings.HasPrefix(line, ":"):
				// 原始 JSON 数据
				eventData = strings.TrimSpace(line)
			}
		}
		if eventData == "" {
			return nil
		}
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(eventData), &parsed) == nil {
			parsed["_eventType"] = eventType
			data = parsed
		} else {
			// 非 JSON，视为原始文本
			data = map[string]interface{}{"text": eventData, "_eventType": eventType}
		}
	} else if m, ok := chunk.(map[string]interface{}); ok {
		data = m
	} else {
		return nil
	}

	// 初始化 state
	if state.ResponseID == "" {
		state.ResponseID = fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
		state.Created = time.Now().Unix()
		state.ChunkIndex = 0
	}

	eventType := stringOf(data["_eventType"])
	if eventType == "" {
		eventType = stringOf(data["event"])
	}

	// assistantResponseEvent
	if eventType == "assistantResponseEvent" || data["assistantResponseEvent"] != nil {
		var content string
		if m := asMap(data["assistantResponseEvent"]); m != nil {
			content = stringOf(m["content"])
		}
		if content == "" {
			content = stringOf(data["content"])
		}
		if content == "" {
			return nil
		}
		delta := SSEDelta{Content: content}
		if state.ChunkIndex == 0 {
			delta.Role = "assistant"
		}
		id, created, model := kiroChunkMeta(state)
		state.ChunkIndex++
		return buildKiroChunk(id, created, model, delta, nil)
	}

	// reasoningContentEvent（思考内容 → delta.reasoning_content）
	if eventType == "reasoningContentEvent" || data["reasoningContentEvent"] != nil {
		var reasoning interface{}
		if m := asMap(data["reasoningContentEvent"]); m != nil {
			reasoning = m
		} else {
			reasoning = data
		}
		var content string
		if s, ok := reasoning.(string); ok {
			content = s
		} else {
			rm := asMap(reasoning)
			if rm != nil {
				content = stringOf(rm["text"])
				if content == "" {
					content = stringOf(rm["content"])
				}
			}
			if content == "" {
				content = stringOf(data["content"])
			}
		}
		if content == "" {
			return nil
		}
		id, created, model := kiroChunkMeta(state)
		chunk := buildKiroChunk(id, created, model, reasoningDelta(content, state.ChunkIndex == 0), nil)
		state.ChunkIndex++
		return chunk
	}

	// toolUseEvent
	if eventType == "toolUseEvent" || data["toolUseEvent"] != nil {
		state.HadToolUse = true
		var toolUse map[string]interface{}
		if m := asMap(data["toolUseEvent"]); m != nil {
			toolUse = m
		} else {
			toolUse = data
		}
		toolCallID := stringOf(toolUse["toolUseId"])
		if toolCallID == "" {
			toolCallID = fallbackToolCallId()
		}
		toolName := stringOf(toolUse["name"])
		toolInput := toolUse["input"]
		if toolInput == nil {
			toolInput = map[string]interface{}{}
		}
		argsJSON, _ := json.Marshal(toolInput)
		delta := SSEDelta{
			ToolCalls: []SSEToolCall{{
				Index: 0,
				ID:    toolCallID,
				Type:  "function",
				Function: &SSEToolFunction{
					Name:      toolName,
					Arguments: string(argsJSON),
				},
			}},
		}
		if state.ChunkIndex == 0 {
			delta.Role = "assistant"
		}
		id, created, model := kiroChunkMeta(state)
		state.ChunkIndex++
		return buildKiroChunk(id, created, model, delta, nil)
	}

	// messageStopEvent / done
	if eventType == "messageStopEvent" || eventType == "done" || data["messageStopEvent"] != nil {
		// tool 使用时 finish_reason=tool_calls，否则 stop（kiro 上游无显式原因）
		finishReason := "stop"
		if state.HadToolUse {
			finishReason = toOpenAIFinishKiro("tool_use")
		} else {
			finishReason = toOpenAIFinishKiro("stop")
		}
		state.FinishReason = finishReason // 供 usage 注入

		id, created, model := kiroChunkMeta(state)
		openaiChunk := buildKiroChunk(id, created, model, SSEDelta{}, &finishReason)

		// 终块附带 usage（如有）
		if state.Usage != nil {
			openaiChunk.Usage = state.Usage
		}
		return openaiChunk
	}

	// usageEvent
	if eventType == "usageEvent" || data["usageEvent"] != nil {
		var raw map[string]interface{}
		if m := asMap(data["usageEvent"]); m != nil {
			raw = m
		} else {
			raw = data
		}
		if usage := toOpenAIUsageKiro(raw); usage != nil {
			state.Usage = usage
		}
		return nil
	}

	// 未知事件类型 - 跳过
	return nil
}

// ===== kiro-to-claude.js =====

// KiroClaudeRespState kiro→claude 翻译状态。
type KiroClaudeRespState struct {
	MessageStartSent     bool
	MessageID            string
	Model                string
	NextBlockIndex       int
	ThinkingBlockStarted bool
	ThinkingBlockIndex   int
	TextBlockStarted     bool
	TextBlockClosed      bool
	TextBlockIndex       int
	ToolCalls            map[int]kiroToolCallInfo
	ToolArgBuffers       map[int]string
	Usage                kiroClaudeUsage
	FinishReason         string
}

type kiroToolCallInfo struct {
	ID         string
	Name       string
	BlockIndex int
}

type kiroClaudeUsage struct {
	InputTokens  int
	OutputTokens int
}

func stopThinkingBlock(state *KiroClaudeRespState, results []map[string]interface{}) []map[string]interface{} {
	if !state.ThinkingBlockStarted {
		return results
	}
	results = append(results, map[string]interface{}{"type": "content_block_stop", "index": state.ThinkingBlockIndex})
	state.ThinkingBlockStarted = false
	return results
}

func stopTextBlock(state *KiroClaudeRespState, results []map[string]interface{}) []map[string]interface{} {
	if !state.TextBlockStarted || state.TextBlockClosed {
		return results
	}
	state.TextBlockClosed = true
	results = append(results, map[string]interface{}{"type": "content_block_stop", "index": state.TextBlockIndex})
	state.TextBlockStarted = false
	return results
}

// convertFinishReasonClaude OpenAI finish_reason → Claude stop_reason
// （kiro-to-claude.js convertFinishReason）。
func convertFinishReasonClaude(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// KiroToClaudeResponse 转换一个 OpenAI chunk（KiroExecutor 输出）为 Claude SSE
// 事件数组；无产出返回 nil（kiroToClaudeResponse）。
func KiroToClaudeResponse(chunk interface{}, state *KiroClaudeRespState) []map[string]interface{} {
	var data map[string]interface{}
	// 容忍 string chunk（防御性——直连路径恒为对象）
	if s, ok := chunk.(string); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" || trimmed == "[DONE]" {
			return nil
		}
		payload := trimmed
		if strings.HasPrefix(trimmed, "data:") {
			payload = strings.TrimSpace(trimmed[5:])
		}
		if json.Unmarshal([]byte(payload), &data) != nil {
			return nil
		}
	} else if ev, ok := chunk.(SSEEvent); ok {
		data = map[string]interface{}{
			"id":      ev.ID,
			"object":  ev.Object,
			"created": ev.Created,
			"model":   ev.Model,
		}
		if len(ev.Choices) > 0 {
			choice := ev.Choices[0]
			delta := map[string]interface{}{}
			if choice.Delta.Role != "" {
				delta["role"] = choice.Delta.Role
			}
			if choice.Delta.Content != "" {
				delta["content"] = choice.Delta.Content
			}
			if choice.Delta.ReasoningContent != "" {
				delta["reasoning_content"] = choice.Delta.ReasoningContent
			}
			if len(choice.Delta.ToolCalls) > 0 {
				tcs := []interface{}{}
				for _, tc := range choice.Delta.ToolCalls {
					tcm := map[string]interface{}{"index": tc.Index}
					if tc.ID != "" {
						tcm["id"] = tc.ID
					}
					if tc.Function != nil {
						fn := map[string]interface{}{}
						if tc.Function.Name != "" {
							fn["name"] = tc.Function.Name
						}
						if tc.Function.Arguments != "" {
							fn["arguments"] = tc.Function.Arguments
						}
						tcm["function"] = fn
					}
					tcs = append(tcs, tcm)
				}
				delta["tool_calls"] = tcs
			}
			choiceMap := map[string]interface{}{"index": choice.Index, "delta": delta}
			if choice.FinishReason != nil {
				choiceMap["finish_reason"] = *choice.FinishReason
			}
			data["choices"] = []interface{}{choiceMap}
		}
		if ev.Usage != nil {
			usageMap := map[string]interface{}{}
			if ev.Usage.PromptTokens != 0 {
				usageMap["prompt_tokens"] = ev.Usage.PromptTokens
			}
			if ev.Usage.CompletionTokens != 0 {
				usageMap["completion_tokens"] = ev.Usage.CompletionTokens
			}
			data["usage"] = usageMap
		}
	} else if m, ok := chunk.(map[string]interface{}); ok {
		data = m
	} else {
		return nil
	}

	if data == nil || asList(data["choices"]) == nil {
		return nil
	}
	choices := asList(data["choices"])
	if len(choices) == 0 {
		return nil
	}
	choice := asMap(choices[0])
	if choice == nil {
		return nil
	}
	delta := asMap(choice["delta"])
	if delta == nil {
		delta = map[string]interface{}{}
	}

	results := []map[string]interface{}{}

	// chunk 携带 usage 时记录（data.usage 为对象）
	if u := asMap(data["usage"]); u != nil {
		state.Usage = kiroClaudeUsage{
			InputTokens:  asInt(u["prompt_tokens"]),
			OutputTokens: asInt(u["completion_tokens"]),
		}
	}

	// 首块 → message_start
	if !state.MessageStartSent {
		state.MessageStartSent = true
		mid := stringOf(data["id"])
		if strings.HasPrefix(mid, "chatcmpl-") {
			mid = mid[len("chatcmpl-"):]
		}
		if mid == "" {
			mid = fmt.Sprintf("msg_%d", time.Now().UnixMilli())
		}
		state.MessageID = mid
		state.Model = stringOf(data["model"])
		if state.Model == "" {
			state.Model = "kiro"
		}
		state.NextBlockIndex = 0
		results = append(results, map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":            state.MessageID,
				"type":          "message",
				"role":          "assistant",
				"model":         state.Model,
				"content":       []interface{}{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]interface{}{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		})
	}

	// 推理/思考内容（reasoning_content / reasoning）
	reasoningContent := stringOf(delta["reasoning_content"])
	if reasoningContent == "" {
		reasoningContent = stringOf(delta["reasoning"])
	}
	if reasoningContent != "" {
		results = stopTextBlock(state, results)
		if !state.ThinkingBlockStarted {
			state.ThinkingBlockIndex = state.NextBlockIndex
			state.NextBlockIndex++
			state.ThinkingBlockStarted = true
			results = append(results, map[string]interface{}{
				"type":  "content_block_start",
				"index": state.ThinkingBlockIndex,
				"content_block": map[string]interface{}{
					"type":     "thinking",
					"thinking": "",
				},
			})
		}
		results = append(results, map[string]interface{}{
			"type":  "content_block_delta",
			"index": state.ThinkingBlockIndex,
			"delta": map[string]interface{}{
				"type":     "thinking_delta",
				"thinking": reasoningContent,
			},
		})
	}

	// 常规文本内容
	if content := stringOf(delta["content"]); content != "" {
		results = stopThinkingBlock(state, results)
		if !state.TextBlockStarted {
			state.TextBlockIndex = state.NextBlockIndex
			state.NextBlockIndex++
			state.TextBlockStarted = true
			state.TextBlockClosed = false
			results = append(results, map[string]interface{}{
				"type":  "content_block_start",
				"index": state.TextBlockIndex,
				"content_block": map[string]interface{}{
					"type": "text",
					"text": "",
				},
			})
		}
		results = append(results, map[string]interface{}{
			"type":  "content_block_delta",
			"index": state.TextBlockIndex,
			"delta": map[string]interface{}{
				"type": "text_delta",
				"text": content,
			},
		})
	}

	// 工具调用
	if tcs := asList(delta["tool_calls"]); tcs != nil {
		if state.ToolCalls == nil {
			state.ToolCalls = map[int]kiroToolCallInfo{}
		}
		if state.ToolArgBuffers == nil {
			state.ToolArgBuffers = map[int]string{}
		}
		for _, tc := range tcs {
			tcm := asMap(tc)
			if tcm == nil {
				continue
			}
			idx := asInt(tcm["index"])
			if id := stringOf(tcm["id"]); id != "" {
				results = stopThinkingBlock(state, results)
				results = stopTextBlock(state, results)
				toolBlockIndex := state.NextBlockIndex
				state.NextBlockIndex++
				name := ""
				if fn := asMap(tcm["function"]); fn != nil {
					name = stringOf(fn["name"])
				}
				state.ToolCalls[idx] = kiroToolCallInfo{ID: id, Name: name, BlockIndex: toolBlockIndex}
				results = append(results, map[string]interface{}{
					"type":  "content_block_start",
					"index": toolBlockIndex,
					"content_block": map[string]interface{}{
						"type":  "tool_use",
						"id":    id,
						"name":  name,
						"input": map[string]interface{}{},
					},
				})
			}
			if fn := asMap(tcm["function"]); fn != nil {
				if args := stringOf(fn["arguments"]); args != "" {
					if toolInfo, ok := state.ToolCalls[idx]; ok {
						_ = toolInfo
						state.ToolArgBuffers[idx] = state.ToolArgBuffers[idx] + args
					}
				}
			}
		}
	}

	// 结束
	if fr := stringOf(choice["finish_reason"]); fr != "" {
		results = stopThinkingBlock(state, results)
		results = stopTextBlock(state, results)

		if state.ToolCalls != nil {
			for idx, toolInfo := range state.ToolCalls {
				if buffered := state.ToolArgBuffers[idx]; buffered != "" {
					results = append(results, map[string]interface{}{
						"type":  "content_block_delta",
						"index": toolInfo.BlockIndex,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": buffered,
						},
					})
				}
				results = append(results, map[string]interface{}{"type": "content_block_stop", "index": toolInfo.BlockIndex})
			}
		}

		state.FinishReason = fr
		finalUsage := state.Usage
		if finalUsage.InputTokens == 0 && finalUsage.OutputTokens == 0 {
			finalUsage = kiroClaudeUsage{}
		}
		results = append(results, map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason": convertFinishReasonClaude(fr),
			},
			"usage": map[string]interface{}{
				"input_tokens":  finalUsage.InputTokens,
				"output_tokens": finalUsage.OutputTokens,
			},
		})
		results = append(results, map[string]interface{}{"type": "message_stop"})
	}

	if len(results) == 0 {
		return nil
	}
	return results
}

// KiroToClaudeNonStreaming 非流式 Kiro → Claude（kiroToClaudeNonStreaming：
// 防御性辅助，入参为聚合后的 OpenAI 形状 completion）。
func KiroToClaudeNonStreaming(data map[string]interface{}) map[string]interface{} {
	content := []interface{}{}
	choices := asList(data["choices"])
	var choice map[string]interface{}
	if len(choices) > 0 {
		choice = asMap(choices[0])
	}
	var message map[string]interface{}
	if choice != nil {
		message = asMap(choice["message"])
	}
	if message == nil {
		message = map[string]interface{}{}
	}

	if c := stringOf(message["content"]); c != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": c})
	}
	if tcs := asList(message["tool_calls"]); tcs != nil {
		for _, tc := range tcs {
			tcm := asMap(tc)
			if tcm == nil {
				continue
			}
			fn := asMap(tcm["function"])
			var input interface{} = map[string]interface{}{}
			if fn != nil {
				if args, ok := fn["arguments"].(string); ok {
					var parsed interface{}
					if json.Unmarshal([]byte(args), &parsed) == nil {
						input = parsed
					}
				} else if fn["arguments"] != nil {
					input = fn["arguments"]
				}
			}
			id := stringOf(tcm["id"])
			if id == "" {
				id = fmt.Sprintf("toolu_%d", time.Now().UnixMilli())
			}
			name := ""
			if fn != nil {
				name = stringOf(fn["name"])
			}
			content = append(content, map[string]interface{}{
				"type":  "tool_use",
				"id":    id,
				"name":  name,
				"input": input,
			})
		}
	}

	usage := asMap(data["usage"])
	if usage == nil {
		usage = map[string]interface{}{}
	}
	finishReason := "stop"
	if choice != nil {
		finishReason = stringOf(choice["finish_reason"])
		if finishReason == "" {
			finishReason = "stop"
		}
	}
	return map[string]interface{}{
		"id":          fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		"type":        "message",
		"role":        "assistant",
		"content":     content,
		"model":       stringOf(data["model"]),
		"stop_reason": convertFinishReasonClaude(finishReason),
		"usage": map[string]interface{}{
			"input_tokens":  asInt(usage["prompt_tokens"]),
			"output_tokens": asInt(usage["completion_tokens"]),
		},
	}
}
