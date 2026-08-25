package reverseproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// ===== 冷却/退避常量（9router open-sse/config/errorConfig.js 对齐）=====
// 注意：退避数学与错误分类已统一到 kiro_err.go（Backoff）与 kiro_fallback.go
// （KiroCheckFallbackError），此处仅保留账号级锁（Suspend）使用的固定冷却。
const (
	fixedCooldownMs     = 120000 // 401/402/403/404
	refreshLeadSec      = 300    // TOKEN_EXPIRY_BUFFER_MS = 5min
	tokenDefaultExpireS = 3600   // expiresIn 缺省（9router testUtils 默认值）
	dedupResultTTL      = 10 * time.Second
)

type Account struct {
	ClientID        string   `json:"clientId"`
	ClientSecret    string   `json:"clientSecret"`
	RefreshToken    string   `json:"refreshToken"`
	Region          string   `json:"region"`
	Email           string   `json:"email"`
	Subscription    string   `json:"subscription"`
	CreditLimit     float64  `json:"creditLimit"`
	CreditUsed      float64  `json:"creditUsed"`
	Provider        string   `json:"provider"`
	Time            string   `json:"time"`
	Proxy           string   `json:"proxy,omitempty"`           // per-account proxy URL
	ProfileArn      string   `json:"profileArn,omitempty"`      // CodeWhisperer profile ARN
	AvailableModels []string `json:"availableModels,omitempty"` // available model IDs

	accessToken          string
	accessTokenExpiresAt int64 // unix nano; 0 = unknown（下次请求时刷新）
	concurrency          int
	suspended            bool
	cooldownUntil        int64            // unix nano; 账号级锁（token 刷新失败等），跳过条件同 9router
	backoffLevel         int              // 指数退避级别（成功清零，per-connection 同 9router）
	lastErrorCode        int              // 最近一次失败状态码（全锁时透传）
	modelLocks           map[string]int64 // model -> 锁到期 unix nano（9router modelLock_<model>）

	quotaExhaustedUntil int64  // unix nano; 额度耗尽后直到重置时间，0=未耗尽
	quotaResetAt        string // 上游返回的 nextDateReset 原始值，用于展示
	quotaCheckedAt      int64  // unix nano; 上次检查额度的时间，避免频繁调 API
}

// AccessToken 返回缓存的 access token（可能为空）。
func (a *Account) AccessToken() string { return a.accessToken }

// SetAccessToken 缓存 access token。
func (a *Account) SetAccessToken(t string) { a.accessToken = t }

// TokenExpiresAt 返回缓存 token 的过期时刻（unix nano；0 = 未知）。
func (a *Account) TokenExpiresAt() int64 { return a.accessTokenExpiresAt }

// SetTokenExpiresAt 记录缓存 token 的过期时刻（unix nano）。
func (a *Account) SetTokenExpiresAt(t int64) { a.accessTokenExpiresAt = t }

type AccountPool struct {
	mu       sync.RWMutex
	accounts []*Account
	path     string

	nextIdx          int
	maxAccountConc   int
	totalConcurrency int

	refreshFunc func(acc *Account) (string, error)
	quotaFunc   QuotaRefreshFunc // 9router 同款：定时刷新额度
}

func NewAccountPool(path string, maxAccountConc, totalConc int, refresh func(*Account) (string, error)) *AccountPool {
	return &AccountPool{
		path:             path,
		maxAccountConc:   maxAccountConc,
		totalConcurrency: totalConc,
		refreshFunc:      refresh,
	}
}

// SetQuotaFunc 设置额度刷新回调（9router 同款：GetUsageLimits 定时刷新）
func (p *AccountPool) SetQuotaFunc(fn QuotaRefreshFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quotaFunc = fn
}

// RefreshQuota 遍历所有账号刷新额度信息（9router 同款：getKiroUsage 定时任务）
func (p *AccountPool) RefreshQuota() {
	p.mu.RLock()
	quotaFunc := p.quotaFunc
	accounts := make([]*Account, len(p.accounts))
	copy(accounts, p.accounts)
	p.mu.RUnlock()

	if quotaFunc == nil {
		return
	}

	for _, acc := range accounts {
		// 跳过刚检查过的（<60s）
		p.mu.RLock()
		lastCheck := acc.quotaCheckedAt
		p.mu.RUnlock()
		if lastCheck > 0 && time.Now().UnixNano()-lastCheck < 60*1e9 {
			continue
		}

		token := acc.accessToken
		if token == "" {
			// 尝试刷新 token
			if p.refreshFunc != nil {
				var err error
				token, err = p.refreshFunc(acc)
				if err != nil {
					continue
				}
				acc.SetAccessToken(token)
			} else {
				continue
			}
		}

		proxy := acc.Proxy
		region := acc.Region
		if region == "" {
			region = "us-east-1"
		}
		arn := EffectiveProfileArn(acc.ProfileArn)
		if arn == "" {
			if resolved, err := ListAvailableProfiles(token, region, proxy); err == nil {
				arn = EffectiveProfileArn(resolved)
				if arn != "" {
					acc.ProfileArn = arn
				}
			}
		}
		used, limit, resetAt, err := quotaFunc(token, region, proxy, UsageQueryProfileArn(arn))
		if err != nil {
			continue
		}
		p.UpdateQuota(acc, used, limit, resetAt)
	}
}

func (p *AccountPool) Load() error {
	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read accounts: %w", err)
	}
	var list []*Account
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parse accounts: %w", err)
	}
	p.mu.Lock()
	p.accounts = list
	p.mu.Unlock()
	return nil
}

func (p *AccountPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.accounts)
}

func (p *AccountPool) ActiveCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := 0
	for _, a := range p.accounts {
		if !a.suspended {
			n++
		}
	}
	return n
}

// Acquire 按轮询取一个可用账号：跳过挂起、账号级锁、该模型锁（9router modelLock_<model>）
// 额度耗尽（quotaExhaustedUntil > now）、并发上限，round-robin。全不可用返回 nil。
func (p *AccountPool) Acquire(model string) *Account {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check total concurrency cap
	if p.totalConcurrency > 0 {
		total := 0
		for _, a := range p.accounts {
			total += a.concurrency
		}
		if total >= p.totalConcurrency {
			return nil
		}
	}

	now := time.Now().UnixNano()
	for i := 0; i < len(p.accounts); i++ {
		idx := (p.nextIdx + i) % len(p.accounts)
		acc := p.accounts[idx]
		if acc.suspended {
			continue
		}
		if acc.cooldownUntil > 0 {
			if now < acc.cooldownUntil {
				continue
			}
			acc.cooldownUntil = 0 // cooldown expired, account usable again
		}
		// 额度耗尽：跳过直到重置时间（9router 同款：resetsAtMs 精确冷却）
		if acc.quotaExhaustedUntil > 0 && now < acc.quotaExhaustedUntil {
			continue
		}
		if acc.quotaExhaustedUntil > 0 && now >= acc.quotaExhaustedUntil {
			acc.quotaExhaustedUntil = 0 // 额度已重置，恢复可用
			acc.CreditUsed = 0
		}
		if len(acc.modelLocks) > 0 {
			if lockUntil, locked := acc.modelLocks[model]; locked && now < lockUntil {
				continue
			}
		}
		if acc.concurrency >= p.maxAccountConc {
			continue
		}
		acc.concurrency++
		p.nextIdx = (idx + 1) % len(p.accounts)
		return acc
	}
	return nil
}

// MarkQuotaExhausted 标记账号额度耗尽，直到 resetAt 时间或默认 30min 后自动恢复。
// 9router MAX_RATE_LIMIT_COOLDOWN_MS = 30min，对齐上游 resetsAtMs 精确冷却。
func (p *AccountPool) MarkQuotaExhausted(acc *Account, resetAt string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acc.quotaResetAt = resetAt
	// 解析重置时间
	if resetAt != "" {
		if t, err := time.Parse(time.RFC3339, resetAt); err == nil {
			until := t.UnixNano()
			maxWait := time.Now().Add(30 * time.Minute).UnixNano()
			if until > maxWait {
				until = maxWait
			}
			acc.quotaExhaustedUntil = until
			return
		}
	}
	// 无有效重置时间，30min 默认冷却
	acc.quotaExhaustedUntil = time.Now().Add(30 * time.Minute).UnixNano()
}

// ClearQuotaExhausted 清除额度耗尽标记（手动刷新后调用）
func (p *AccountPool) ClearQuotaExhausted(acc *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acc.quotaExhaustedUntil = 0
	acc.quotaResetAt = ""
}

// UpdateQuota 更新账号额度信息（从 GetUsageLimits API 刷新后调用）
func (p *AccountPool) UpdateQuota(acc *Account, creditUsed, creditLimit float64, resetAt string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acc.CreditUsed = creditUsed
	acc.CreditLimit = creditLimit
	acc.quotaCheckedAt = time.Now().UnixNano()
	if creditLimit > 0 && creditUsed >= creditLimit {
		acc.quotaResetAt = resetAt
		if resetAt != "" {
			if t, err := time.Parse(time.RFC3339, resetAt); err == nil {
				until := t.UnixNano()
				maxWait := time.Now().Add(30 * time.Minute).UnixNano()
				if until > maxWait {
					until = maxWait
				}
				acc.quotaExhaustedUntil = until
				return
			}
		}
		acc.quotaExhaustedUntil = time.Now().Add(30 * time.Minute).UnixNano()
	} else {
		acc.quotaExhaustedUntil = 0
		acc.quotaResetAt = ""
	}
}

func (p *AccountPool) Release(acc *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acc.concurrency--
	if acc.concurrency < 0 {
		acc.concurrency = 0
	}
}

// MarkUnavailable 标记账号失败并上模型锁，返回冷却时长（9router markAccountUnavailable）。
// 分类统一走 KiroCheckFallbackError（ERROR_RULES 顺序匹配：文本规则优先、
// 429 指数退避、401/402/403/404 固定 2min、未匹配 30s 瞬态）。锁按模型隔离，
// 不阻塞其他模型请求。
func (p *AccountPool) MarkUnavailable(acc *Account, model string, status int, errText string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	res := KiroCheckFallbackError(status, errText, acc.backoffLevel)
	var cd time.Duration
	if res.CooldownMs > 0 {
		cd = time.Duration(res.CooldownMs) * time.Millisecond
	}
	if res.HasNewBackoffLevel {
		acc.backoffLevel = res.NewBackoffLevel
	}
	if acc.modelLocks == nil {
		acc.modelLocks = map[string]int64{}
	}
	acc.modelLocks[model] = time.Now().Add(cd).UnixNano()
	if status != 0 {
		acc.lastErrorCode = status
	}
	return cd
}

// MarkSuccess 请求成功：清该模型锁；无活跃锁时退避级别清零（9router clearAccountError）。
func (p *AccountPool) MarkSuccess(acc *Account, model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(acc.modelLocks) > 0 {
		delete(acc.modelLocks, model)
	}
	now := time.Now().UnixNano()
	for _, exp := range acc.modelLocks {
		if now < exp {
			return // 仍有活跃锁，退避级别保留
		}
	}
	acc.backoffLevel = 0
}

// EarliestModelLock 返回该模型最早到期的锁时刻（unix nano；含账号级锁、额度耗尽），全解锁返回 0。
func (p *AccountPool) EarliestModelLock(model string) int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now().UnixNano()
	var earliest int64
	for _, a := range p.accounts {
		if a.cooldownUntil > now && (earliest == 0 || a.cooldownUntil < earliest) {
			earliest = a.cooldownUntil
		}
		if a.quotaExhaustedUntil > now && (earliest == 0 || a.quotaExhaustedUntil < earliest) {
			earliest = a.quotaExhaustedUntil
		}
		if lockUntil, locked := a.modelLocks[model]; locked && lockUntil > now && (earliest == 0 || lockUntil < earliest) {
			earliest = lockUntil
		}
	}
	return earliest
}

// Accounts 返回账号指针快照（读锁内拷贝，锁外使用）。
func (p *AccountPool) Accounts() []*Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Account, len(p.accounts))
	copy(out, p.accounts)
	return out
}

// Suspend puts an account into account-wide cooldown (2 minutes: bad tokens /
// refresh failures). After the cooldown it is usable again automatically.
func (p *AccountPool) Suspend(acc *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acc.suspended = false
	acc.concurrency = 0
	acc.cooldownUntil = time.Now().Add(fixedCooldownMs * time.Millisecond).UnixNano()
}

// Unsuspend restores a suspended account if its token has expired (can be retried).
func (p *AccountPool) Unsuspend(acc *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acc.suspended = false
}

// ForEach iterates over all accounts (read-only).
func (p *AccountPool) ForEach(fn func(*Account)) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, a := range p.accounts {
		fn(a)
	}
}

func (p *AccountPool) List() []*Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Account, len(p.accounts))
	for i, a := range p.accounts {
		out[i] = &Account{
			ClientID:             a.ClientID,
			ClientSecret:         a.ClientSecret,
			RefreshToken:         a.RefreshToken,
			Region:               a.Region,
			Email:                a.Email,
			Subscription:         a.Subscription,
			CreditLimit:          a.CreditLimit,
			CreditUsed:           a.CreditUsed,
			Provider:             a.Provider,
			Time:                 a.Time,
			Proxy:                a.Proxy,
			ProfileArn:           a.ProfileArn,
			AvailableModels:      a.AvailableModels,
			concurrency:          a.concurrency,
			suspended:            a.suspended,
			backoffLevel:         a.backoffLevel,
			lastErrorCode:        a.lastErrorCode,
			cooldownUntil:        a.cooldownUntil,
			accessTokenExpiresAt: a.accessTokenExpiresAt,
			quotaExhaustedUntil:  a.quotaExhaustedUntil,
			quotaResetAt:         a.quotaResetAt,
			quotaCheckedAt:       a.quotaCheckedAt,
		}
	}
	return out
}
