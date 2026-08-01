package reverseproxy

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// ===== Kiro Upstream Endpoints =====

// Paths must match the Kiro client: the CodeWhisperer streaming surface is
// the /generateAssistantResponse route on each host.
var kiroEndpoints = []string{
	"https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
	"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
	"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
}

func newClient(proxy ...string) tls_client.HttpClient {
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(120),
		tls_client.WithClientProfile(profiles.Chrome_144),
		tls_client.WithInsecureSkipVerify(),
	}
	if len(proxy) > 0 && proxy[0] != "" {
		opts = append(opts, tls_client.WithProxyUrl(proxy[0]))
	}
	c, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		panic(fmt.Sprintf("create client: %v", err))
	}
	return c
}

// ===== Exported helpers (used by the app layer) =====

func ParseEventStream(body io.ReadCloser, chunks chan<- SSEEvent, model string) error {
	return parseEventStream(body, chunks, model)
}

type cwState struct {
	ChatTriggerType     string          `json:"chatTriggerType,omitempty"`
	ConversationID      string          `json:"conversationId,omitempty"`
	AgentContinuationID string          `json:"agentContinuationId,omitempty"`
	AgentTaskType       string          `json:"agentTaskType,omitempty"`
	CurrentMessage      *cwTurn         `json:"currentMessage"`
	History             []cwHistoryItem `json:"history"`
	// profileArn/systemPrompt/inferenceConfig/additionalModelRequestFields
	// are sent at the payload top level (matches the Kiro client/9router);
	// keep the struct fields for code reference only.
	ProfileArn            string                 `json:"-"`
	SystemPrompt          string                 `json:"-"`
	InferenceConfig       *cwInferenceCfg        `json:"-"`
	AdditionalModelFields map[string]interface{} `json:"-"`
}

// cwPayload is the full Kiro request body: conversationState plus top-level
// agentMode/profileArn/systemPrompt/inferenceConfig/additionalModelRequestFields.
type cwPayload struct {
	ConversationState            *cwState               `json:"conversationState"`
	AgentMode                    string                 `json:"agentMode"`
	ProfileArn                   string                 `json:"profileArn,omitempty"`
	SystemPrompt                 string                 `json:"systemPrompt,omitempty"`
	InferenceConfig              *cwInferenceCfg        `json:"inferenceConfig,omitempty"`
	AdditionalModelRequestFields map[string]interface{} `json:"additionalModelRequestFields,omitempty"`
}

type cwTurn struct {
	UserInputMessage *cwUserMessage `json:"userInputMessage,omitempty"`
}

type cwUserMessage struct {
	Content                 string            `json:"content"`
	ModelId                 string            `json:"modelId,omitempty"`
	Origin                  string            `json:"origin,omitempty"`
	Images                  []cwImage         `json:"images,omitempty"`
	UserInputMessageContext *cwUserMsgContext `json:"userInputMessageContext,omitempty"`
}

type cwUserMsgContext struct {
	ToolResults []cwToolResult `json:"toolResults,omitempty"`
	Tools       []cwTool       `json:"tools,omitempty"`
}

type cwTool struct {
	ToolSpecification *cwToolSpec `json:"toolSpecification,omitempty"`
}

type cwToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema *cwInputSchema `json:"inputSchema,omitempty"`
}

type cwInputSchema struct {
	JSON map[string]interface{} `json:"json"`
}

type cwToolResult struct {
	ToolUseID string      `json:"toolUseId"`
	Status    string      `json:"status"`
	Content   []cwContent `json:"content"`
}

type cwContent struct {
	Text string `json:"text"`
}

type cwImage struct {
	Format string `json:"format"`
	Source struct {
		Bytes string `json:"bytes"`
	} `json:"source"`
}

type cwInferenceCfg struct {
	MaxTokens   int      `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

type cwHistoryItem struct {
	UserInputMessage         *cwUserMessage  `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *cwAssistantMsg `json:"assistantResponseMessage,omitempty"`
}

type cwAssistantMsg struct {
	Content  string      `json:"content"`
	ToolUses []cwToolUse `json:"toolUses,omitempty"`
}

type cwToolUse struct {
	ToolUseID string      `json:"toolUseId"`
	Name      string      `json:"name"`
	Input     interface{} `json:"input"` // safeJSONParse 结果可为任意 JSON 值（JS 语义）
}

// ===== Event Stream Parsing =====

type eventStreamState struct {
	id                     string
	created                int64
	model                  string
	contentBuf             string
	usage                  *UsageStats
	inThinking             bool
	finishEmitted          bool
	seenToolIDs            map[string]int // toolCallId → toolIndex（KiroExecutor 同款）
	chunkIndex             int
	hasReasoningContent    bool
	reasoningChunkCount    int
	hasToolCalls           bool
	toolCallIndex          int
	hasMeteringEvent       bool
	hasContextUsage        bool
	contextUsagePercentage float64
	totalContentLength     int
	contextWindow          int
}

func parseEventStream(body io.ReadCloser, chunks chan<- SSEEvent, model string) error {
	defer body.Close()
	defer close(chunks)

	st := &eventStreamState{
		id:            newSSEID(),
		created:       time.Now().Unix(),
		model:         model,
		seenToolIDs:   make(map[string]int),
		contextWindow: 200000, // 对齐 KiroExecutor：capabilities contextWindow || 200000
	}

	buf := make([]byte, 4)
	for {
		_, err := io.ReadFull(body, buf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return fmt.Errorf("read totalLen: %w", err)
		}
		totalLen := binary.BigEndian.Uint32(buf)
		if totalLen < 12 {
			continue
		}

		frame := make([]byte, totalLen)
		copy(frame, buf)
		if _, err := io.ReadFull(body, frame[4:]); err != nil {
			return fmt.Errorf("read frame: %w", err)
		}

		headersLen := binary.BigEndian.Uint32(frame[4:8])
		if headersLen > totalLen-12 {
			continue
		}

		// Parse headers
		eventType := ""
		pos := uint32(12)
		headersEnd := pos + headersLen
		for pos < headersEnd {
			if int(pos) >= len(frame) {
				break
			}
			nl := uint32(frame[pos])
			pos++
			if pos+nl > headersEnd {
				break
			}
			name := string(frame[pos : pos+nl])
			pos += nl
			if int(pos) >= len(frame) {
				break
			}
			valType := frame[pos]
			pos++
			if pos+2 > headersEnd {
				break
			}
			vl := uint32(binary.BigEndian.Uint16(frame[pos : pos+2]))
			pos += 2
			if pos+vl > headersEnd {
				break
			}
			value := string(frame[pos : pos+vl])
			pos += vl
			if name == ":event-type" && valType == 7 {
				eventType = value
			}
		}

		payloadStart := pos
		payloadEnd := totalLen - 4 // exclude message CRC
		if payloadEnd <= payloadStart {
			continue
		}
		payload := frame[payloadStart:payloadEnd]

		processEvent(st, eventType, payload, chunks)
	}

	// Send final stop if not yet emitted（KiroExecutor flush：无 usage 附带）
	if !st.finishEmitted {
		fs := "stop"
		chunks <- SSEEvent{
			ID:      st.id,
			Object:  "chat.completion.chunk",
			Created: st.created,
			Model:   st.model,
			Choices: []SSEChoice{{
				Index:        0,
				Delta:        SSEDelta{},
				FinishReason: &fs,
			}},
		}
	}

	return nil
}

// processEvent handles a single CW event. Returns true if an SSE chunk was emitted.
func processEvent(st *eventStreamState, eventType string, payload []byte, chunks chan<- SSEEvent) bool {
	switch eventType {
	case "assistantResponseEvent":
		return processAssistantResponse(st, payload, chunks)
	case "reasoningContentEvent":
		return processReasoningContent(st, payload, chunks)
	case "codeEvent":
		return processCodeEvent(st, payload, chunks)
	case "toolUseEvent":
		return processToolUse(st, payload, chunks)
	case "metricsEvent":
		return processMetrics(st, payload)
	case "contextUsageEvent":
		return processContextUsage(st, payload, chunks)
	case "meteringEvent":
		return processMetering(st, chunks)
	case "messageStopEvent":
		return processMessageStop(st, chunks)
	}
	return false
}

func processAssistantResponse(st *eventStreamState, payload []byte, chunks chan<- SSEEvent) bool {
	var ev struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil || ev.Content == "" {
		return false
	}

	content := ev.Content

	// 剥 <thinking> 字面块（对齐 KiroExecutor.transformEventStreamToSSE：
	// reasoning 已由 reasoningContentEvent 路由，避免内容流重复）
	if st.inThinking {
		if strings.Contains(content, "</thinking>") {
			st.inThinking = false
			after := strings.SplitN(content, "</thinking>", 2)[1]
			content = strings.TrimPrefix(after, "\n")
		} else {
			content = "" // thinking 块内整体丢弃
		}
	} else if strings.Contains(content, "<thinking>") {
		st.inThinking = true
		if strings.Contains(content, "</thinking>") {
			st.inThinking = false
			before := strings.SplitN(content, "<thinking>", 2)[0]
			after := strings.SplitN(content, "</thinking>", 2)[1]
			content = before + strings.TrimPrefix(after, "\n")
		} else {
			content = strings.SplitN(content, "<thinking>", 2)[0]
		}
	}

	if content == "" && st.hasReasoningContent {
		// 已发 reasoning 时跳过空 content chunk
		return false
	}

	st.totalContentLength += len(content)

	delta := SSEDelta{Content: content}
	if st.chunkIndex == 0 {
		delta.Role = "assistant"
	}
	st.chunkIndex++
	chunks <- SSEEvent{
		ID:      st.id,
		Object:  "chat.completion.chunk",
		Created: st.created,
		Model:   st.model,
		Choices: []SSEChoice{{
			Index: 0,
			Delta: delta,
		}},
	}
	return true
}

func processReasoningContent(st *eventStreamState, payload []byte, chunks chan<- SSEEvent) bool {
	// reasoningContentEvent 载荷：payload.reasoningContentEvent || payload
	// （reasoning 可为字符串或 {text|content}）
	var reasoning interface{}
	if json.Unmarshal(payload, &reasoning) != nil {
		return false
	}
	if m, ok := reasoning.(map[string]interface{}); ok {
		if rw := m["reasoningContentEvent"]; rw != nil {
			reasoning = rw
		} else {
			reasoning = m
		}
	}
	var reasoningText string
	if s, ok := reasoning.(string); ok {
		reasoningText = s
	} else if rm := asMap(reasoning); rm != nil {
		reasoningText = stringOf(rm["text"])
		if reasoningText == "" {
			reasoningText = stringOf(rm["content"])
		}
	}
	if reasoningText == "" {
		return false
	}

	st.hasReasoningContent = true
	st.totalContentLength += len(reasoningText)

	delta := SSEDelta{ReasoningContent: reasoningText}
	if st.reasoningChunkCount == 0 && st.chunkIndex == 0 {
		delta.Role = "assistant"
	}
	st.chunkIndex++
	st.reasoningChunkCount++
	chunks <- SSEEvent{
		ID:      st.id,
		Object:  "chat.completion.chunk",
		Created: st.created,
		Model:   st.model,
		Choices: []SSEChoice{{
			Index: 0,
			Delta: delta,
		}},
	}
	return true
}

func processCodeEvent(st *eventStreamState, payload []byte, chunks chan<- SSEEvent) bool {
	var ev struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil || ev.Content == "" {
		return false
	}
	st.contentBuf += ev.Content
	chunks <- SSEEvent{
		ID:      st.id,
		Object:  "chat.completion.chunk",
		Created: st.created,
		Model:   st.model,
		Choices: []SSEChoice{{
			Index: 0,
			Delta: SSEDelta{Content: ev.Content},
		}},
	}
	return true
}

func processToolUse(st *eventStreamState, payload []byte, chunks chan<- SSEEvent) bool {
	// toolUseEvent 载荷可为数组或单个（对齐 KiroExecutor）
	var events []struct {
		ToolUseID string      `json:"toolUseId"`
		Name      string      `json:"name"`
		Input     interface{} `json:"input"`
	}
	if err := json.Unmarshal(payload, &events); err != nil {
		var single struct {
			ToolUseID string      `json:"toolUseId"`
			Name      string      `json:"name"`
			Input     interface{} `json:"input"`
		}
		if err := json.Unmarshal(payload, &single); err != nil {
			return false
		}
		events = append(events, single)
	}

	st.hasToolCalls = true
	for _, ev := range events {
		toolCallID := ev.ToolUseID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("call_%d", time.Now().UnixMilli())
		}

		toolIndex, isNewTool := st.seenToolIDs[toolCallID]
		if !isNewTool {
			toolIndex = st.toolCallIndex
			st.toolCallIndex++
			st.seenToolIDs[toolCallID] = toolIndex

			// 新工具：起始块（id + name + 空 arguments）
			delta := SSEDelta{
				ToolCalls: []SSEToolCall{{
					Index: toolIndex,
					ID:    toolCallID,
					Type:  "function",
					Function: &SSEToolFunction{
						Name:      ev.Name,
						Arguments: "",
					},
				}},
			}
			if st.chunkIndex == 0 {
				delta.Role = "assistant"
			}
			st.chunkIndex++
			chunks <- SSEEvent{
				ID:      st.id,
				Object:  "chat.completion.chunk",
				Created: st.created,
				Model:   st.model,
				Choices: []SSEChoice{{Index: 0, Delta: delta}},
			}
		}

		if ev.Input != nil {
			// 后续参数块：字符串原样、对象 JSON 序列化、其他类型跳过
			var argumentsStr string
			switch input := ev.Input.(type) {
			case string:
				argumentsStr = input
			case map[string]interface{}:
				b, err := json.Marshal(input)
				if err != nil {
					continue
				}
				argumentsStr = string(b)
			case []interface{}:
				b, err := json.Marshal(input)
				if err != nil {
					continue
				}
				argumentsStr = string(b)
			default:
				continue
			}

			st.chunkIndex++
			chunks <- SSEEvent{
				ID:      st.id,
				Object:  "chat.completion.chunk",
				Created: st.created,
				Model:   st.model,
				Choices: []SSEChoice{{
					Index: 0,
					Delta: SSEDelta{
						ToolCalls: []SSEToolCall{{
							Index: toolIndex,
							Function: &SSEToolFunction{
								Arguments: argumentsStr,
							},
						}},
					},
				}},
			}
		}
	}
	return true
}

func processMetrics(st *eventStreamState, payload []byte) bool {
	// metricsEvent 载荷：payload.metricsEvent || payload
	var payloadMap map[string]interface{}
	if json.Unmarshal(payload, &payloadMap) != nil {
		return false
	}
	metrics := asMap(payloadMap["metricsEvent"])
	if metrics == nil {
		metrics = payloadMap
	}

	inputTokens := asInt(metrics["inputTokens"])
	outputTokens := asInt(metrics["outputTokens"])
	// ponytail: Amazon Q 上游今天不暴露 cache 字段；若事件形状长出
	// cache_read_input_tokens / cache_creation_input_tokens 则拾取（camelCase
	// 优先，snake 兜底），保持成本跟踪准确。
	cachedTokens := asInt(metrics["cacheReadInputTokens"])
	if cachedTokens == 0 {
		cachedTokens = asInt(metrics["cache_read_input_tokens"])
	}
	cacheCreationInputTokens := asInt(metrics["cacheCreationInputTokens"])
	if cacheCreationInputTokens == 0 {
		cacheCreationInputTokens = asInt(metrics["cache_creation_input_tokens"])
	}

	if inputTokens > 0 || outputTokens > 0 {
		st.usage = &UsageStats{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      inputTokens + outputTokens,
		}
		// Kiro 是 Claude 后端：inputTokens EXCLUDES cache（Claude 惯例），
		// 发 cache_read_input_tokens（而非 cached_tokens）走 Claude fold 路径
		if cachedTokens > 0 {
			v := cachedTokens
			st.usage.CacheReadInputTokens = &v
		}
		if cacheCreationInputTokens > 0 {
			v := cacheCreationInputTokens
			st.usage.CacheCreationInputTokens = &v
		}
	}
	return false // 不发 SSE chunk
}

func processContextUsage(st *eventStreamState, payload []byte, chunks chan<- SSEEvent) bool {
	var ev struct {
		ContextUsagePercentage float64 `json:"contextUsagePercentage"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return false
	}
	if ev.ContextUsagePercentage != 0 {
		st.contextUsagePercentage = ev.ContextUsagePercentage
		st.hasContextUsage = true
	}
	return maybeEmitUsageFinish(st, chunks)
}

func processMetering(st *eventStreamState, chunks chan<- SSEEvent) bool {
	st.hasMeteringEvent = true
	return maybeEmitUsageFinish(st, chunks)
}

// maybeEmitUsageFinish 对齐 KiroExecutor：meteringEvent 与 contextUsageEvent 双到
// 且未发 finish 时，估算 usage 并补发终块。
func maybeEmitUsageFinish(st *eventStreamState, chunks chan<- SSEEvent) bool {
	if !st.hasMeteringEvent || !st.hasContextUsage || st.finishEmitted {
		return false
	}
	st.finishEmitted = true

	// 事件未提供 usage 时估算
	if st.usage == nil {
		estimatedOutputTokens := 0
		if st.totalContentLength > 0 {
			estimatedOutputTokens = st.totalContentLength / 4
			if estimatedOutputTokens < 1 {
				estimatedOutputTokens = 1
			}
		}
		estimatedInputTokens := 0
		if st.contextUsagePercentage > 0 {
			estimatedInputTokens = int(st.contextUsagePercentage * float64(st.contextWindow) / 100)
		}
		st.usage = &UsageStats{
			PromptTokens:     estimatedInputTokens,
			CompletionTokens: estimatedOutputTokens,
			TotalTokens:      estimatedInputTokens + estimatedOutputTokens,
		}
	}

	fs := "stop"
	if st.hasToolCalls {
		fs = "tool_calls"
	}
	chunks <- SSEEvent{
		ID:      st.id,
		Object:  "chat.completion.chunk",
		Created: st.created,
		Model:   st.model,
		Choices: []SSEChoice{{
			Index:        0,
			Delta:        SSEDelta{},
			FinishReason: &fs,
		}},
		Usage: st.usage,
	}
	return true
}

func processMessageStop(st *eventStreamState, chunks chan<- SSEEvent) bool {
	fs := "stop"
	// 本轮回过工具调用则用 tool_calls finish reason
	if st.hasToolCalls {
		fs = "tool_calls"
	}

	chunks <- SSEEvent{
		ID:      st.id,
		Object:  "chat.completion.chunk",
		Created: st.created,
		Model:   st.model,
		Choices: []SSEChoice{{
			Index:        0,
			Delta:        SSEDelta{},
			FinishReason: &fs,
		}},
	}
	st.finishEmitted = true
	return true
}

// ===== Helper Functions =====

// extractUserContent extracts text and images from user message content.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GetUsageLimits 璋冪敤 Kiro GetUsageLimits API 鏌ヨ棰濆害锛?router getKiroUsage 鍚屾锛夈€
// 杩斿洖 creditUsed, creditLimit, resetAt, error銆
func GetUsageLimits(accessToken, region, proxy string) (float64, float64, string, error) {
	client := newClient(proxy)
	// 灏濊瘯澶氫釜绔偣锛?router 鍚屾锛歝odewhisperer-post 鈫?q-get锛
	type attempt struct {
		name string
		run  func() (*fhttp.Response, error)
	}
	attempts := []attempt{
		{
			name: "codewhisperer-post",
			run: func() (*fhttp.Response, error) {
				body, _ := json.Marshal(map[string]interface{}{
					"origin":       "AI_EDITOR",
					"resourceType": "AGENTIC_REQUEST",
				})
				url := fmt.Sprintf("https://codewhisperer.%s.amazonaws.com", region)
				req, _ := fhttp.NewRequest("POST", url, bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+accessToken)
				req.Header.Set("Content-Type", "application/x-amz-json-1.0")
				req.Header.Set("x-amz-target", "AmazonCodeWhispererService.GetUsageLimits")
				req.Header.Set("Accept", "application/json")
				return client.Do(req)
			},
		},
		{
			name: "q-get",
			run: func() (*fhttp.Response, error) {
				url := fmt.Sprintf("https://q.%s.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST", region)
				req, _ := fhttp.NewRequest("GET", url, nil)
				req.Header.Set("Authorization", "Bearer "+accessToken)
				req.Header.Set("Accept", "application/json")
				return client.Do(req)
			},
		},
	}

	var lastErr error
	for _, a := range attempts {
		resp, err := a.run()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", a.name, err)
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("%s: HTTP %d", a.name, resp.StatusCode)
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal(respBody, &data); err != nil {
			lastErr = fmt.Errorf("%s: parse error", a.name)
			continue
		}
		used, limit, resetAt := parseUsageResponse(data)
		return used, limit, resetAt, nil
	}
	if lastErr != nil {
		return 0, 0, "", lastErr
	}
	return 0, 0, "", fmt.Errorf("all usage endpoints failed")
}

// parseUsageResponse 瑙ｆ瀽 GetUsageLimits 鍝嶅簲锛?router parseKiroQuotaData 鍚屾锛夈€
func parseUsageResponse(data map[string]interface{}) (used, limit float64, resetAt string) {
	usageList, ok := data["usageBreakdownList"].([]interface{})
	if !ok {
		return 0, 0, ""
	}
	// 鍙?resetDate锛?router 鍚屾锛歱arseResetTime锛
	if rd, ok := data["nextDateReset"].(string); ok && rd != "" {
		resetAt = rd
	} else if rd, ok := data["resetDate"].(string); ok && rd != "" {
		resetAt = rd
	}
	for _, item := range usageList {
		breakdown, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		rt, _ := breakdown["resourceType"].(string)
		if rt == "" {
			continue
		}
		cu, _ := breakdown["currentUsageWithPrecision"].(float64)
		cl, _ := breakdown["usageLimitWithPrecision"].(float64)
		used += cu
		limit += cl
		// 鍙彇绗竴涓湁鍊肩殑 resourceType 鐨勯搴︼紙9router 鍚屾锛欰GENTIC_REQUEST锛
		if used > 0 || limit > 0 {
			break
		}
	}
	return used, limit, resetAt
}

// NewQuotaRefreshFunc 鍒涘缓 QuotaRefreshFunc锛?router 鍚屾锛歡etKiroUsage 鈫?GetUsageLimits锛
func NewQuotaRefreshFunc() QuotaRefreshFunc {
	return func(accessToken, region, proxy string) (float64, float64, string, error) {
		return GetUsageLimits(accessToken, region, proxy)
	}
}

func SanitizeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "claude-sonnet-4.5"
	}
	return model
}
