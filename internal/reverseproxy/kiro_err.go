package reverseproxy

// 本文件 1:1 迁移自 9router open-sse/config/errorConfig.js 与
// open-sse/config/runtimeConfig.js 的 Kiro 相关配置（阶段 2：错误映射规则 + 重试冷却配置）。
// 数值、枚举、匹配顺序与 JS 原版逐项对齐。
//
// 非 Kiro 相关项（SEARXNG_URL、GEMINI_NATIVE_TTS_FETCH_TIMEOUT_MS 等）不在迁移范围，已剔除。

import (
	"os"
	"strconv"
)

// ErrorType OpenAI 兼容错误类型映射（面向客户端）。
type ErrorType struct {
	Type string // type
	Code string // code
}

// ErrorTypes 状态码 → OpenAI 兼容错误类型映射（ERROR_TYPES）。
var ErrorTypes = map[int]ErrorType{
	400: {Type: "invalid_request_error", Code: "bad_request"},
	401: {Type: "authentication_error", Code: "invalid_api_key"},
	402: {Type: "billing_error", Code: "payment_required"},
	403: {Type: "permission_error", Code: "insufficient_quota"},
	404: {Type: "invalid_request_error", Code: "model_not_found"},
	406: {Type: "invalid_request_error", Code: "model_not_supported"},
	429: {Type: "rate_limit_error", Code: "rate_limit_exceeded"},
	500: {Type: "server_error", Code: "internal_server_error"},
	502: {Type: "server_error", Code: "bad_gateway"},
	503: {Type: "server_error", Code: "service_unavailable"},
	504: {Type: "server_error", Code: "gateway_timeout"},
}

// DefaultErrorMessages 每个状态码的默认错误文案（面向客户端）。
var DefaultErrorMessages = map[int]string{
	400: "Bad request",
	401: "Invalid API key provided",
	402: "Payment required",
	403: "You exceeded your current quota",
	404: "Model not found",
	406: "Model not supported",
	429: "Rate limit exceeded",
	500: "Internal server error",
	502: "Bad gateway - upstream provider error",
	503: "Service temporarily unavailable",
	504: "Gateway timeout",
}

// BackoffConfig 限流的指数退避配置（BACKOFF_CONFIG）。
type BackoffConfig struct {
	Base     int // 基础退避 ms
	Max      int // 退避上限 ms
	MaxLevel int // 退避级别上限
}

// Backoff 限流指数退避配置：2000ms 底、300s 顶、级别上限 15。
var Backoff = BackoffConfig{
	Base:     2000,
	Max:      5 * 60 * 1000,
	MaxLevel: 15,
}

// TransientCooldownMs 瞬态/未知错误的默认冷却（ms）。
const TransientCooldownMs = 30 * 1000

// MaxRateLimitCooldownMs 提供商上报限流冷却的硬上限（ms）。
// 例如 codex 的 resets_at 可达 5-6 小时。
const MaxRateLimitCooldownMs = 30 * 60 * 1000

// 冷却时长（ms），对应 JS 未导出的 COOLDOWN 对象。
const (
	cooldownLongMs  = 2 * 60 * 1000
	cooldownShortMs = 5 * 1000
)

// ErrorRule 统一错误分类规则条目。
// 自上而下检查：先文本规则（按顺序，优先级即顺序），后状态码规则。
//   - Text: 错误文案上的子串匹配（大小写不敏感）
//   - Status: HTTP 状态码匹配
//   - CooldownMs: 固定冷却时长
//   - Backoff: true 时使用指数退避（限流）
type ErrorRule struct {
	Text       string
	Status     int
	CooldownMs int
	Backoff    bool
}

// ErrorRules 统一错误分类规则表（ERROR_RULES），顺序即匹配优先级，禁止重排。
var ErrorRules = []ErrorRule{
	// --- 文本规则（先检查，顺序即优先级） ---
	{Text: "no credentials", CooldownMs: cooldownLongMs},
	{Text: "request not allowed", CooldownMs: cooldownShortMs},
	{Text: "improperly formed request", CooldownMs: cooldownLongMs},
	{Text: "rate limit", Backoff: true},
	{Text: "too many requests", Backoff: true},
	{Text: "quota exceeded", Backoff: true},
	{Text: "capacity", Backoff: true},
	{Text: "overloaded", Backoff: true},

	// --- 状态码规则（文本未匹配时的兜底） ---
	{Status: 401, CooldownMs: cooldownLongMs},
	{Status: 402, CooldownMs: cooldownLongMs},
	{Status: 403, CooldownMs: cooldownLongMs},
	{Status: 404, CooldownMs: cooldownLongMs},
	{Status: 429, Backoff: true},
}

// CooldownMs 向后兼容的冷却映射（COOLDOWN_MS，JS 由 index.js 再导出）。
var CooldownMs = map[string]int{
	"unauthorized":      cooldownLongMs,
	"paymentRequired":   cooldownLongMs,
	"notFound":          cooldownLongMs,
	"transient":         TransientCooldownMs,
	"requestNotAllowed": cooldownShortMs,
}

// ===== runtimeConfig.js（Kiro 相关部分） =====

// HTTP 状态码（HTTP_STATUS）。
const (
	HTTPStatusBadRequest         = 400
	HTTPStatusUnauthorized       = 401
	HTTPStatusPaymentRequired    = 402
	HTTPStatusForbidden          = 403
	HTTPStatusNotFound           = 404
	HTTPStatusNotAcceptable      = 406
	HTTPStatusRequestTimeout     = 408
	HTTPStatusRateLimited        = 429
	HTTPStatusServerError        = 500
	HTTPStatusBadGateway         = 502
	HTTPStatusServiceUnavailable = 503
	HTTPStatusGatewayTimeout     = 504
)

// CacheTTL 缓存 TTL（秒）。
type CacheTTL struct {
	UserInfo   int // 5 分钟
	ModelAlias int // 1 小时
}

// CacheTTLConfig 缓存 TTL 配置（CACHE_TTL）。
var CacheTTLConfig = CacheTTL{
	UserInfo:   300,
	ModelAlias: 3600,
}

// MemoryConfig 内存管理配置（MEMORY_CONFIG）。
type MemoryConfig struct {
	SessionTtlMs             int64 // 会话 TTL
	SessionCleanupIntervalMs int64 // 会话清理周期
	DNSCacheTtlMs            int64 // DNS 缓存 TTL
	ProxyDispatchersMaxSize  int   // 代理分发器上限
}

// MemoryConfigSettings 内存管理配置（MEMORY_CONFIG）。
var MemoryConfigSettings = MemoryConfig{
	SessionTtlMs:             2 * 60 * 60 * 1000,
	SessionCleanupIntervalMs: 30 * 60 * 1000,
	DNSCacheTtlMs:            5 * 60 * 1000,
	ProxyDispatchersMaxSize:  20,
}

// envMs 解析正整数环境变量覆盖，失败回退默认值（envMs）。
func envMs(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// StreamStallTimeoutMs 块间停顿超时（token 已流动后）。预留充足余量，
// 避免慢推理模型在流中途被中止。Env: STREAM_STALL_TIMEOUT_MS。
var StreamStallTimeoutMs = envMs("STREAM_STALL_TIMEOUT_MS", 360*1000)

// StreamFirstChunkTimeoutMs 首 token 超时（prompt 预填充）。Env: STREAM_FIRST_CHUNK_TIMEOUT_MS。
var StreamFirstChunkTimeoutMs = envMs("STREAM_FIRST_CHUNK_TIMEOUT_MS", 200*1000)

// FetchConnectTimeoutMs 连接超时：上游在此时长内未返回响应头则中止。Env: FETCH_CONNECT_TIMEOUT_MS。
var FetchConnectTimeoutMs = envMs("FETCH_CONNECT_TIMEOUT_MS", 60*1000)

// 默认 token 上限。
const (
	DefaultMaxTokens = 64000
	DefaultMinTokens = 32000
)

// TokenSaverHeader RTK token 节省器请求头名。
const TokenSaverHeader = "x-9router-token-saver"

// RetryConfig 429 响应的重试配置（RETRY_CONFIG，遗留，向后兼容保留）。
type RetryConfig struct {
	MaxAttempts int
	DelayMs     int
}

// RetryConfigDefault 429 重试配置（RETRY_CONFIG）。
var RetryConfigDefault = RetryConfig{
	MaxAttempts: 2,
	DelayMs:     2000,
}

// RetryEntry 单条重试配置：{ attempts, delayMs }。
type RetryEntry struct {
	Attempts int
	DelayMs  int
}

// DefaultRetryConfig 按状态码的默认重试配置（DEFAULT_RETRY_CONFIG）。
// 注意 429 默认不重试（Kiro registry 覆盖为 0）。
var DefaultRetryConfig = map[int]RetryEntry{
	429: {Attempts: 0, DelayMs: 0},
	502: {Attempts: 3, DelayMs: 3000},
	503: {Attempts: 3, DelayMs: 2000},
	504: {Attempts: 2, DelayMs: 3000},
}

// ResolveRetryEntry 归一化一条重试配置为 { attempts, delayMs }（resolveRetryEntry）。
//   - nil → { attempts: 0, delayMs: RETRY_CONFIG.delayMs }
//   - 数字 → { attempts: entry, delayMs: RETRY_CONFIG.delayMs }
//   - 对象 → { attempts: entry.attempts || 0, delayMs: entry.delayMs != null ? entry.delayMs : RETRY_CONFIG.delayMs }
func ResolveRetryEntry(entry interface{}) RetryEntry {
	if entry == nil {
		return RetryEntry{Attempts: 0, DelayMs: RetryConfigDefault.DelayMs}
	}
	switch v := entry.(type) {
	case int:
		return RetryEntry{Attempts: v, DelayMs: RetryConfigDefault.DelayMs}
	case float64: // JSON 数字
		return RetryEntry{Attempts: int(v), DelayMs: RetryConfigDefault.DelayMs}
	case map[string]interface{}:
		var attempts int
		if a, ok := v["attempts"].(float64); ok {
			attempts = int(a)
		}
		e := RetryEntry{Attempts: attempts}
		if d, ok := v["delayMs"]; ok && d != nil {
			if df, ok := d.(float64); ok {
				e.DelayMs = int(df)
			}
		} else {
			e.DelayMs = RetryConfigDefault.DelayMs
		}
		return e
	case RetryEntry:
		return v
	case *RetryEntry:
		if v == nil {
			return RetryEntry{Attempts: 0, DelayMs: RetryConfigDefault.DelayMs}
		}
		return *v
	}
	return RetryEntry{Attempts: 0, DelayMs: RetryConfigDefault.DelayMs}
}

// SkipPatterns 包含这些文本的请求将绕过提供商（SKIP_PATTERNS）。
var SkipPatterns = []string{
	"Please write a 5-10 word title for the following conversation:",
}
