package reverseproxy

// 本文件 1:1 迁移自 9router（阶段 5：聊天请求翻译）：
//   - open-sse/translator/request/openai-to-kiro.js（openaiToKiroRequest 全量）
//   - open-sse/translator/request/claude-to-kiro.js（claudeToKiroRequest 全量，
//     直连路由，不经 OpenAI 中转）
//   - 依赖的 kiroConstants.js 函数（resolveKiroThinkingBudget 等的 map 版）与
//     thinkingUnified.js extractThinking（map 版）
//
// 遗留 Bug 按既定约束原样复刻：
//   ① openai 路径 maxTokens 硬编码 32000，忽略客户端 max_tokens；
//   ② http 图片链接降级为文本 "[Image: url]"；
//   ③ 畸形 tool 参数经 safeJSONParse 兜底，不崩溃。
//
// 中间表示与 JS 一致用 map[string]interface{}，最终 JSON 往返转 cwPayload
// （供 KiroSendChat 使用），上游模型 ID 经 KiroTranslatedRequest.UpstreamModel 返回
// （对应 JS 非枚举属性 _kiroUpstreamModel）。

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// KiroTranslatorCredentials 对应 JS openaiToKiroRequest 的 credentials 参数。
type KiroTranslatorCredentials struct {
	RawHeaders           map[string]string // 原始入站请求头（键大小写不敏感）
	ConnectionID         string
	ProviderSpecificData KiroProviderSpecificData
}

// KiroTranslatedRequest 翻译结果：Kiro 请求体 + 上游模型 ID 提示。
type KiroTranslatedRequest struct {
	Payload       *cwPayload
	UpstreamModel string // 对应 JS 非枚举 _kiroUpstreamModel
}

// defaultImageMime 图片缺省 MIME（schema/defaults.js DEFAULT_IMAGE_MIME）。
const defaultImageMime = "image/png"

// ===== map 工具 =====

func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

func asList(v interface{}) []interface{} {
	l, _ := v.([]interface{})
	return l
}

func asFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func asInt(v interface{}) int {
	if f, ok := asFloat(v); ok {
		return int(f)
	}
	return 0
}

// nested 逐层取嵌套 map 值（v 为 nil 或非 map 时返回 nil）。
func nested(m map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = m
	for _, k := range keys {
		cm := asMap(cur)
		if cm == nil {
			return nil
		}
		cur = cm[k]
	}
	return cur
}

func nestedStr(m map[string]interface{}, keys ...string) string {
	return stringOf(nested(m, keys...))
}

// ===== 文本渲染 =====

// kiroToolCallToText 渲染单个工具调用为可读文本行（openai-to-kiro.js toolCallToText）。
func kiroToolCallToText(name string, input interface{}) string {
	var argStr string
	if s, ok := input.(string); ok {
		argStr = s
	} else {
		var err error
		var b []byte
		if input == nil {
			b = []byte("{}")
		} else {
			b, err = json.Marshal(input)
		}
		if err != nil {
			argStr = "{}"
		} else {
			argStr = string(b)
		}
	}
	if name == "" {
		name = "unknown"
	}
	return "[Tool call: " + name + "(" + argStr + ")]"
}

// kiroToolResultToText 渲染工具结果（字符串或内容块数组）为文本行
// （openai-to-kiro.js toolResultToText）。
func kiroToolResultToText(content interface{}) string {
	var text string
	switch c := content.(type) {
	case []interface{}:
		var parts []string
		for _, item := range c {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			} else {
				parts = append(parts, stringOf(asMap(item)["text"]))
			}
		}
		text = strings.Join(parts, "\n")
	case string:
		text = c
	}
	return "[Tool result: " + text + "]"
}

// kiroToolResultBlockToText 渲染 Claude tool_result 块内容（claude-to-kiro.js
// toolResultBlockToText：对象兜底 JSON.stringify）。
func kiroToolResultBlockToText(content interface{}) string {
	var text string
	switch c := content.(type) {
	case string:
		text = c
	case []interface{}:
		var parts []string
		for _, item := range c {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			} else {
				parts = append(parts, stringOf(asMap(item)["text"]))
			}
		}
		text = strings.Join(parts, "\n")
	default:
		if content != nil {
			if b, err := json.Marshal(content); err == nil {
				text = string(b)
			}
		}
	}
	return "[Tool result: " + text + "]"
}

// kiroSafeJSONParse 安全 JSON 解析（Bug ③：畸形参数兜底，不崩溃）。
// 非字符串原样返回；解析失败返回 fallback。
func kiroSafeJSONParse(str interface{}, fallback interface{}) interface{} {
	if s, ok := str.(string); ok {
		var out interface{}
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return fallback
		}
		return out
	}
	if str == nil {
		return fallback
	}
	return str
}

// ===== data URI（concerns/image.js parseDataUri） =====

var kiroDataURIRE = regexp.MustCompile(`^data:([^;]+);base64,([\s\S]+)$`)

func parseKiroDataURI(url string) (mimeType, base64 string, ok bool) {
	m := kiroDataURIRE.FindStringSubmatch(url)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// ===== thinkingUnified.js extractThinking（map 版） =====

type kiroThinkingCfg struct {
	Mode   string // none / budget / level / auto
	Budget int
	Level  string
}

// extractKiroThinkingFromBody 从请求体提取统一思考意图（thinkingUnified.js
// extractThinking 的 map 版；Gemini/Qwen 分支一并保留，Kiro 路由实际只会
// 收到 OpenAI/Claude 形状）。
func extractKiroThinkingFromBody(body map[string]interface{}) *kiroThinkingCfg {
	if body == nil {
		return nil
	}

	// Claude output_config.effort（显式，优先于 adaptive thinking）
	if oc := asMap(body["output_config"]); oc != nil {
		if e := stringOf(oc["effort"]); e != "" {
			el := strings.ToLower(e)
			if el == "none" || el == "off" {
				return &kiroThinkingCfg{Mode: "none"}
			}
			if el == "auto" {
				return &kiroThinkingCfg{Mode: "auto"}
			}
			return &kiroThinkingCfg{Mode: "level", Level: el}
		}
	}

	// Claude shape
	if t := asMap(body["thinking"]); t != nil {
		switch stringOf(t["type"]) {
		case "disabled":
			return &kiroThinkingCfg{Mode: "none"}
		case "adaptive", "enabled":
			if budget, ok := asFloat(t["budget_tokens"]); ok && budget > 0 {
				return &kiroThinkingCfg{Mode: "budget", Budget: int(budget)}
			}
			return &kiroThinkingCfg{Mode: "auto"}
		}
	}

	// OpenAI chat / Responses shape
	var effort string
	if e := stringOf(body["reasoning_effort"]); e != "" {
		effort = e
	} else if r := asMap(body["reasoning"]); r != nil {
		effort = stringOf(r["effort"])
	}
	if effort != "" {
		el := strings.ToLower(effort)
		if el == "none" || el == "off" {
			return &kiroThinkingCfg{Mode: "none"}
		}
		if el == "auto" {
			return &kiroThinkingCfg{Mode: "auto"}
		}
		return &kiroThinkingCfg{Mode: "level", Level: el}
	}

	// Gemini shape（top-level / generationConfig / request envelope）
	var tc map[string]interface{}
	if m := asMap(body["thinkingConfig"]); m != nil {
		tc = m
	} else if gc := asMap(body["generationConfig"]); gc != nil {
		tc = asMap(gc["thinkingConfig"])
	} else if req := asMap(body["request"]); req != nil {
		if gc2 := asMap(req["generationConfig"]); gc2 != nil {
			tc = asMap(gc2["thinkingConfig"])
		}
	}
	if tc != nil {
		if lv := stringOf(tc["thinkingLevel"]); lv != "" {
			return &kiroThinkingCfg{Mode: "level", Level: strings.ToLower(lv)}
		}
		if tb, ok := asFloat(tc["thinkingBudget"]); ok {
			if tb == 0 {
				return &kiroThinkingCfg{Mode: "none"}
			}
			if tb < 0 {
				return &kiroThinkingCfg{Mode: "auto"}
			}
			return &kiroThinkingCfg{Mode: "budget", Budget: int(tb)}
		}
	}

	// Qwen shape
	if v, ok := body["enable_thinking"].(bool); ok {
		if !v {
			return &kiroThinkingCfg{Mode: "none"}
		}
		if tb, ok2 := asFloat(body["thinking_budget"]); ok2 && tb > 0 {
			return &kiroThinkingCfg{Mode: "budget", Budget: int(tb)}
		}
		return &kiroThinkingCfg{Mode: "auto"}
	}

	return nil
}

// pickKiroHeader 大小写不敏感的头查找（kiroConstants.js pickHeader）。
func pickKiroHeader(headers map[string]string, name string) string {
	if headers == nil {
		return ""
	}
	if v, ok := headers[name]; ok {
		return v
	}
	lower := strings.ToLower(name)
	for k, v := range headers {
		if strings.ToLower(k) == lower {
			return v
		}
	}
	return ""
}

// containsTagInText 文本是否含 thinking 魔法标签（kiroConstants.js containsTagInText）。
func containsTagInText(text string) bool {
	if text == "" {
		return false
	}
	if !strings.Contains(text, "<thinking_mode>") {
		return false
	}
	return strings.Contains(text, "<thinking_mode>enabled</thinking_mode>") ||
		strings.Contains(text, "<thinking_mode>interleaved</thinking_mode>")
}

// containsThinkingModeTagBody 扫描 messages（system/user）与 body.system
// （kiroConstants.js containsThinkingModeTag 的 map 版）。
func containsThinkingModeTagBody(body map[string]interface{}) bool {
	if body == nil {
		return false
	}
	if msgs := asList(body["messages"]); msgs != nil {
		for _, m := range msgs {
			mm := asMap(m)
			if mm == nil {
				continue
			}
			role := stringOf(mm["role"])
			if role != "system" && role != "user" {
				continue
			}
			content := mm["content"]
			if s, ok := content.(string); ok {
				if containsTagInText(s) {
					return true
				}
			} else if arr, ok := content.([]interface{}); ok {
				for _, part := range arr {
					pm := asMap(part)
					if pm == nil {
						continue
					}
					if containsTagInText(stringOf(pm["text"])) {
						return true
					}
				}
			}
		}
	}
	if s, ok := body["system"].(string); ok && containsTagInText(s) {
		return true
	}
	return false
}

// resolveKiroThinkingBudgetBody 解析客户端请求的 Kiro 思考预算
// （kiroConstants.js resolveKiroThinkingBudget 的 map 版；0 = 禁用）。
func resolveKiroThinkingBudgetBody(body map[string]interface{}, headers map[string]string, model string) int {
	if cfg := extractKiroThinkingFromBody(body); cfg != nil {
		switch cfg.Mode {
		case "none":
			return 0
		case "budget":
			return cfg.Budget
		case "level":
			return effortToBudget(cfg.Level)
		default:
			return KiroThinkingBudgetDefault
		}
	}

	if headers != nil {
		beta := pickKiroHeader(headers, "anthropic-beta")
		if beta != "" && strings.Contains(strings.ToLower(beta), "interleaved-thinking") {
			return KiroThinkingBudgetDefault
		}
	}

	if containsThinkingModeTagBody(body) {
		return KiroThinkingBudgetDefault
	}

	if model != "" {
		ml := strings.ToLower(model)
		if strings.Contains(ml, "thinking") || strings.Contains(ml, "-reason") {
			return KiroThinkingBudgetDefault
		}
	}

	return 0
}

// extractKiroEffortLevelBody 提取 effort 级别（kiroConstants.js
// extractKiroEffortLevel 的 map 版：output_config.effort ?? reasoning_effort ??
// reasoning.effort）。
func extractKiroEffortLevelBody(body map[string]interface{}) string {
	var effort string
	if oc := asMap(body["output_config"]); oc != nil {
		effort = stringOf(oc["effort"])
	}
	if effort == "" {
		effort = stringOf(body["reasoning_effort"])
	}
	if effort == "" {
		if r := asMap(body["reasoning"]); r != nil {
			effort = stringOf(r["effort"])
		}
	}
	if effort == "" {
		return ""
	}
	return normalizeEffort(effort)
}

// buildKiroAdditionalModelRequestFieldsBody 构建 adaptive thinking 请求字段
// （kiroConstants.js buildKiroAdditionalModelRequestFields 的 map 版）。
func buildKiroAdditionalModelRequestFieldsBody(body map[string]interface{}) map[string]interface{} {
	effort := extractKiroEffortLevelBody(body)
	if effort == "" {
		return nil
	}
	return map[string]interface{}{
		"thinking": map[string]interface{}{
			"type":    "adaptive",
			"display": "summarized",
		},
		"output_config": map[string]interface{}{
			"effort": effort,
		},
	}
}

// buildKiroAdditionalModelRequestFieldsForModelBody 按模型过滤
// （kiroConstants.js buildKiroAdditionalModelRequestFieldsForModel 的 map 版）。
func buildKiroAdditionalModelRequestFieldsForModelBody(body map[string]interface{}, model string) map[string]interface{} {
	if !SupportsAdditionalModelRequestFields(model) {
		return nil
	}
	return buildKiroAdditionalModelRequestFieldsBody(body)
}

// ===== openai-to-kiro.js =====

// flattenToolInteractions 把会话中全部工具调用/结果拍平成纯文本
// （openai-to-kiro.js flattenToolInteractions）。仅客户端未发 tools 时调用，
// 规避 Kiro "Improperly formed request"（HTTP 400）的 "tools required" 规则。
func kiroFlattenToolInteractions(messages []interface{}) []interface{} {
	out := []interface{}{}
	for _, m := range messages {
		msg := asMap(m)
		if msg == nil {
			out = append(out, m)
			continue
		}
		role := stringOf(msg["role"])

		if role == "tool" {
			out = append(out, map[string]interface{}{
				"role":    "user",
				"content": kiroToolResultToText(msg["content"]),
			})
			continue
		}

		if role == "assistant" {
			parts := []string{}
			if arr, ok := msg["content"].([]interface{}); ok {
				for _, c := range arr {
					cm := asMap(c)
					if cm == nil {
						continue
					}
					if stringOf(cm["type"]) == "tool_use" {
						parts = append(parts, kiroToolCallToText(stringOf(cm["name"]), cm["input"]))
					} else if stringOf(cm["type"]) == "text" || cm["text"] != nil {
						parts = append(parts, stringOf(cm["text"]))
					}
				}
			} else if s, ok := msg["content"].(string); ok {
				parts = append(parts, s)
			}
			for _, tc := range asList(msg["tool_calls"]) {
				tcm := asMap(tc)
				if tcm == nil {
					continue
				}
				fn := asMap(tcm["function"])
				var name string
				var args interface{}
				if fn != nil {
					name = stringOf(fn["name"])
					args = fn["arguments"]
				}
				parts = append(parts, kiroToolCallToText(name, args))
			}
			var joined []string
			for _, p := range parts {
				if p != "" {
					joined = append(joined, p)
				}
			}
			out = append(out, map[string]interface{}{
				"role":    "assistant",
				"content": strings.Join(joined, "\n"),
			})
			continue
		}

		// 用户消息：tool_result 块替换为文本，保留文本与图片
		if role == "user" {
			if arr, ok := msg["content"].([]interface{}); ok {
				newContent := []interface{}{}
				for _, c := range arr {
					cm := asMap(c)
					if cm != nil && stringOf(cm["type"]) == "tool_result" {
						newContent = append(newContent, map[string]interface{}{
							"type": "text",
							"text": kiroToolResultToText(cm["content"]),
						})
					} else {
						newContent = append(newContent, c)
					}
				}
				nm := copyMap(msg)
				nm["content"] = newContent
				out = append(out, nm)
				continue
			}
		}

		out = append(out, m)
	}
	return out
}

// reconcileOrphanedToolResults 折叠孤儿 toolResults（openai-to-kiro.js
// reconcileOrphanedToolResults；toolUseId 无对应 toolUse 的悬空结构化引用会触发
// Kiro 400，内容折回用户文本保留）。
func kiroReconcileOrphanedToolResults(history []interface{}, currentMessage map[string]interface{}) {
	validIDs := map[string]bool{}
	for _, h := range history {
		hm := asMap(h)
		if hm == nil {
			continue
		}
		arm := asMap(hm["assistantResponseMessage"])
		if arm == nil {
			continue
		}
		for _, tu := range asList(arm["toolUses"]) {
			tum := asMap(tu)
			if tum == nil {
				continue
			}
			if id := stringOf(tum["toolUseId"]); id != "" {
				validIDs[id] = true
			}
		}
	}

	carriers := history
	if currentMessage != nil {
		carriers = append(carriers, currentMessage)
	}
	for _, item := range carriers {
		im := asMap(item)
		if im == nil {
			continue
		}
		uim := asMap(im["userInputMessage"])
		if uim == nil {
			continue
		}
		ctx := asMap(uim["userInputMessageContext"])
		if ctx == nil {
			continue
		}
		trs := asList(ctx["toolResults"])
		if len(trs) == 0 {
			continue
		}

		var kept []interface{}
		var salvaged []string
		for _, tr := range trs {
			trm := asMap(tr)
			if trm == nil {
				kept = append(kept, tr)
				continue
			}
			if validIDs[stringOf(trm["toolUseId"])] {
				kept = append(kept, tr)
			} else {
				salvaged = append(salvaged, kiroToolResultToText(trm["content"]))
			}
		}
		if len(salvaged) == 0 {
			continue
		}

		extra := strings.Join(salvaged, "\n")
		if content := stringOf(uim["content"]); content != "" {
			uim["content"] = content + "\n\n" + extra
		} else {
			uim["content"] = extra
		}
		ctx["toolResults"] = kept
		if len(kept) == 0 && len(asList(ctx["tools"])) == 0 {
			delete(uim, "userInputMessageContext")
		}
	}
}

// normalizeKiroToolSpec 构造 toolSpecification（openai-to-kiro.js 内联逻辑：
// schema 归一化为 {type, properties, required}，缺省补 required:[]）。
func normalizeKiroToolSpec(t map[string]interface{}) map[string]interface{} {
	name := nestedStr(t, "function", "name")
	if name == "" {
		name = stringOf(t["name"])
	}
	description := nestedStr(t, "function", "description")
	if description == "" {
		description = stringOf(t["description"])
	}
	if strings.TrimSpace(description) == "" {
		description = "Tool: " + name
	}

	var schema map[string]interface{}
	if s := nested(t, "function", "parameters"); s != nil {
		schema = asMap(s)
	}
	if schema == nil {
		schema = asMap(t["parameters"])
	}
	if schema == nil {
		schema = asMap(t["input_schema"])
	}
	if schema == nil {
		schema = map[string]interface{}{}
	}

	var normalizedSchema map[string]interface{}
	if len(schema) == 0 {
		normalizedSchema = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []interface{}{},
		}
	} else {
		normalizedSchema = copyMap(schema)
		if _, ok := schema["required"]; !ok {
			normalizedSchema["required"] = []interface{}{}
		}
	}

	return map[string]interface{}{
		"toolSpecification": map[string]interface{}{
			"name":        name,
			"description": description,
			"inputSchema": map[string]interface{}{"json": normalizedSchema},
		},
	}
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// convertMessagesOpenAI 转换 OpenAI 消息为 Kiro history + currentMessage
// （openai-to-kiro.js convertMessages 全量：system/tool→user 归一化、连续同角色
// 合并、tools 注入首个用户消息、清理、孤儿 toolResults 调和）。
func convertMessagesOpenAI(messages []interface{}, tools []interface{}, model string) ([]interface{}, map[string]interface{}) {
	history := []interface{}{}
	var currentMessage map[string]interface{}

	clientProvidedTools := len(tools) > 0

	if !clientProvidedTools {
		messages = kiroFlattenToolInteractions(messages)
	}

	var pendingUserContent []string
	var pendingAssistantContent []string
	var pendingToolResults []interface{}
	var pendingImages []interface{}
	currentRole := ""
	toolsInjectedToFirstUserMsg := false

	flushPending := func() {
		if currentRole == "user" {
			content := strings.TrimSpace(strings.Join(pendingUserContent, "\n\n"))
			if content == "" {
				content = "continue"
			}
			userMsg := map[string]interface{}{
				"userInputMessage": map[string]interface{}{
					"content": content,
					"modelId": "",
				},
			}
			uim := userMsg["userInputMessage"].(map[string]interface{})

			if len(pendingImages) > 0 {
				uim["images"] = pendingImages
			}
			if len(pendingToolResults) > 0 {
				uim["userInputMessageContext"] = map[string]interface{}{
					"toolResults": pendingToolResults,
				}
			}

			// tools 注入到无前驱 assistant 的首个用户消息（此后任一用户消息，
			// 取最先出现者；以标志位跟踪）
			if clientProvidedTools && !toolsInjectedToFirstUserMsg {
				ctx := asMap(uim["userInputMessageContext"])
				if ctx == nil {
					ctx = map[string]interface{}{}
					uim["userInputMessageContext"] = ctx
				}
				var specs []interface{}
				for _, t := range tools {
					specs = append(specs, normalizeKiroToolSpec(asMap(t)))
				}
				ctx["tools"] = specs
				toolsInjectedToFirstUserMsg = true
			}

			history = append(history, userMsg)
			currentMessage = userMsg
			pendingUserContent = nil
			pendingToolResults = nil
			pendingImages = nil
		} else if currentRole == "assistant" {
			content := strings.TrimSpace(strings.Join(pendingAssistantContent, "\n\n"))
			if content == "" {
				content = "..."
			}
			history = append(history, map[string]interface{}{
				"assistantResponseMessage": map[string]interface{}{"content": content},
			})
			pendingAssistantContent = nil
		}
	}

	for i := 0; i < len(messages); i++ {
		msg := asMap(messages[i])
		if msg == nil {
			continue
		}
		role := stringOf(msg["role"])

		// 归一化：system/tool → user
		wasSystem := role == "system"
		if role == "system" || role == "tool" {
			role = "user"
		}

		if role != currentRole && currentRole != "" {
			flushPending()
		}
		currentRole = role

		if role == "user" {
			var content string
			switch c := msg["content"].(type) {
			case string:
				content = c
			case []interface{}:
				var textParts []string
				for _, part := range c {
					cm := asMap(part)
					if cm == nil {
						continue
					}
					ctype := stringOf(cm["type"])
					switch {
					case ctype == "text" || cm["text"] != nil:
						textParts = append(textParts, stringOf(cm["text"]))
					case ctype == "image_url":
						url := ""
						if iu := asMap(cm["image_url"]); iu != nil {
							url = stringOf(iu["url"])
						}
						if mime, b64, ok := parseKiroDataURI(url); ok {
							format := mime
							if idx := strings.Index(mime, "/"); idx >= 0 {
								format = mime[idx+1:]
							}
							pendingImages = append(pendingImages, map[string]interface{}{
								"format": format,
								"source": map[string]interface{}{"bytes": b64},
							})
						} else if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
							// 遗留 Bug ②：Kiro 只支持 base64，http 图片降级为 URL 文本
							textParts = append(textParts, "[Image: "+url+"]")
						}
					case ctype == "image":
						// Claude 格式：source.type="base64" / media_type / data
						src := asMap(cm["source"])
						if src != nil && stringOf(src["type"]) == "base64" && stringOf(src["data"]) != "" {
							mediaType := stringOf(src["media_type"])
							if mediaType == "" {
								mediaType = defaultImageMime
							}
							format := mediaType
							if idx := strings.Index(mediaType, "/"); idx >= 0 {
								format = mediaType[idx+1:]
							}
							pendingImages = append(pendingImages, map[string]interface{}{
								"format": format,
								"source": map[string]interface{}{"bytes": stringOf(src["data"])},
							})
						}
					}
				}
				content = strings.Join(textParts, "\n")

				// tool_result 块
				var toolResultBlocks []map[string]interface{}
				for _, part := range c {
					cm := asMap(part)
					if cm != nil && stringOf(cm["type"]) == "tool_result" {
						toolResultBlocks = append(toolResultBlocks, cm)
					}
				}
				for _, block := range toolResultBlocks {
					var text string
					switch bc := block["content"].(type) {
					case []interface{}:
						var parts []string
						for _, p := range bc {
							parts = append(parts, stringOf(asMap(p)["text"]))
						}
						text = strings.Join(parts, "\n")
					case string:
						text = bc
					}
					pendingToolResults = append(pendingToolResults, map[string]interface{}{
						"toolUseId": stringOf(block["tool_use_id"]),
						"status":    "success",
						"content":   []interface{}{map[string]interface{}{"text": text}},
					})
				}
			}

			// tool 角色（已归一化）
			if stringOf(msg["role"]) == "tool" {
				toolContent := ""
				if s, ok := msg["content"].(string); ok {
					toolContent = s
				}
				pendingToolResults = append(pendingToolResults, map[string]interface{}{
					"toolUseId": stringOf(msg["tool_call_id"]),
					"status":    "success",
					"content":   []interface{}{map[string]interface{}{"text": toolContent}},
				})
			} else if content != "" {
				// <instructions> 标签：Claude 模型视为权威指令
				if wasSystem {
					pendingUserContent = append(pendingUserContent, "<instructions>\n"+content+"\n</instructions>")
				} else {
					pendingUserContent = append(pendingUserContent, content)
				}
			}
		} else if role == "assistant" {
			var textContent string
			var toolUses []interface{}

			switch c := msg["content"].(type) {
			case []interface{}:
				var textBlocks []string
				var tuBlocks []map[string]interface{}
				for _, part := range c {
					cm := asMap(part)
					if cm == nil {
						continue
					}
					if stringOf(cm["type"]) == "text" {
						textBlocks = append(textBlocks, stringOf(cm["text"]))
					} else if stringOf(cm["type"]) == "tool_use" {
						tuBlocks = append(tuBlocks, cm)
					}
				}
				textContent = strings.TrimSpace(strings.Join(textBlocks, "\n"))
				for _, b := range tuBlocks {
					toolUses = append(toolUses, b)
				}
			case string:
				textContent = strings.TrimSpace(c)
			}

			if tcs := asList(msg["tool_calls"]); len(tcs) > 0 {
				toolUses = tcs
			}

			if textContent != "" {
				pendingAssistantContent = append(pendingAssistantContent, textContent)
			}

			// 工具使用存入最后一条 assistant 消息
			if len(toolUses) > 0 {
				flushPending()
				if len(history) > 0 {
					lastMsg := asMap(history[len(history)-1])
					if arm := asMap(lastMsg["assistantResponseMessage"]); arm != nil {
						var mapped []interface{}
						for _, tc := range toolUses {
							tcm := asMap(tc)
							if tcm == nil {
								continue
							}
							tid := stringOf(tcm["id"])
							if tid == "" {
								tid = uuid.NewString()
							}
							if fn := asMap(tcm["function"]); fn != nil {
								mapped = append(mapped, map[string]interface{}{
									"toolUseId": tid,
									"name":      stringOf(fn["name"]),
									"input":     kiroSafeJSONParse(fn["arguments"], map[string]interface{}{}),
								})
							} else {
								input := tcm["input"]
								if input == nil {
									input = map[string]interface{}{}
								}
								mapped = append(mapped, map[string]interface{}{
									"toolUseId": tid,
									"name":      stringOf(tcm["name"]),
									"input":     input,
								})
							}
						}
						arm["toolUses"] = mapped
					}
				}
				currentRole = ""
			}
		}
	}

	if currentRole != "" {
		flushPending()
	}

	// 弹出最后一条 userInputMessage 作为 currentMessage（自尾向前，跳过尾部 assistant）
	for i := len(history) - 1; i >= 0; i-- {
		item := asMap(history[i])
		if item != nil && item["userInputMessage"] != nil {
			currentMessage = item
			history = append(history[:i], history[i+1:]...)
			break
		}
	}

	// 清理前先抓取首个 history 项的 tools
	var firstHistoryTools []interface{}
	if len(history) > 0 {
		if h0 := asMap(history[0]); h0 != nil {
			if uim0 := asMap(h0["userInputMessage"]); uim0 != nil {
				if ctx0 := asMap(uim0["userInputMessageContext"]); ctx0 != nil {
					firstHistoryTools = asList(ctx0["tools"])
				}
			}
		}
	}

	// Kiro API 兼容清理
	for _, item := range history {
		im := asMap(item)
		if im == nil {
			continue
		}
		uim := asMap(im["userInputMessage"])
		if uim == nil {
			continue
		}
		ctx := asMap(uim["userInputMessageContext"])
		if ctx != nil {
			delete(ctx, "tools")
			if len(ctx) == 0 {
				delete(uim, "userInputMessageContext")
			}
		}
		if stringOf(uim["modelId"]) == "" {
			uim["modelId"] = model
		}
	}

	// 合并连续用户消息（Kiro 要求 user/assistant 交替；合并时同时合并上下文
	// 字段，避免 toolResults/images 被静默丢弃）
	var mergedHistory []interface{}
	for i := 0; i < len(history); i++ {
		current := asMap(history[i])
		if current != nil && current["userInputMessage"] != nil && len(mergedHistory) > 0 {
			prev := asMap(mergedHistory[len(mergedHistory)-1])
			if prev != nil && prev["userInputMessage"] != nil {
				prevUim := asMap(prev["userInputMessage"])
				curUim := asMap(current["userInputMessage"])
				prevUim["content"] = stringOf(prevUim["content"]) + "\n\n" + stringOf(curUim["content"])
				prevCtx := asMap(prevUim["userInputMessageContext"])
				curCtx := asMap(curUim["userInputMessageContext"])
				if curCtx != nil {
					if prevCtx == nil {
						prevUim["userInputMessageContext"] = curCtx
					} else {
						if curTRs := asList(curCtx["toolResults"]); len(curTRs) > 0 {
							prevCtx["toolResults"] = append(asList(prevCtx["toolResults"]), curTRs...)
						}
						if curTs := asList(curCtx["tools"]); len(curTs) > 0 {
							prevCtx["tools"] = append(asList(prevCtx["tools"]), curTs...)
						}
					}
				}
				continue
			}
		}
		mergedHistory = append(mergedHistory, history[i])
	}

	// 无用户消息的边界情况：构造最小 currentMessage 以便注入 tools/content
	if currentMessage == nil {
		currentMessage = map[string]interface{}{
			"userInputMessage": map[string]interface{}{
				"content": "",
				"modelId": model,
			},
		}
	}

	// 孤儿 toolResults 调和（仅 tools 存在路径；无 tools 时 flatten 已全部文本化）
	if clientProvidedTools {
		kiroReconcileOrphanedToolResults(mergedHistory, currentMessage)
	}

	// 清理后把 tools 注入 currentMessage（仅客户端显式发过 tools 时存在）
	resolvedTools := firstHistoryTools
	if len(resolvedTools) > 0 {
		uim := asMap(currentMessage["userInputMessage"])
		if uim != nil {
			ctx := asMap(uim["userInputMessageContext"])
			if ctx == nil || ctx["tools"] == nil {
				if ctx == nil {
					ctx = map[string]interface{}{}
					uim["userInputMessageContext"] = ctx
				}
				ctx["tools"] = resolvedTools
			}
		}
	}

	return mergedHistory, currentMessage
}

// OpenAIToKiroRequest 从 OpenAI 格式构建 Kiro payload
// （openai-to-kiro.js openaiToKiroRequest 全量）。
func OpenAIToKiroRequest(model string, body map[string]interface{}, credentials *KiroTranslatorCredentials) (*KiroTranslatedRequest, error) {
	messages := asList(body["messages"])
	if messages == nil {
		messages = []interface{}{}
	}
	tools := asList(body["tools"])
	if tools == nil {
		tools = []interface{}{}
	}
	// 遗留 Bug ①：maxTokens 硬编码 32000，忽略客户端 max_tokens
	maxTokens := 32000
	temperature := body["temperature"]
	topP := body["top_p"]

	resolved := ResolveKiroModel(model)
	upstreamModel := resolved.Upstream
	agentic := resolved.Agentic

	var rawHeaders map[string]string
	if credentials != nil {
		rawHeaders = credentials.RawHeaders
	}
	thinkingBudget := resolveKiroThinkingBudgetBody(body, rawHeaders, model)

	history, currentMessage := convertMessagesOpenAI(messages, tools, upstreamModel)

	// api_key / idc / external_idp 的 profile 与账号绑定：注入共享默认占位 ARN
	// 会让 CodeWhisperer 回 403 "bearer token invalid"，故绝不回退默认值——发已
	// 解析 ARN 或空串（上游用 token 自身默认 profile）。仅 OAuth/social 保留占位。
	var authMethod string
	var connID string
	if credentials != nil {
		authMethod = credentials.ProviderSpecificData.AuthMethod
		connID = credentials.ConnectionID
	}
	accountBoundAuth := authMethod == "api_key" || authMethod == "idc" || authMethod == "external_idp"
	var profileArn string
	if accountBoundAuth {
		profileArn = credentials.ProviderSpecificData.ProfileArn
	} else {
		profileArn = credentials.ProviderSpecificData.ProfileArn
		if profileArn == "" {
			profileArn = ResolveDefaultProfileArn(authMethod)
		}
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	// Kiro CLI/KAS 以顶层 systemPrompt 发送；同时保留内容兜底（CodeWhisperer
	// 直连调用不总强制顶层 systemPrompt）。
	var systemPromptParts []string
	if thinkingBudget != 0 {
		systemPromptParts = append(systemPromptParts, BuildThinkingSystemPrefix(thinkingBudget))
	}
	if agentic {
		systemPromptParts = append(systemPromptParts, KiroAgenticSystemPrompt)
	}
	var systemPromptPartsFiltered []string
	for _, p := range systemPromptParts {
		if p != "" {
			systemPromptPartsFiltered = append(systemPromptPartsFiltered, p)
		}
	}
	systemPrompt := strings.Join(systemPromptPartsFiltered, "\n\n")
	currentTimeContext := "[Context: Current time is " + timestamp + "]"
	contentPrefix := strings.Join(filterNonEmpty(systemPrompt, currentTimeContext), "\n\n")

	sessionIdentity := ResolveSessionIdentity(rawHeaders, body, connID, "", "kiro")
	conversationID := sessionIdentity.SessionID
	continuationID := ResolveContinuationId(conversationID, connID, "kiro", sessionIdentity.Ephemeral)

	replay := ApplyKiroSessionReplay(KiroSessionReplayOpts{
		ConversationID:       conversationID,
		ConnectionID:         connID,
		ModelID:              upstreamModel,
		SystemPrompt:         systemPrompt,
		ContentPrefix:        contentPrefix,
		CurrentContentPrefix: currentTimeContext,
		History:              history,
		CurrentMessage:       currentMessage,
	})
	replayCurrent := map[string]interface{}{}
	if replay.CurrentMessage != nil {
		replayCurrent = asMap(replay.CurrentMessage["userInputMessage"])
		if replayCurrent == nil {
			replayCurrent = map[string]interface{}{}
		}
	}

	currentUIM := map[string]interface{}{
		"content": stringOf(replayCurrent["content"]),
		"modelId": upstreamModel,
		"origin":  "AI_EDITOR",
	}
	if images := asList(replayCurrent["images"]); len(images) > 0 {
		currentUIM["images"] = images
	}
	if ctx := asMap(replayCurrent["userInputMessageContext"]); ctx != nil {
		currentUIM["userInputMessageContext"] = ctx
	}

	payload := map[string]interface{}{
		"conversationState": map[string]interface{}{
			"chatTriggerType":     "MANUAL",
			"conversationId":      conversationID,
			"agentContinuationId": continuationID,
			"agentTaskType":       "vibe",
			"currentMessage": map[string]interface{}{
				"userInputMessage": currentUIM,
			},
			"history": replay.History,
		},
		"agentMode": "vibe",
	}

	if profileArn != "" {
		payload["profileArn"] = profileArn
	}
	if systemPrompt != "" {
		payload["systemPrompt"] = systemPrompt
	}
	if fields := buildKiroAdditionalModelRequestFieldsForModelBody(body, upstreamModel); fields != nil {
		payload["additionalModelRequestFields"] = fields
	}

	if maxTokens != 0 || temperature != nil || topP != nil {
		ic := map[string]interface{}{}
		if maxTokens != 0 {
			ic["maxTokens"] = maxTokens
		}
		if temperature != nil {
			ic["temperature"] = temperature
		}
		if topP != nil {
			ic["topP"] = topP
		}
		payload["inferenceConfig"] = ic
	}

	return kiroFinalizePayload(payload, upstreamModel)
}

func filterNonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// kiroFinalizePayload 把 map payload 经 JSON 往返转为 cwPayload
// （_kiroUpstreamModel 为非枚举属性，不进入 JSON，单独返回）。
func kiroFinalizePayload(payload map[string]interface{}, upstreamModel string) (*KiroTranslatedRequest, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var cw cwPayload
	if err := json.Unmarshal(b, &cw); err != nil {
		return nil, err
	}
	return &KiroTranslatedRequest{Payload: &cw, UpstreamModel: upstreamModel}, nil
}

// ===== claude-to-kiro.js =====

// flattenClaudeToolInteractions 客户端未发 tools 时把所有 tool_use/tool_result
// 内容块改写为纯文本（claude-to-kiro.js flattenClaudeToolInteractions；保留
// 文本与图片，不改输入）。
func flattenClaudeToolInteractions(messages []interface{}) []interface{} {
	out := []interface{}{}
	for _, m := range messages {
		msg := asMap(m)
		if msg == nil {
			out = append(out, m)
			continue
		}
		role := stringOf(msg["role"])

		if role == "assistant" {
			if arr, ok := msg["content"].([]interface{}); ok {
				parts := []string{}
				for _, block := range arr {
					bm := asMap(block)
					if bm == nil {
						continue
					}
					switch stringOf(bm["type"]) {
					case "text":
						parts = append(parts, stringOf(bm["text"]))
					case "tool_use":
						parts = append(parts, kiroToolCallToText(stringOf(bm["name"]), bm["input"]))
					}
				}
				nm := copyMap(msg)
				nm["content"] = strings.Join(parts, "\n")
				out = append(out, nm)
				continue
			}
		}

		if role == "user" {
			if arr, ok := msg["content"].([]interface{}); ok {
				newContent := []interface{}{}
				for _, block := range arr {
					bm := asMap(block)
					if bm != nil && stringOf(bm["type"]) == "tool_result" {
						newContent = append(newContent, map[string]interface{}{
							"type": "text",
							"text": kiroToolResultBlockToText(bm["content"]),
						})
					} else {
						newContent = append(newContent, block)
					}
				}
				nm := copyMap(msg)
				nm["content"] = newContent
				out = append(out, nm)
				continue
			}
		}

		out = append(out, m)
	}
	return out
}

// reconcileOrphanedToolResultsClaude 折叠孤儿 toolResults（claude-to-kiro.js
// reconcileOrphanedToolResults；文本渲染与 openai 版不同：数组项取 text||""）。
func reconcileOrphanedToolResultsClaude(history []interface{}, currentMessage map[string]interface{}) {
	validIDs := map[string]bool{}
	for _, h := range history {
		hm := asMap(h)
		if hm == nil {
			continue
		}
		arm := asMap(hm["assistantResponseMessage"])
		if arm == nil {
			continue
		}
		for _, tu := range asList(arm["toolUses"]) {
			tum := asMap(tu)
			if tum == nil {
				continue
			}
			if id := stringOf(tum["toolUseId"]); id != "" {
				validIDs[id] = true
			}
		}
	}

	carriers := history
	if currentMessage != nil {
		carriers = append(carriers, currentMessage)
	}
	for _, item := range carriers {
		im := asMap(item)
		if im == nil {
			continue
		}
		uim := asMap(im["userInputMessage"])
		if uim == nil {
			continue
		}
		ctx := asMap(uim["userInputMessageContext"])
		if ctx == nil {
			continue
		}
		trs := asList(ctx["toolResults"])
		if len(trs) == 0 {
			continue
		}

		var kept []interface{}
		var salvaged []string
		for _, tr := range trs {
			trm := asMap(tr)
			if trm == nil {
				kept = append(kept, tr)
				continue
			}
			if validIDs[stringOf(trm["toolUseId"])] {
				kept = append(kept, tr)
			} else {
				var text string
				if arr, ok := trm["content"].([]interface{}); ok {
					var parts []string
					for _, c := range arr {
						parts = append(parts, stringOf(asMap(c)["text"]))
					}
					text = strings.Join(parts, "\n")
				}
				salvaged = append(salvaged, "[Tool result: "+text+"]")
			}
		}
		if len(salvaged) == 0 {
			continue
		}

		extra := strings.Join(salvaged, "\n")
		if content := stringOf(uim["content"]); content != "" {
			uim["content"] = content + "\n\n" + extra
		} else {
			uim["content"] = extra
		}
		ctx["toolResults"] = kept
		if len(kept) == 0 && len(asList(ctx["tools"])) == 0 {
			delete(uim, "userInputMessageContext")
		}
	}
}

// buildClaudeToolSpecs 构造 Claude 工具规范（claude-to-kiro.js buildToolSpecs）。
func buildClaudeToolSpecs(tools []interface{}) []interface{} {
	var specs []interface{}
	for _, t := range tools {
		tm := asMap(t)
		if tm == nil {
			continue
		}
		name := stringOf(tm["name"])
		description := stringOf(tm["description"])
		if description == "" {
			description = "Tool: " + name
		}
		schema := asMap(tm["input_schema"])
		if schema == nil {
			schema = map[string]interface{}{}
		}
		var normalizedSchema map[string]interface{}
		if len(schema) == 0 {
			normalizedSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
				"required":   []interface{}{},
			}
		} else {
			normalizedSchema = copyMap(schema)
			if _, ok := schema["required"]; !ok {
				normalizedSchema["required"] = []interface{}{}
			}
		}
		specs = append(specs, map[string]interface{}{
			"toolSpecification": map[string]interface{}{
				"name":        name,
				"description": description,
				"inputSchema": map[string]interface{}{"json": normalizedSchema},
			},
		})
	}
	return specs
}

// convertClaudeMessages 转换 Claude 消息为 Kiro history + currentMessage
// （claude-to-kiro.js convertClaudeMessagesToKiro 全量：user/assistant 交替、
// 连续同角色合并、tools 注入首个用户轮次、清理、孤儿调和在外部调用）。
func convertClaudeMessages(messages []interface{}, tools []interface{}, model string) ([]interface{}, map[string]interface{}) {
	history := []interface{}{}
	var currentMessage map[string]interface{}

	var pendingUserContent []string
	var pendingAssistantContent []string
	var pendingToolResults []interface{}
	var pendingImages []interface{}
	currentRole := ""
	toolsInjected := false

	clientProvidedTools := len(tools) > 0

	flushPending := func() {
		if currentRole == "user" {
			content := strings.TrimSpace(strings.Join(pendingUserContent, "\n\n"))
			if content == "" {
				content = "continue"
			}
			userMsg := map[string]interface{}{
				"userInputMessage": map[string]interface{}{
					"content": content,
					"modelId": model,
				},
			}
			uim := userMsg["userInputMessage"].(map[string]interface{})

			if len(pendingImages) > 0 {
				uim["images"] = pendingImages
			}
			if len(pendingToolResults) > 0 {
				uim["userInputMessageContext"] = map[string]interface{}{
					"toolResults": pendingToolResults,
				}
			}
			// tools 仅注入首个用户轮次
			if clientProvidedTools && !toolsInjected {
				ctx := asMap(uim["userInputMessageContext"])
				if ctx == nil {
					ctx = map[string]interface{}{}
					uim["userInputMessageContext"] = ctx
				}
				ctx["tools"] = buildClaudeToolSpecs(tools)
				toolsInjected = true
			}

			history = append(history, userMsg)
			currentMessage = userMsg
			pendingUserContent = nil
			pendingToolResults = nil
			pendingImages = nil
		} else if currentRole == "assistant" {
			content := strings.TrimSpace(strings.Join(pendingAssistantContent, "\n\n"))
			if content == "" {
				content = "..."
			}
			history = append(history, map[string]interface{}{
				"assistantResponseMessage": map[string]interface{}{"content": content},
			})
			pendingAssistantContent = nil
		}
	}

	for _, m := range messages {
		msg := asMap(m)
		if msg == nil {
			continue
		}
		role := stringOf(msg["role"])
		if role != currentRole && currentRole != "" {
			flushPending()
		}
		currentRole = role

		if role == "user" {
			switch c := msg["content"].(type) {
			case string:
				pendingUserContent = append(pendingUserContent, c)
			case []interface{}:
				for _, block := range c {
					bm := asMap(block)
					if bm == nil {
						continue
					}
					switch stringOf(bm["type"]) {
					case "text":
						pendingUserContent = append(pendingUserContent, stringOf(bm["text"]))
					case "image":
						src := asMap(bm["source"])
						if src != nil && stringOf(src["type"]) == "base64" {
							mediaType := stringOf(src["media_type"])
							if mediaType == "" {
								mediaType = defaultImageMime
							}
							format := mediaType
							if idx := strings.Index(mediaType, "/"); idx >= 0 {
								format = mediaType[idx+1:]
							}
							pendingImages = append(pendingImages, map[string]interface{}{
								"format": format,
								"source": map[string]interface{}{"bytes": stringOf(src["data"])},
							})
						}
					case "tool_result":
						var resultContent string
						switch bc := bm["content"].(type) {
						case string:
							resultContent = bc
						case []interface{}:
							var textParts []string
							for _, c2 := range bc {
								c2m := asMap(c2)
								if c2m != nil && stringOf(c2m["type"]) == "text" {
									textParts = append(textParts, stringOf(c2m["text"]))
								}
							}
							resultContent = strings.Join(textParts, "\n")
							if resultContent == "" {
								if b, err := json.Marshal(bc); err == nil {
									resultContent = string(b)
								}
							}
						default:
							if bm["content"] != nil {
								if b, err := json.Marshal(bm["content"]); err == nil {
									resultContent = string(b)
								}
							}
						}
						pendingToolResults = append(pendingToolResults, map[string]interface{}{
							"toolUseId": stringOf(bm["tool_use_id"]),
							"status":    "success",
							"content":   []interface{}{map[string]interface{}{"text": resultContent}},
						})
					}
				}
			}
		} else if role == "assistant" {
			var textContent string
			var toolUses []interface{}
			switch c := msg["content"].(type) {
			case string:
				textContent = c
			case []interface{}:
				for _, block := range c {
					bm := asMap(block)
					if bm == nil {
						continue
					}
					switch stringOf(bm["type"]) {
					case "text":
						textContent += stringOf(bm["text"])
					case "tool_use":
						input := bm["input"]
						if input == nil {
							input = map[string]interface{}{}
						}
						toolUses = append(toolUses, map[string]interface{}{
							"toolUseId": stringOf(bm["id"]),
							"name":      stringOf(bm["name"]),
							"input":     input,
						})
					}
				}
			}
			if textContent != "" {
				pendingAssistantContent = append(pendingAssistantContent, textContent)
			}

			if len(toolUses) > 0 {
				flushPending()
				if len(history) > 0 {
					lastMsg := asMap(history[len(history)-1])
					if arm := asMap(lastMsg["assistantResponseMessage"]); arm != nil {
						arm["toolUses"] = toolUses
					}
				}
				currentRole = ""
			}
		}
	}

	if currentRole != "" {
		flushPending()
	}

	// 弹出最后一条用户轮次作为 currentMessage（跳过尾部 assistant 轮次）
	for i := len(history) - 1; i >= 0; i-- {
		item := asMap(history[i])
		if item != nil && item["userInputMessage"] != nil {
			currentMessage = item
			history = append(history[:i], history[i+1:]...)
			break
		}
	}

	var firstHistoryTools []interface{}
	if len(history) > 0 {
		if h0 := asMap(history[0]); h0 != nil {
			if uim0 := asMap(h0["userInputMessage"]); uim0 != nil {
				if ctx0 := asMap(uim0["userInputMessageContext"]); ctx0 != nil {
					firstHistoryTools = asList(ctx0["tools"])
				}
			}
		}
	}

	for _, item := range history {
		im := asMap(item)
		if im == nil {
			continue
		}
		uim := asMap(im["userInputMessage"])
		if uim == nil {
			continue
		}
		ctx := asMap(uim["userInputMessageContext"])
		if ctx != nil {
			delete(ctx, "tools")
			if len(ctx) == 0 {
				delete(uim, "userInputMessageContext")
			}
		}
		if stringOf(uim["modelId"]) == "" {
			uim["modelId"] = model
		}
	}

	// 合并连续用户轮次
	var mergedHistory []interface{}
	for _, current := range history {
		cm := asMap(current)
		if cm != nil && cm["userInputMessage"] != nil && len(mergedHistory) > 0 {
			prev := asMap(mergedHistory[len(mergedHistory)-1])
			if prev != nil && prev["userInputMessage"] != nil {
				prevUim := asMap(prev["userInputMessage"])
				curUim := asMap(cm["userInputMessage"])
				prevUim["content"] = stringOf(prevUim["content"]) + "\n\n" + stringOf(curUim["content"])
				prevCtx := asMap(prevUim["userInputMessageContext"])
				curCtx := asMap(curUim["userInputMessageContext"])
				if curCtx != nil {
					if prevCtx == nil {
						prevUim["userInputMessageContext"] = curCtx
					} else {
						if curTRs := asList(curCtx["toolResults"]); len(curTRs) > 0 {
							prevCtx["toolResults"] = append(asList(prevCtx["toolResults"]), curTRs...)
						}
						if curTs := asList(curCtx["tools"]); len(curTs) > 0 {
							prevCtx["tools"] = append(asList(prevCtx["tools"]), curTs...)
						}
					}
				}
				continue
			}
		}
		mergedHistory = append(mergedHistory, current)
	}

	if currentMessage == nil {
		currentMessage = map[string]interface{}{
			"userInputMessage": map[string]interface{}{
				"content": "",
				"modelId": model,
			},
		}
	}

	// 清理后注入 tools 到 currentMessage（如尚未注入）
	if len(firstHistoryTools) > 0 {
		uim := asMap(currentMessage["userInputMessage"])
		if uim != nil {
			ctx := asMap(uim["userInputMessageContext"])
			if ctx == nil || ctx["tools"] == nil {
				if ctx == nil {
					ctx = map[string]interface{}{}
					uim["userInputMessageContext"] = ctx
				}
				ctx["tools"] = firstHistoryTools
			}
		}
	}

	return mergedHistory, currentMessage
}

// extractClaudeSystemText 提取 Claude 顶层 system 文本（claude-to-kiro.js
// extractClaudeSystemText：字符串 / 块数组）。
func extractClaudeSystemText(system interface{}) string {
	switch s := system.(type) {
	case nil:
		return ""
	case string:
		return s
	case []interface{}:
		var parts []string
		for _, item := range s {
			if str, ok := item.(string); ok {
				parts = append(parts, str)
			} else {
				parts = append(parts, stringOf(asMap(item)["text"]))
			}
		}
		var filtered []string
		for _, p := range parts {
			if p != "" {
				filtered = append(filtered, p)
			}
		}
		return strings.Join(filtered, "\n")
	default:
		return ""
	}
}

// ClaudeToKiroRequest 从 Claude Messages API 请求体直接构建 Kiro payload
// （claude-to-kiro.js claudeToKiroRequest 全量，直连路由不经 OpenAI 中转）。
// 复刻 openai-to-kiro.js 的两个 400-guard。
func ClaudeToKiroRequest(model string, body map[string]interface{}, credentials *KiroTranslatorCredentials) (*KiroTranslatedRequest, error) {
	messages := asList(body["messages"])
	if messages == nil {
		messages = []interface{}{}
	}
	tools := asList(body["tools"])
	if tools == nil {
		tools = []interface{}{}
	}
	clientProvidedTools := len(tools) > 0
	maxTokens := asInt(body["max_tokens"])
	if maxTokens == 0 {
		maxTokens = 32000
	}
	temperature := body["temperature"]
	topP := body["top_p"]

	resolved := ResolveKiroModel(model)
	upstreamModel := resolved.Upstream
	agentic := resolved.Agentic

	var rawHeaders map[string]string
	var authMethod string
	var connID string
	if credentials != nil {
		rawHeaders = credentials.RawHeaders
		authMethod = credentials.ProviderSpecificData.AuthMethod
		connID = credentials.ConnectionID
	}
	thinkingBudget := resolveKiroThinkingBudgetBody(body, rawHeaders, model)

	// Guard 1：客户端未发 tools → 拍平全部工具交互为文本
	if !clientProvidedTools {
		messages = flattenClaudeToolInteractions(messages)
	}

	history, currentMessage := convertClaudeMessages(messages, tools, upstreamModel)

	// Guard 2：有 tools → 调和悬空 tool_results
	if clientProvidedTools {
		reconcileOrphanedToolResultsClaude(history, currentMessage)
	}

	// api_key / idc / external_idp 绝不使用共享默认 ARN（属其他账号 → 403）；
	// OAuth/social 回退默认值。
	accountBoundAuth := authMethod == "api_key" || authMethod == "idc" || authMethod == "external_idp"
	var profileArn string
	if accountBoundAuth {
		profileArn = credentials.ProviderSpecificData.ProfileArn
	} else {
		profileArn = credentials.ProviderSpecificData.ProfileArn
		if profileArn == "" {
			profileArn = ResolveDefaultProfileArn(authMethod)
		}
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	var systemPromptParts []string
	if thinkingBudget != 0 {
		systemPromptParts = append(systemPromptParts, BuildThinkingSystemPrefix(thinkingBudget))
	}
	if agentic {
		systemPromptParts = append(systemPromptParts, KiroAgenticSystemPrompt)
	}
	if si := extractClaudeSystemText(body["system"]); si != "" {
		systemPromptParts = append(systemPromptParts, si)
	}
	systemPrompt := strings.Join(filterNonEmpty(systemPromptParts...), "\n\n")
	currentTimeContext := "[Context: Current time is " + timestamp + "]"
	contentPrefix := strings.Join(filterNonEmpty(systemPrompt, currentTimeContext), "\n\n")

	sessionIdentity := ResolveSessionIdentity(rawHeaders, body, connID, "", "kiro")
	conversationID := sessionIdentity.SessionID
	continuationID := ResolveContinuationId(conversationID, connID, "kiro", sessionIdentity.Ephemeral)

	replay := ApplyKiroSessionReplay(KiroSessionReplayOpts{
		ConversationID:       conversationID,
		ConnectionID:         connID,
		ModelID:              upstreamModel,
		SystemPrompt:         systemPrompt,
		ContentPrefix:        contentPrefix,
		CurrentContentPrefix: currentTimeContext,
		History:              history,
		CurrentMessage:       currentMessage,
	})
	replayCurrent := map[string]interface{}{}
	if replay.CurrentMessage != nil {
		replayCurrent = asMap(replay.CurrentMessage["userInputMessage"])
		if replayCurrent == nil {
			replayCurrent = map[string]interface{}{}
		}
	}

	userInputMessage := map[string]interface{}{
		"content": stringOf(replayCurrent["content"]),
		"modelId": upstreamModel,
		"origin":  "AI_EDITOR",
	}
	if ctx := asMap(replayCurrent["userInputMessageContext"]); ctx != nil {
		userInputMessage["userInputMessageContext"] = ctx
	}
	if images := replayCurrent["images"]; images != nil {
		userInputMessage["images"] = images
	}

	payload := map[string]interface{}{
		"conversationState": map[string]interface{}{
			"chatTriggerType":     "MANUAL",
			"conversationId":      conversationID,
			"agentContinuationId": continuationID,
			"agentTaskType":       "vibe",
			"currentMessage": map[string]interface{}{
				"userInputMessage": userInputMessage,
			},
			"history": replay.History,
		},
		"agentMode": "vibe",
	}

	if profileArn != "" {
		payload["profileArn"] = profileArn
	}
	if systemPrompt != "" {
		payload["systemPrompt"] = systemPrompt
	}
	if fields := buildKiroAdditionalModelRequestFieldsForModelBody(body, upstreamModel); fields != nil {
		payload["additionalModelRequestFields"] = fields
	}

	if maxTokens != 0 || temperature != nil || topP != nil {
		ic := map[string]interface{}{}
		if maxTokens != 0 {
			ic["maxTokens"] = maxTokens
		}
		if temperature != nil {
			ic["temperature"] = temperature
		}
		if topP != nil {
			ic["topP"] = topP
		}
		payload["inferenceConfig"] = ic
	}

	return kiroFinalizePayload(payload, upstreamModel)
}
