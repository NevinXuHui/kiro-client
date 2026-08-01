package reverseproxy

import (
	"encoding/json"
	"fmt"
	"time"
)

// ===== Request Types =====

type ChatRequest struct {
	Model           string           `json:"model"`
	Messages        []ChatMessage    `json:"messages"`
	Stream          bool             `json:"stream"`
	MaxTokens       int              `json:"max_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	Tools           []Tool           `json:"tools,omitempty"`
	ToolChoice      interface{}      `json:"tool_choice,omitempty"`
	System          interface{}      `json:"system,omitempty"`           // string or []ContentPart (Claude)
	ReasoningEffort string           `json:"reasoning_effort,omitempty"` // low/medium/high/none
	OutputConfig    *OutputConfig    `json:"output_config,omitempty"`    // Claude: {effort}
	Thinking        *ThinkingConfig  `json:"thinking,omitempty"`         // Claude: {type, budget_tokens}
	Reasoning       *ReasoningConfig `json:"reasoning,omitempty"`        // OpenAI Responses: {effort}
}

type ReasoningConfig struct {
	Effort string `json:"effort,omitempty"` // low/medium/high/none
}

type OutputConfig struct {
	Effort string `json:"effort,omitempty"` // low/medium/high
}

type ThinkingConfig struct {
	Type         string `json:"type,omitempty"` // disabled/adaptive/enabled
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
}

type ChatMessage struct {
	Role       string          `json:"role"` // user/assistant/system/tool
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCallItem  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"` // for tool role
	Name       string          `json:"name,omitempty"`         // for tool role
}

type ToolCallItem struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ContentPart 支持多模态内容
type ContentPart struct {
	Type     string `json:"type"` // "text" / "image_url" / "image"
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"` // data URI or http URL
	} `json:"image_url,omitempty"`
	Source *struct {
		Type  string `json:"type"`  // "base64"
		Media string `json:"media"` // "image/jpeg" etc
		Bytes string `json:"bytes"` // base64 data
	} `json:"source,omitempty"` // Claude image format
}

// ===== Response Types =====

type SSEEvent struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []SSEChoice `json:"choices"`
	Usage   *UsageStats `json:"usage,omitempty"`
}

type SSEChoice struct {
	Index        int      `json:"index"`
	Delta        SSEDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

type SSEDelta struct {
	Role             string        `json:"role,omitempty"`
	Content          string        `json:"content,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []SSEToolCall `json:"tool_calls,omitempty"`
}

type SSEToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function *SSEToolFunction `json:"function,omitempty"`
}

type SSEToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type UsageStats struct {
	PromptTokens        int                `json:"prompt_tokens"`
	CompletionTokens    int                `json:"completion_tokens"`
	TotalTokens         int                `json:"total_tokens"`
	PromptTokensDetails *UsageTokenDetails `json:"prompt_tokens_details,omitempty"`
	// Kiro 是 Claude 后端：inputTokens 不含 cache（Claude 惯例），metricsEvent
	// 以 cache_read_input_tokens/cache_creation_input_tokens 平铺输出（走
	// canonicalizeUsage 的 Claude fold 路径，对齐 KiroExecutor）。
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
}

type UsageTokenDetails struct {
	CachedTokens        int `json:"cached_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
}

// ===== SSE Helpers =====

func writeSSE(w interface{ Write([]byte) (int, error) }, e SSEEvent) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

func writeSSEDone(w interface{ Write([]byte) (int, error) }) error {
	_, err := fmt.Fprintf(w, "data: [DONE]\n\n")
	return err
}

// writeSSEComment 写 SSE 注释（keepalive）
func writeSSEComment(w interface{ Write([]byte) (int, error) }) error {
	_, err := fmt.Fprintf(w, ": ka\n\n")
	return err
}

// newID 生成响应 ID
func newSSEID() string {
	return fmt.Sprintf("chatcmpl-%d", timeUnixNano())
}

func timeUnixNano() int64 {
	return time.Now().UnixNano()
}
