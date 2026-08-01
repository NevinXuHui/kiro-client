package reverseproxy

// 本文件 1:1 迁移自 9router open-sse/services/oauthCredentialManager.js（阶段 3）：
// 凭证过期判定、刷新结果合并、并发锁。JS 中 expiresAt/lastRefreshAt 存 ISO 字符串，
// Go 统一用 epoch 毫秒（int64）表示，语义等价。

import (
	"sync"
	"time"
)

// KiroCredential 对应 JS credentials 对象（kiro 相关字段全集）。
// 锁键字段（ConnectionId/Id/Email/Name）与令牌字段分离存放。
type KiroCredential struct {
	// 刷新锁稳定键（JS getRefreshLockKey 用）
	ConnectionId string
	Id           string
	Email        string
	Name         string

	AccessToken           string
	ApiKey                string
	Token                 string
	RefreshToken          string
	IdToken               string
	ExpiresIn             int
	ExpiresAtMs           int64 // JS expiresAt / tokenExpiresAt（epoch ms，0=未知）
	LastRefreshAtMs       int64 // JS lastRefreshAt / lastRefresh / psd.lastRefreshAt
	ProjectId             string
	CopilotToken          string
	CopilotTokenExpiresAt string
	ProviderSpecificData  KiroProviderSpecificData
	RefreshError          string // 不可恢复刷新错误（JS result.error，merge 原样透传）
}

// ParseTimeMs 对应 JS parseTimeMs：数字 <1e12 视为秒 ×1000，否则视为 ms 原样；
// 字符串按 RFC3339 解析；空/非法返回 0。
func ParseTimeMs(value interface{}) int64 {
	switch v := value.(type) {
	case nil:
		return 0
	case int:
		return normalizeTimeNumber(float64(v))
	case int64:
		return normalizeTimeNumber(float64(v))
	case float64:
		return normalizeTimeNumber(v)
	case string:
		if v == "" {
			return 0
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UnixMilli()
		}
		return 0
	default:
		return 0
	}
}

func normalizeTimeNumber(n float64) int64 {
	if n < 1e12 {
		return int64(n * 1000)
	}
	return int64(n)
}

// GetCredentialExpiryMs 凭证过期时刻（getCredentialExpiryMs：
// expiresAt ?? tokenExpiresAt）。
func GetCredentialExpiryMs(c *KiroCredential) int64 {
	if c == nil {
		return 0
	}
	return c.ExpiresAtMs
}

// GetCredentialLastRefreshMs 最近刷新时刻（getCredentialLastRefreshMs：
// lastRefreshAt ?? lastRefresh ?? providerSpecificData.lastRefreshAt）。
func GetCredentialLastRefreshMs(c *KiroCredential) int64 {
	if c == nil {
		return 0
	}
	return c.LastRefreshAtMs
}

// ShouldRefreshCredentials 判定凭证是否需要刷新（shouldRefreshCredentials）：
// expiresAt - now < getRefreshLeadMs(provider) 时刷新；maxRefreshAgeMs 分支为
// codex 专属（kiro registry 未声明，不适用，注释保留原语义）。
func ShouldRefreshCredentials(provider string, c *KiroCredential, nowMs int64) bool {
	if c == nil {
		return false
	}

	expiresAtMs := GetCredentialExpiryMs(c)
	if expiresAtMs != 0 && expiresAtMs-nowMs < GetRefreshLeadMs(provider) {
		return true
	}

	// Proactive stale refresh for providers declaring oauth.maxRefreshAgeMs
	// (e.g. codex). Kiro registry declares no maxRefreshAgeMs — branch skipped.
	return false
}

// MergeProviderSpecificData 合并 providerSpecificData（mergeProviderSpecificData：
// {...existing, ...next}，next 非空时逐字段覆盖，空值亦覆盖）。
func MergeProviderSpecificData(existing, next KiroProviderSpecificData, hasNext bool) KiroProviderSpecificData {
	if !hasNext {
		return existing
	}
	out := existing
	out.AuthMethod = next.AuthMethod
	out.ClientId = next.ClientId
	out.ClientSecret = next.ClientSecret
	out.Region = next.Region
	out.ProfileArn = next.ProfileArn
	out.Provider = next.Provider
	out.TokenEndpoint = next.TokenEndpoint
	out.Scope = next.Scope
	return out
}

// MergeRefreshedCredentials 把刷新结果合并进凭证（mergeRefreshedCredentials）。
// 不可恢复错误原样透传（返回仅含 RefreshError 的凭证）；其余字段按 JS 条件复制。
func MergeRefreshedCredentials(provider string, current *KiroCredential, refreshed *KiroRefreshResult, nowMs int64) *KiroCredential {
	if refreshed == nil {
		return nil
	}
	if IsUnrecoverableRefreshError(refreshed) {
		return &KiroCredential{RefreshError: refreshed.Error}
	}

	next := &KiroCredential{}
	if refreshed.AccessToken != "" {
		next.AccessToken = refreshed.AccessToken
	}
	if refreshed.ApiKey != "" {
		next.ApiKey = refreshed.ApiKey
	}
	if refreshed.Token != "" {
		next.Token = refreshed.Token
	}

	refreshToken := refreshed.RefreshToken
	if refreshToken == "" && current != nil {
		refreshToken = current.RefreshToken
	}
	if refreshToken != "" {
		next.RefreshToken = refreshToken
	}

	idToken := refreshed.IdToken
	if idToken == "" && current != nil {
		idToken = current.IdToken
	}
	if idToken != "" {
		next.IdToken = idToken
	}

	if refreshed.ExpiresIn != 0 {
		next.ExpiresIn = refreshed.ExpiresIn
		next.ExpiresAtMs = nowMs + int64(refreshed.ExpiresIn)*1000
	} else if refreshed.ExpiresAtMs != 0 {
		next.ExpiresAtMs = refreshed.ExpiresAtMs
	}

	if refreshed.ProjectId != "" {
		next.ProjectId = refreshed.ProjectId
	}

	if refreshed.ProviderSpecificData != nil {
		next.ProviderSpecificData = MergeProviderSpecificData(credPSD(current), *refreshed.ProviderSpecificData, true)
	}

	if refreshed.CopilotToken != "" {
		next.CopilotToken = refreshed.CopilotToken
	}
	if refreshed.CopilotTokenExpiresAt != "" {
		next.CopilotTokenExpiresAt = refreshed.CopilotTokenExpiresAt
	}

	// trackRefreshAt providers (e.g. codex) 或任意令牌更新时盖戳 lastRefreshAt
	if next.AccessToken != "" || next.ApiKey != "" || next.Token != "" ||
		next.RefreshToken != "" || next.CopilotToken != "" {
		next.LastRefreshAtMs = nowMs // refreshed.lastRefreshAt 无对应字段，JS 亦缺省时用 nowIso
	}
	_ = provider
	return next
}

func credPSD(c *KiroCredential) KiroProviderSpecificData {
	if c == nil {
		return KiroProviderSpecificData{}
	}
	return c.ProviderSpecificData
}

// ===== 并发刷新锁（withCredentialRefreshLock） =====

var (
	credentialRefreshLocksMu sync.Mutex
	credentialRefreshLocks   = map[string]*credentialRefreshLock{}
)

type credentialRefreshLock struct {
	done   chan struct{}
	result *KiroCredential
}

// getRefreshLockKey 刷新锁键（getRefreshLockKey）：
// connectionId || id || email || name || refreshToken 后 16 位 || "default"。
func getRefreshLockKey(provider string, c *KiroCredential) string {
	var stableId string
	if c != nil {
		switch {
		case c.ConnectionId != "":
			stableId = c.ConnectionId
		case c.Id != "":
			stableId = c.Id
		case c.Email != "":
			stableId = c.Email
		case c.Name != "":
			stableId = c.Name
		case len(c.RefreshToken) > 0:
			if len(c.RefreshToken) >= 16 {
				stableId = c.RefreshToken[len(c.RefreshToken)-16:]
			} else {
				stableId = c.RefreshToken
			}
		}
	}
	if stableId == "" {
		stableId = "default"
	}
	return provider + ":" + stableId
}

// WithCredentialRefreshLock 同 key 并发刷新共享一次执行（withCredentialRefreshLock）。
func WithCredentialRefreshLock(provider string, c *KiroCredential, fn func() *KiroCredential) *KiroCredential {
	key := getRefreshLockKey(provider, c)
	for {
		credentialRefreshLocksMu.Lock()
		if e, ok := credentialRefreshLocks[key]; ok {
			credentialRefreshLocksMu.Unlock()
			<-e.done
			credentialRefreshLocksMu.Lock()
			result := e.result
			credentialRefreshLocksMu.Unlock()
			return result
		}
		e := &credentialRefreshLock{done: make(chan struct{})}
		credentialRefreshLocks[key] = e
		credentialRefreshLocksMu.Unlock()

		result := fn()
		credentialRefreshLocksMu.Lock()
		e.result = result
		delete(credentialRefreshLocks, key)
		credentialRefreshLocksMu.Unlock()
		close(e.done)
		return result
	}
}

// RefreshProviderCredentials 刷新 provider 凭证（refreshProviderCredentials）：
// 并发锁内 refreshTokenByProvider → mergeRefreshedCredentials。
func RefreshProviderCredentials(provider string, c *KiroCredential, proxy string) *KiroCredential {
	if c == nil {
		return nil
	}
	return WithCredentialRefreshLock(provider, c, func() *KiroCredential {
		refreshed := RefreshTokenByProvider(provider, c, proxy)
		return MergeRefreshedCredentials(provider, c, refreshed, time.Now().UnixMilli())
	})
}
