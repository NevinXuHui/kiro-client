package reverseproxy

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// KiroAgenticSuffix / KiroThinkingSuffix / KiroThinkingBudgetDefault /
// KiroAgenticSystemPrompt 已迁至 kiro_const.go（1:1 对齐 9router kiroConstants.js，
// 提示词内容为 JS 原版完整分块写入协议）。

// LevelToBudget maps reasoning effort levels to token budgets.
var LevelToBudget = map[string]int{
	"none":    0,
	"minimal": 512,
	"low":     1024,
	"medium":  8192,
	"high":    24576,
	"xhigh":   32768,
	"max":     128000,
}

// KiroResolvedModel represents a parsed model with suffix flags.
type KiroResolvedModel struct {
	Upstream string // model name after stripping suffixes
	Agentic  bool
	Thinking bool
}

// ResolveKiroModel strips -agentic and -thinking suffixes from model name.
// Order: agentic first, then thinking（对齐 kiroConstants.js resolveKiroModel：
// endsWith 语义，不做 TrimSpace）。
func ResolveKiroModel(model string) KiroResolvedModel {
	res := KiroResolvedModel{Upstream: model}
	if isAgenticModel(model) {
		res.Agentic = true
		res.Upstream = stripAgenticSuffix(model)
	}
	if isThinkingModel(res.Upstream) {
		res.Thinking = true
		res.Upstream = stripThinkingSuffix(res.Upstream)
	}
	return res
}

// isAgenticModel 是否 9router 合成 agentic 变体（kiroConstants.js isAgenticModel）。
func isAgenticModel(model string) bool {
	return strings.HasSuffix(model, KiroAgenticSuffix)
}

// stripAgenticSuffix 剥离 -agentic 后缀（kiroConstants.js stripAgenticSuffix）。
func stripAgenticSuffix(model string) string {
	if !isAgenticModel(model) {
		return model
	}
	return model[:len(model)-len(KiroAgenticSuffix)]
}

// isThinkingModel 是否 9router 合成 thinking 变体（kiroConstants.js isThinkingModel；
// kr/ 命名空间上游无真实 -thinking 模型，纯合成别名）。
func isThinkingModel(model string) bool {
	return strings.HasSuffix(model, KiroThinkingSuffix)
}

// stripThinkingSuffix 剥离 -thinking 后缀（kiroConstants.js stripThinkingSuffix）。
func stripThinkingSuffix(model string) string {
	if !isThinkingModel(model) {
		return model
	}
	return model[:len(model)-len(KiroThinkingSuffix)]
}

// ThinkingIntent captures the user's desired thinking configuration.
type ThinkingIntent struct {
	Enabled bool
	Budget  int    // 0 = none, >0 = specific budget
	Level   string // low/medium/high/xhigh or ""
	Auto    bool   // true = use default budget
}

// extractThinkingFromEffort maps an effort string to a ThinkingIntent.
func extractThinkingFromEffort(effort string) *ThinkingIntent {
	switch strings.ToLower(effort) {
	case "", "none", "off", "disabled":
		return nil
	case "auto":
		return &ThinkingIntent{Enabled: true, Auto: true, Budget: KiroThinkingBudgetDefault}
	default:
		budget := effortToBudget(effort)
		return &ThinkingIntent{Enabled: true, Budget: budget, Level: effort}
	}
}

// ExtractThinking detects reasoning/thinking intent from body fields.
// Priority: output_config.effort > reasoning_effort > thinking object.
func ExtractThinking(body *ChatRequest) *ThinkingIntent {
	// 1. output_config.effort (Claude style)
	if body.OutputConfig != nil && body.OutputConfig.Effort != "" {
		if intent := extractThinkingFromEffort(body.OutputConfig.Effort); intent != nil {
			return intent
		}
	}

	// 2. thinking object (Claude style)
	if body.Thinking != nil {
		switch body.Thinking.Type {
		case "disabled":
			return nil
		case "adaptive", "enabled":
			if body.Thinking.BudgetTokens != nil && *body.Thinking.BudgetTokens > 0 {
				return &ThinkingIntent{Enabled: true, Budget: *body.Thinking.BudgetTokens}
			}
			return &ThinkingIntent{Enabled: true, Auto: true, Budget: KiroThinkingBudgetDefault}
		}
	}

	// 3. reasoning_effort (OpenAI style)
	if body.ReasoningEffort != "" {
		return extractThinkingFromEffort(body.ReasoningEffort)
	}

	return nil
}

// ExtractThinkingFromHeaders checks headers for interleaved-thinking beta.
func ExtractThinkingFromHeaders(headers map[string]string) *ThinkingIntent {
	if beta, ok := headers["anthropic-beta"]; ok {
		if strings.Contains(beta, "interleaved-thinking") {
			return &ThinkingIntent{Enabled: true, Auto: true, Budget: KiroThinkingBudgetDefault}
		}
	}
	return nil
}

// ContainsThinkingModeTag scans messages and system for <thinking_mode> tags.
func ContainsThinkingModeTag(body *ChatRequest) bool {
	tag := "<thinking_mode>enabled</thinking_mode>"
	tag2 := "<thinking_mode>interleaved</thinking_mode>"

	// Check system field
	if sysStr, ok := body.System.(string); ok {
		if strings.Contains(sysStr, tag) || strings.Contains(sysStr, tag2) {
			return true
		}
	}

	// Check messages
	for _, msg := range body.Messages {
		if msg.Role != "system" && msg.Role != "user" {
			continue
		}
		text := extractMessageText(msg.Content)
		if strings.Contains(text, tag) || strings.Contains(text, tag2) {
			return true
		}
	}
	return false
}

// extractMessageText extracts text from a ChatMessage content (json.RawMessage).
func extractMessageText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// Try string
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	// Try []ContentPart
	var parts []ContentPart
	if err := json.Unmarshal(content, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return ""
}

// ResolveKiroThinkingBudget resolves thinking budget from body, headers, and model name.
func ResolveKiroThinkingBudget(body *ChatRequest, headers map[string]string, model string) int {
	// 1. Extract from body fields
	intent := ExtractThinking(body)
	if intent != nil {
		if !intent.Enabled {
			return 0
		}
		if intent.Budget > 0 {
			return intent.Budget
		}
		return KiroThinkingBudgetDefault
	}

	// 2. Check headers
	if h := ExtractThinkingFromHeaders(headers); h != nil {
		return KiroThinkingBudgetDefault
	}

	// 3. Check existing tags in messages
	if ContainsThinkingModeTag(body) {
		return KiroThinkingBudgetDefault
	}

	// 4. Check model name（JS 原版 lowercase 后匹配）
	ml := strings.ToLower(model)
	if strings.Contains(ml, "thinking") || strings.Contains(ml, "-reason") {
		return KiroThinkingBudgetDefault
	}

	return 0
}

// BuildThinkingSystemPrefix generates the thinking mode XML tags for system prompt.
// 对齐 kiroConstants.js buildThinkingSystemPrefix：budget 钳位 1..32000，
// 0/非法值按默认 16000（JS: Math.max(1, Math.min(32000, Number(budget)||16000))），
// 结尾无换行。
func BuildThinkingSystemPrefix(budget int) string {
	safeBudget := KiroThinkingBudgetDefault
	if budget > 0 {
		safeBudget = budget
	}
	if safeBudget > 32000 {
		safeBudget = 32000
	}
	if safeBudget < 1 {
		safeBudget = 1
	}
	return "<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>" + strconv.Itoa(safeBudget) + "</max_thinking_length>"
}

// ExtractKiroEffortLevel extracts the effort level from body.
// 对齐 kiroConstants.js extractKiroEffortLevel：优先级
// output_config.effort ?? reasoning_effort ?? reasoning.effort。
func ExtractKiroEffortLevel(body *ChatRequest) string {
	if body.OutputConfig != nil && body.OutputConfig.Effort != "" {
		return normalizeEffort(body.OutputConfig.Effort)
	}
	if body.ReasoningEffort != "" {
		return normalizeEffort(body.ReasoningEffort)
	}
	if body.Reasoning != nil && body.Reasoning.Effort != "" {
		return normalizeEffort(body.Reasoning.Effort)
	}
	return ""
}

func normalizeEffort(effort string) string {
	normalized := strings.ToLower(effort)
	switch normalized {
	case "none", "off", "disabled":
		return ""
	case "xhigh", "max":
		return "high"
	case "low", "medium", "high":
		return normalized
	default:
		return ""
	}
}

// effortToBudget converts an effort level string to a token budget.
func effortToBudget(level string) int {
	if b, ok := LevelToBudget[strings.ToLower(level)]; ok {
		return b
	}
	return KiroThinkingBudgetDefault
}

// BuildAdditionalModelRequestFields returns Claude >=4.6 adaptive thinking fields.
// Only returns fields when effort level is specified.
func BuildAdditionalModelRequestFields(body *ChatRequest) map[string]interface{} {
	level := ExtractKiroEffortLevel(body)
	if level == "" {
		return nil
	}
	return map[string]interface{}{
		"thinking": map[string]interface{}{
			"type":    "adaptive",
			"display": "summarized",
		},
		"output_config": map[string]interface{}{
			"effort": level,
		},
	}
}

// SupportsAdditionalModelRequestFields checks if the model supports Claude >=4.6
// adaptive thinking. 对齐 kiroConstants.js supportsKiroAdditionalModelRequestFields：
// 归一化（lowercase + dash→dot）后按 JS 正则匹配；拒绝 major<4 或
// major==4 且（minor 缺失 || minor<=5 || minor>=1000 日期后缀）；未来模型默认放行。
var kiroAdditionalModelFieldsRE = regexp.MustCompile(`(?:^|[/.])claude(?:[/.][a-z]+)*[/.](\d+)(?:[/.](\d+))?(?:[/.]|$)`)

func SupportsAdditionalModelRequestFields(model string) bool {
	if model == "" {
		return false
	}
	normalized := strings.ToLower(strings.ReplaceAll(model, "-", "."))
	if !strings.Contains(normalized, "claude") {
		return false
	}
	m := kiroAdditionalModelFieldsRE.FindStringSubmatch(normalized)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor := 0
	hasMinor := false
	if m[2] != "" {
		minor, _ = strconv.Atoi(m[2])
		hasMinor = true
	}
	dateSuffixMinor := hasMinor && minor >= 1000
	// Kiro 在 legacy 4.5 模型上拒绝了 additionalModelRequestFields（实测）。
	// 未来 Claude/Kiro 模型默认放行，新模型发布无需更新白名单。
	if major < 4 {
		return false
	}
	if major == 4 && (!hasMinor || minor <= 5 || dateSuffixMinor) {
		return false
	}
	return true
}
