package reverseproxy

// 本文件 1:1 迁移自 9router open-sse/services/accountFallback.js（阶段 6）：
// 错误分类/冷却数学、模型锁、账号状态机。规则数据（ERROR_RULES/BACKOFF_CONFIG/
// TRANSIENT_COOLDOWN_MS）见 kiro_err.go（阶段 2 已迁）。
//
// JS 时间以 ISO 字符串传递；Go 统一 time.Time（语义等价）。

import (
	"strings"
	"time"
)

// MODEL_LOCK_PREFIX 连接记录上模型锁扁平字段的前缀。
const MODEL_LOCK_PREFIX = "modelLock_"

// MODEL_LOCK_ALL 未知模型时的特殊键（账号级锁）。
const MODEL_LOCK_ALL = MODEL_LOCK_PREFIX + "__all"

// KiroQuotaCooldown 计算限流（429）的指数退避冷却（getQuotaCooldown）。
// 数学对齐 JS：level = max(0, backoffLevel-1)；cooldown = base * 2^level；
// 上限 BACKOFF_CONFIG.max（300000ms）。
func KiroQuotaCooldown(backoffLevel int) int64 {
	level := backoffLevel - 1
	if level < 0 {
		level = 0
	}
	cooldown := int64(Backoff.Base) << uint(level)
	if cooldown > int64(Backoff.Max) {
		cooldown = int64(Backoff.Max)
	}
	return cooldown
}

// KiroFallbackResult checkFallbackError 返回值。
type KiroFallbackResult struct {
	ShouldFallback     bool
	CooldownMs         int64
	NewBackoffLevel    int
	HasNewBackoffLevel bool
}

// KiroCheckFallbackError 判断错误是否应触发账号回退（checkFallbackError）。
// 按 ERROR_RULES 自上而下匹配：先文本规则（顺序即优先级），后状态码规则；
// backoff 规则 → 新级别 min(level+1, 15) + 指数退避冷却；固定规则 → 固定冷却；
// 未匹配 → 恒回退 + 瞬态冷却 30s。
func KiroCheckFallbackError(status int, errorText string, backoffLevel int) KiroFallbackResult {
	lowerError := ""
	if errorText != "" {
		lowerError = strings.ToLower(errorText)
	}

	for _, rule := range ErrorRules {
		// 文本规则：错误文案子串匹配（大小写不敏感）
		if rule.Text != "" && lowerError != "" && strings.Contains(lowerError, rule.Text) {
			if rule.Backoff {
				newLevel := backoffLevel + 1
				if newLevel > Backoff.MaxLevel {
					newLevel = Backoff.MaxLevel
				}
				return KiroFallbackResult{
					ShouldFallback:     true,
					CooldownMs:         KiroQuotaCooldown(newLevel),
					NewBackoffLevel:    newLevel,
					HasNewBackoffLevel: true,
				}
			}
			return KiroFallbackResult{ShouldFallback: true, CooldownMs: int64(rule.CooldownMs)}
		}

		// 状态码规则：HTTP 状态码匹配
		if rule.Status != 0 && rule.Status == status {
			if rule.Backoff {
				newLevel := backoffLevel + 1
				if newLevel > Backoff.MaxLevel {
					newLevel = Backoff.MaxLevel
				}
				return KiroFallbackResult{
					ShouldFallback:     true,
					CooldownMs:         KiroQuotaCooldown(newLevel),
					NewBackoffLevel:    newLevel,
					HasNewBackoffLevel: true,
				}
			}
			return KiroFallbackResult{ShouldFallback: true, CooldownMs: int64(rule.CooldownMs)}
		}
	}

	// 默认：未匹配错误走瞬态冷却
	return KiroFallbackResult{ShouldFallback: true, CooldownMs: TransientCooldownMs}
}

// KiroIsAccountUnavailable 账号是否处于冷却中（isAccountUnavailable：
// 无 until 视为可用；until > now 不可用）。
func KiroIsAccountUnavailable(unavailableUntil time.Time, now time.Time) bool {
	if unavailableUntil.IsZero() {
		return false
	}
	return unavailableUntil.After(now)
}

// KiroGetUnavailableUntil 计算冷却截止时刻（getUnavailableUntil）。
func KiroGetUnavailableUntil(cooldownMs int64, now time.Time) time.Time {
	return now.Add(time.Duration(cooldownMs) * time.Millisecond)
}

// KiroGetEarliestRateLimitedUntil 取账号列表中最早的未来 rateLimitedUntil
// （getEarliestRateLimitedUntil；JS 返回 ISO|null，Go 返回 (time, bool)）。
func KiroGetEarliestRateLimitedUntil(accounts []*KiroFallbackAccount, now time.Time) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, acc := range accounts {
		if acc == nil || acc.RateLimitedUntil.IsZero() {
			continue
		}
		if !acc.RateLimitedUntil.After(now) {
			continue
		}
		if !found || acc.RateLimitedUntil.Before(earliest) {
			earliest = acc.RateLimitedUntil
			found = true
		}
	}
	return earliest, found
}

// KiroFormatRetryAfter 格式化为可读的 "reset after Xm Ys"（formatRetryAfter）。
func KiroFormatRetryAfter(rateLimitedUntil time.Time, now time.Time) string {
	if rateLimitedUntil.IsZero() {
		return ""
	}
	diffMs := rateLimitedUntil.Sub(now).Milliseconds()
	if diffMs <= 0 {
		return "reset after 0s"
	}
	totalSec := (diffMs + 999) / 1000 // ceil(diffMs/1000)
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60
	parts := []string{}
	if h > 0 {
		parts = append(parts, itoa(int(h))+"h")
	}
	if m > 0 {
		parts = append(parts, itoa(int(m))+"m")
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, itoa(int(s))+"s")
	}
	return "reset after " + strings.Join(parts, " ")
}

// KiroGetModelLockKey 构建模型锁扁平字段键（getModelLockKey：
// model 为空 → MODEL_LOCK_ALL）。
func KiroGetModelLockKey(model string) string {
	if model != "" {
		return MODEL_LOCK_PREFIX + model
	}
	return MODEL_LOCK_ALL
}

// KiroIsModelLockActive 模型锁是否仍生效（isModelLockActive：
// 读 modelLock_{model}（或 modelLock___all 兜底），过期即失效）。
func KiroIsModelLockActive(locks map[string]time.Time, model string, now time.Time) bool {
	key := KiroGetModelLockKey(model)
	expiry := locks[key]
	if expiry.IsZero() {
		expiry = locks[MODEL_LOCK_ALL]
	}
	if expiry.IsZero() {
		return false
	}
	return expiry.After(now)
}

// KiroGetEarliestModelLockUntil 全部 modelLock_* 字段中最早的未过期锁
// （getEarliestModelLockUntil；UI 冷却展示用）。
func KiroGetEarliestModelLockUntil(locks map[string]time.Time, now time.Time) (time.Time, bool) {
	var earliest time.Time
	found := false
	for key, val := range locks {
		if !strings.HasPrefix(key, MODEL_LOCK_PREFIX) || val.IsZero() {
			continue
		}
		if !val.After(now) {
			continue
		}
		if !found || val.Before(earliest) {
			earliest = val
			found = true
		}
	}
	return earliest, found
}

// KiroBuildModelLockUpdate 构建设置模型锁的更新（buildModelLockUpdate）。
func KiroBuildModelLockUpdate(model string, cooldownMs int64, now time.Time) map[string]time.Time {
	key := KiroGetModelLockKey(model)
	return map[string]time.Time{key: now.Add(time.Duration(cooldownMs) * time.Millisecond)}
}

// KiroBuildClearModelLocksUpdate 构建清除全部模型锁的更新
// （buildClearModelLocksUpdate；JS 置 null，Go 置零值）。
func KiroBuildClearModelLocksUpdate(locks map[string]time.Time) map[string]time.Time {
	cleared := map[string]time.Time{}
	for key := range locks {
		if strings.HasPrefix(key, MODEL_LOCK_PREFIX) {
			cleared[key] = time.Time{}
		}
	}
	return cleared
}

// KiroFallbackAccount 对应 JS 的账号/连接对象（accountFallback 用字段）。
type KiroFallbackAccount struct {
	ID               string
	RateLimitedUntil time.Time
	BackoffLevel     int
	LastError        *KiroLastError
	Status           string
	ModelLocks       map[string]time.Time
}

// KiroLastError 账号最后错误（applyErrorState 写入）。
type KiroLastError struct {
	Status    int
	Message   string
	Timestamp time.Time
}

// KiroFilterAvailableAccounts 过滤可用账号（filterAvailableAccounts：
// 排除 excludeId；冷却未过期的不可用）。
func KiroFilterAvailableAccounts(accounts []*KiroFallbackAccount, excludeId string, now time.Time) []*KiroFallbackAccount {
	var out []*KiroFallbackAccount
	for _, acc := range accounts {
		if acc == nil {
			continue
		}
		if excludeId != "" && acc.ID == excludeId {
			continue
		}
		if !acc.RateLimitedUntil.IsZero() && acc.RateLimitedUntil.After(now) {
			continue
		}
		out = append(out, acc)
	}
	return out
}

// KiroResetAccountState 请求成功时重置账号状态（resetAccountState：
// 清冷却、退避级别归零、清错误、status=active）。
func KiroResetAccountState(account *KiroFallbackAccount) *KiroFallbackAccount {
	if account == nil {
		return account
	}
	account.RateLimitedUntil = time.Time{}
	account.BackoffLevel = 0
	account.LastError = nil
	account.Status = "active"
	return account
}

// KiroApplyErrorState 对账号应用错误状态（applyErrorState）：
// rateLimitedUntil = now + cooldown（cooldown>0 时）；backoffLevel 取新级别
// （无则保留旧值）；lastError 记录 {status, message, timestamp}；status=error。
func KiroApplyErrorState(account *KiroFallbackAccount, status int, errorText string, now time.Time) *KiroFallbackAccount {
	if account == nil {
		return account
	}

	backoffLevel := account.BackoffLevel
	res := KiroCheckFallbackError(status, errorText, backoffLevel)

	if res.CooldownMs > 0 {
		account.RateLimitedUntil = KiroGetUnavailableUntil(res.CooldownMs, now)
	} else {
		account.RateLimitedUntil = time.Time{}
	}
	if res.HasNewBackoffLevel {
		account.BackoffLevel = res.NewBackoffLevel
	} else {
		account.BackoffLevel = backoffLevel
	}
	account.LastError = &KiroLastError{
		Status:    status,
		Message:   errorText,
		Timestamp: now,
	}
	account.Status = "error"
	return account
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
