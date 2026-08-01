package reverseproxy

// 本文件 1:1 迁移自 9router（阶段 5：会话初始化）：
//   - open-sse/utils/sessionManager.js（resolveSessionIdentity /
//     resolveContinuationId / deriveSessionId 等，kiro scope 特判全量）
//   - open-sse/utils/kiroSessionReplay.js（applyKiroSessionReplay：msg0 冻结回放）
//
// JS 用模块级 setInterval + unref 做 TTL 清理；Go 用进程级 goroutine + ticker 等价。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ===== sessionManager.js =====

// KiroSessionIdentity resolveSessionIdentity 返回值。
type KiroSessionIdentity struct {
	SessionID string
	Ephemeral bool
}

// kiroSessionStore 对齐 JS Map：FIFO 淘汰序（Map 插入序）+ 容量上限。
type kiroSessionStore[T any] struct {
	mu    sync.Mutex
	m     map[string]T
	order []string
	max   int
}

func newKiroSessionStore[T any](max int) *kiroSessionStore[T] {
	return &kiroSessionStore[T]{m: map[string]T{}, max: max}
}

func (s *kiroSessionStore[T]) get(key string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}

func (s *kiroSessionStore[T]) set(key string, v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.m[key]; !exists {
		if len(s.order) >= s.max {
			// 淘汰最旧（JS Map.keys().next().value = 首个插入）
			delete(s.m, s.order[0])
			s.order = s.order[1:]
		}
		s.order = append(s.order, key)
	}
	s.m[key] = v
}

// touch 把 key 移到 FIFO 末尾（JS delete+set 语义）。
func (s *kiroSessionStore[T]) touch(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok {
		return
	}
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.order = append(s.order, key)
}

func (s *kiroSessionStore[T]) evictExpired(ttlMs int64, expired func(string, T) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.order {
		if v, ok := s.m[k]; ok && expired(k, v) {
			delete(s.m, k)
		}
	}
}

// startKiroSessionCleanup 进程级 TTL 清理（JS setInterval + unref）。
func startKiroSessionCleanup[T any](store *kiroSessionStore[T], ttlMs int64, intervalMs int64, expired func(string, T) bool) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			store.evictExpired(ttlMs, expired)
		}
	}()
}

type kiroSessionEntry struct {
	SessionID string
	LastUsed  int64
}

var (
	kiroRuntimeSessionStore   = newKiroSessionStore[kiroSessionEntry](1000)
	kiroContinuationStore     = newKiroSessionStore[kiroContinuationEntry](5000)
	kiroAssistantSessionStore = newKiroSessionStore[kiroAssistantEntry](5000)
)

type kiroContinuationEntry struct {
	ContinuationID string
	LastUsed       int64
}

type kiroAssistantEntry struct {
	SessionID string
	LastUsed  int64
}

func init() {
	// runtimeSessionStore 清理（sessionManager.js L19-26）
	startKiroSessionCleanup(kiroRuntimeSessionStore, MemoryConfigSettings.SessionTtlMs, MemoryConfigSettings.SessionCleanupIntervalMs, func(_ string, e kiroSessionEntry) bool {
		return time.Now().UnixMilli()-e.LastUsed > MemoryConfigSettings.SessionTtlMs
	})
	// assistant + continuation 清理（sessionManager.js L258-266）
	startKiroSessionCleanup(kiroAssistantSessionStore, MemoryConfigSettings.SessionTtlMs, MemoryConfigSettings.SessionCleanupIntervalMs, func(_ string, e kiroAssistantEntry) bool {
		return time.Now().UnixMilli()-e.LastUsed > MemoryConfigSettings.SessionTtlMs
	})
	startKiroSessionCleanup(kiroContinuationStore, MemoryConfigSettings.SessionTtlMs, MemoryConfigSettings.SessionCleanupIntervalMs, func(_ string, e kiroContinuationEntry) bool {
		return time.Now().UnixMilli()-e.LastUsed > MemoryConfigSettings.SessionTtlMs
	})
}

// GenerateBinaryStyleID 按二进制格式生成会话 ID（generateBinaryStyleId：
// randomUUID + Date.now()）。
func GenerateBinaryStyleID() string {
	return uuid.NewString() + strconv.FormatInt(time.Now().UnixMilli(), 10)
}

// DeriveSessionID 每个连接一个稳定会话 ID（deriveSessionId；进程生命周期内
// 稳定，容量 1000 上限）。
func DeriveSessionID(connectionId string) string {
	if connectionId == "" {
		return GenerateBinaryStyleID()
	}
	if e, ok := kiroRuntimeSessionStore.get(connectionId); ok {
		e.LastUsed = time.Now().UnixMilli()
		kiroRuntimeSessionStore.set(connectionId, e)
		return e.SessionID
	}
	sessionId := GenerateBinaryStyleID()
	kiroRuntimeSessionStore.set(connectionId, kiroSessionEntry{SessionID: sessionId, LastUsed: time.Now().UnixMilli()})
	return sessionId
}

// ClearSessionStore 清空全部会话存储（clearSessionStore）。
func ClearSessionStore() {
	kiroRuntimeSessionStore.mu.Lock()
	kiroRuntimeSessionStore.m = map[string]kiroSessionEntry{}
	kiroRuntimeSessionStore.order = nil
	kiroRuntimeSessionStore.mu.Unlock()
	kiroAssistantSessionStore.mu.Lock()
	kiroAssistantSessionStore.m = map[string]kiroAssistantEntry{}
	kiroAssistantSessionStore.order = nil
	kiroAssistantSessionStore.mu.Unlock()
	kiroContinuationStore.mu.Lock()
	kiroContinuationStore.m = map[string]kiroContinuationEntry{}
	kiroContinuationStore.order = nil
	kiroContinuationStore.mu.Unlock()
	ClearKiroSessionReplayStore()
}

// sessionManager.js 常量
var (
	kiroSessionHeaderKeys   = []string{"x-session-id", "session-id", "session_id", "x-amp-thread-id"}
	kiroClaudeCodeSessionRE = regexp.MustCompile(`_session_([a-f0-9-]+)$`)
	kiroAntigravityConvRE   = regexp.MustCompile(`(?i)^[a-z]+/([0-9a-f-]{36})/`)
)

const (
	kiroAssistantMinLen = 50
	kiroAssistantCapLen = 50
)

func kiroSha16(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

// normalizeSessionId 归一化会话 ID 候选（trim，空或 >256 返回空串）。
func normalizeSessionId(value string) string {
	if value == "" {
		return ""
	}
	v := strings.TrimSpace(value)
	if v == "" || len(v) > 256 {
		return ""
	}
	return v
}

// extractClaudeCodeSession 从 metadata.user_id 提取 Claude Code 会话
// （_session_{uuid} 或 JSON {session_id}）。
func extractClaudeCodeSession(userId string) string {
	if userId == "" {
		return ""
	}
	if m := kiroClaudeCodeSessionRE.FindStringSubmatch(userId); m != nil {
		return m[1]
	}
	if strings.HasPrefix(userId, "{") {
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(userId), &parsed) == nil {
			return normalizeSessionId(stringOf(parsed["session_id"]))
		}
	}
	return ""
}

// headerValue 小写键头查找（sessionManager.js headerValue：headers[key] ??
// headers[key.toLowerCase()]）。
func headerValue(headers map[string]string, key string) string {
	if headers == nil {
		return ""
	}
	if v, ok := headers[key]; ok {
		return normalizeSessionId(v)
	}
	return normalizeSessionId(headers[strings.ToLower(key)])
}

// extractAntigravitySession 从 body 提取 Antigravity 会话（request.sessionId /
// requestId 内嵌 conversation uuid）。
func extractAntigravitySession(body map[string]interface{}) string {
	if body == nil {
		return ""
	}
	if req := asMap(body["request"]); req != nil {
		if sid := stringOf(req["sessionId"]); sid != "" {
			return normalizeSessionId(sid)
		}
	}
	if rid, ok := body["requestId"].(string); ok {
		if m := kiroAntigravityConvRE.FindStringSubmatch(rid); m != nil {
			return normalizeSessionId(m[1])
		}
	}
	return ""
}

// extractClientSessionId 提取客户端提供的会话 ID（sessionManager.js
// extractClientSessionId；kiro scope 跳过 x-client-request-id 与 metadata.user_id）。
func extractClientSessionId(headers map[string]string, body map[string]interface{}, scope string) string {
	if body != nil {
		if claude := extractClaudeCodeSession(stringOf(nested(body, "metadata", "user_id"))); claude != "" {
			return "claude:" + claude
		}
		if ag := extractAntigravitySession(body); ag != "" {
			return "antigravity:" + ag
		}
	}
	for _, key := range kiroSessionHeaderKeys {
		if v := headerValue(headers, key); v != "" {
			return v
		}
	}
	if scope != "kiro" {
		if v := headerValue(headers, "x-client-request-id"); v != "" {
			return v
		}
	}
	if body != nil {
		if v := normalizeSessionId(stringOf(body["prompt_cache_key"])); v != "" {
			return v
		}
		if v := normalizeSessionId(stringOf(body["session_id"])); v != "" {
			return v
		}
		if v := normalizeSessionId(stringOf(body["conversation_id"])); v != "" {
			return v
		}
		if scope != "kiro" {
			if v := normalizeSessionId(stringOf(nested(body, "metadata", "user_id"))); v != "" {
				return v
			}
		}
	}
	return ""
}

// requestMessages 取 messages 或 input 数组。
func requestMessages(body map[string]interface{}) []interface{} {
	if body == nil {
		return nil
	}
	if msgs := asList(body["messages"]); msgs != nil {
		return msgs
	}
	return asList(body["input"])
}

// accumulateAssistantText 累加 assistant 文本（上限 50 字符即停）。
func accumulateAssistantText(body map[string]interface{}) string {
	items := requestMessages(body)
	var text string
	for _, item := range items {
		im := asMap(item)
		if im == nil {
			continue
		}
		if stringOf(im["role"]) != "assistant" {
			continue
		}
		switch c := im["content"].(type) {
		case string:
			text += c
		case []interface{}:
			for _, part := range c {
				pm := asMap(part)
				if pm == nil {
					continue
				}
				text += stringOf(pm["text"])
				if pm["output"] != nil {
					text += stringOf(pm["output"])
				}
			}
		}
		if len(text) >= kiroAssistantCapLen {
			break
		}
	}
	return text
}

// assistantTextSessionId 基于累加 assistant 文本的稳定会话 ID
// （assistantTextSessionId；kiro scope 不调用）。
func assistantTextSessionId(scope string, body map[string]interface{}) string {
	text := accumulateAssistantText(body)
	if len(text) < kiroAssistantMinLen {
		return ""
	}
	hash := kiroSha16(scope + ":" + text[:kiroAssistantCapLen])
	if e, ok := kiroAssistantSessionStore.get(hash); ok {
		e.LastUsed = time.Now().UnixMilli()
		kiroAssistantSessionStore.set(hash, e)
		return e.SessionID
	}
	sessionId := GenerateBinaryStyleID()
	kiroAssistantSessionStore.set(hash, kiroAssistantEntry{SessionID: sessionId, LastUsed: time.Now().UnixMilli()})
	return sessionId
}

// ResolveSessionIdentity 解析会话稳定的 session id（resolveSessionIdentity）。
// 优先级：客户端会话 → assistant 文本哈希（kiro 跳过）→ workspaceId →
// per-connection（kiro 无头请求每次生成 ephemeral 新会话）。
func ResolveSessionIdentity(headers map[string]string, body map[string]interface{}, connectionId, workspaceId, scope string) KiroSessionIdentity {
	if client := extractClientSessionId(headers, body, scope); client != "" {
		return KiroSessionIdentity{SessionID: client, Ephemeral: false}
	}
	if scope != "kiro" {
		if fromAssistant := assistantTextSessionId(scope+":"+connectionId, body); fromAssistant != "" {
			return KiroSessionIdentity{SessionID: fromAssistant, Ephemeral: false}
		}
	}
	if ws := normalizeSessionId(workspaceId); ws != "" {
		return KiroSessionIdentity{SessionID: ws, Ephemeral: false}
	}
	if scope == "kiro" {
		return KiroSessionIdentity{SessionID: GenerateBinaryStyleID(), Ephemeral: true}
	}
	return KiroSessionIdentity{SessionID: DeriveSessionID(connectionId), Ephemeral: false}
}

// ResolveSessionId 仅取 sessionId（resolveSessionId）。
func ResolveSessionId(headers map[string]string, body map[string]interface{}, connectionId, workspaceId, scope string) string {
	return ResolveSessionIdentity(headers, body, connectionId, workspaceId, scope).SessionID
}

// ResolveContinuationId 会话续延 ID（resolveContinuationId）：ephemeral 每次新
// randomUUID；否则按 scope:connectionId:sessionId 键稳定，容量 5000。
func ResolveContinuationId(sessionId, connectionId, scope string, ephemeral bool) string {
	if ephemeral {
		return uuid.NewString()
	}
	key := scope + ":" + connectionId + ":" + sessionId
	if e, ok := kiroContinuationStore.get(key); ok {
		e.LastUsed = time.Now().UnixMilli()
		kiroContinuationStore.touch(key)
		kiroContinuationStore.set(key, e)
		return e.ContinuationID
	}
	continuationId := uuid.NewString()
	kiroContinuationStore.set(key, kiroContinuationEntry{ContinuationID: continuationId, LastUsed: time.Now().UnixMilli()})
	return continuationId
}

// CaptureSessionId 从请求体 + 凭证提取会话 ID（captureSessionId）。
func CaptureSessionId(body map[string]interface{}, credentials *KiroTranslatorCredentials, connectionId, scope string) string {
	var headers map[string]string
	if credentials != nil {
		headers = credentials.RawHeaders
	}
	return ResolveSessionId(headers, body, connectionId, "", scope)
}

// ===== kiroSessionReplay.js =====

// kiroSessionStartEntry 冻结的会话起点（msg0）。
type kiroSessionStartEntry struct {
	SessionStart map[string]interface{}
	ModelID      string
	SystemPrompt string
	LastUsed     int64
}

var (
	kiroSessionStartStore = newKiroSessionStore[kiroSessionStartEntry](5000)
	kiroSessionReplayMu   sync.Mutex // 保护 store 的 get/set 组合操作
)

func init() {
	startKiroSessionCleanup(kiroSessionStartStore, MemoryConfigSettings.SessionTtlMs, MemoryConfigSettings.SessionCleanupIntervalMs, func(_ string, e kiroSessionStartEntry) bool {
		return time.Now().UnixMilli()-e.LastUsed > MemoryConfigSettings.SessionTtlMs
	})
}

// kiroJSONClone 深拷贝（kiroSessionReplay.js clone：JSON 往返）。
func kiroJSONClone(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out interface{}
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func kiroSessionKey(connectionId, conversationId string) string {
	return connectionId + ":" + conversationId
}

// ensureUserMessageModelId 补 userInputMessage.modelId（ensureUserMessageModelId）。
func ensureUserMessageModelId(message map[string]interface{}, modelId string) map[string]interface{} {
	if message == nil {
		return message
	}
	uim := asMap(message["userInputMessage"])
	if uim != nil && stringOf(uim["modelId"]) == "" && modelId != "" {
		uim["modelId"] = modelId
	}
	return message
}

// ensureHistoryModelIds 历史逐项补 modelId（ensureHistoryModelIds）。
func ensureHistoryModelIds(history []interface{}, modelId string) []interface{} {
	for _, item := range history {
		ensureUserMessageModelId(asMap(item), modelId)
	}
	return history
}

// prefixUserMessage 内容前缀注入（prefixUserMessage：contentPrefix + "\n\n" + content）。
func prefixUserMessage(message map[string]interface{}, contentPrefix, modelId string) map[string]interface{} {
	out := asMap(kiroJSONClone(message))
	if out == nil {
		out = map[string]interface{}{"userInputMessage": map[string]interface{}{"content": ""}}
	}
	if out["userInputMessage"] == nil {
		out["userInputMessage"] = map[string]interface{}{"content": ""}
	}
	ensureUserMessageModelId(out, modelId)
	if contentPrefix != "" {
		uim := asMap(out["userInputMessage"])
		content := stringOf(uim["content"])
		if content != "" {
			uim["content"] = contentPrefix + "\n\n" + content
		} else {
			uim["content"] = contentPrefix
		}
	}
	return out
}

func findFirstUserIndex(history []interface{}) int {
	for i, item := range history {
		if im := asMap(item); im != nil && im["userInputMessage"] != nil {
			return i
		}
	}
	return -1
}

func rememberSessionStart(key string, entry kiroSessionStartEntry) {
	kiroSessionStartStore.set(key, entry)
}

// ClearKiroSessionReplayStore 清空会话起点存储（clearKiroSessionReplayStore）。
func ClearKiroSessionReplayStore() {
	kiroSessionStartStore.mu.Lock()
	kiroSessionStartStore.m = map[string]kiroSessionStartEntry{}
	kiroSessionStartStore.order = nil
	kiroSessionStartStore.mu.Unlock()
}

// KiroSessionReplayOpts applyKiroSessionReplay 参数。
type KiroSessionReplayOpts struct {
	ConversationID       string
	ConnectionID         string
	ModelID              string
	SystemPrompt         string
	ContentPrefix        string
	CurrentContentPrefix string
	History              []interface{}
	CurrentMessage       map[string]interface{}
}

// KiroSessionReplayResult applyKiroSessionReplay 返回值。
type KiroSessionReplayResult struct {
	History        []interface{}
	CurrentMessage map[string]interface{}
	Replayed       bool
}

// ApplyKiroSessionReplay 冻结会话首条用户消息（msg0），后续轮次重放为历史首条，
// 当前时间上下文只注入当前轮（applyKiroSessionReplay）。命中条件：
// modelId 与 systemPrompt 均一致。
func ApplyKiroSessionReplay(opts KiroSessionReplayOpts) KiroSessionReplayResult {
	key := kiroSessionKey(opts.ConnectionID, opts.ConversationID)
	kiroSessionReplayMu.Lock()
	existing, hasExisting := kiroSessionStartStore.get(key)
	kiroSessionReplayMu.Unlock()

	baseHistory := []interface{}{}
	for _, h := range opts.History {
		if c := kiroJSONClone(h); c != nil {
			baseHistory = append(baseHistory, c)
		}
	}
	baseCurrent := asMap(kiroJSONClone(opts.CurrentMessage))
	if baseCurrent == nil {
		baseCurrent = map[string]interface{}{"userInputMessage": map[string]interface{}{"content": ""}}
	}

	if hasExisting && existing.ModelID == opts.ModelID && existing.SystemPrompt == opts.SystemPrompt {
		existing.LastUsed = time.Now().UnixMilli()
		rememberSessionStart(key, existing)
		firstUserIndex := findFirstUserIndex(baseHistory)
		sessionStart := ensureUserMessageModelId(asMap(kiroJSONClone(existing.SessionStart)), opts.ModelID)
		if firstUserIndex >= 0 {
			baseHistory[firstUserIndex] = sessionStart
		} else {
			baseHistory = append([]interface{}{sessionStart}, baseHistory...)
		}
		return KiroSessionReplayResult{
			History:        ensureHistoryModelIds(baseHistory, opts.ModelID),
			CurrentMessage: prefixUserMessage(baseCurrent, opts.CurrentContentPrefix, opts.ModelID),
			Replayed:       true,
		}
	}

	firstUserIndex := findFirstUserIndex(baseHistory)
	var sessionStart map[string]interface{}
	nextCurrent := ensureUserMessageModelId(baseCurrent, opts.ModelID)
	if firstUserIndex >= 0 {
		sessionStart = prefixUserMessage(asMap(baseHistory[firstUserIndex]), opts.ContentPrefix, opts.ModelID)
		baseHistory[firstUserIndex] = kiroJSONClone(sessionStart)
		nextCurrent = prefixUserMessage(baseCurrent, opts.CurrentContentPrefix, opts.ModelID)
	} else {
		sessionStart = prefixUserMessage(baseCurrent, opts.ContentPrefix, opts.ModelID)
		nextCurrent = asMap(kiroJSONClone(sessionStart))
	}

	if opts.ConversationID != "" {
		rememberSessionStart(key, kiroSessionStartEntry{
			SessionStart: asMap(kiroJSONClone(sessionStart)),
			ModelID:      opts.ModelID,
			SystemPrompt: opts.SystemPrompt,
			LastUsed:     time.Now().UnixMilli(),
		})
	}

	return KiroSessionReplayResult{
		History:        ensureHistoryModelIds(baseHistory, opts.ModelID),
		CurrentMessage: nextCurrent,
		Replayed:       false,
	}
}
