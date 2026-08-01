package reverseproxy

// 本文件 1:1 迁移自 9router：
//   - open-sse/services/tokenRefresh.js（kiro 相关部分：TOKEN_EXPIRY_BUFFER_MS、
//     isUnrecoverableRefreshError、getRefreshLeadMs、refreshWithRetry、getAccessToken、
//     refreshTokenByProvider）
//   - open-sse/services/tokenRefresh/providers.js（refreshKiroToken 三分支 +
//     resolveKiroProfileArnPatch）
//   - open-sse/services/tokenRefresh/dedup.js（dedupRefresh：in-flight 复用 + 10s 结果 TTL）
//   - src/lib/oauth/services/kiro.js（listAvailableProfiles，按既定决策统一使用
//     携带区域匹配逻辑的 KiroService 方案）
//
// 会话凭证管理（oauthCredentialManager.js）见 kiro_credential.go。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

// ===== 凭证数据结构（kiro 连接） =====

// KiroProviderSpecificData 对应 JS providerSpecificData（authMethod 相关的
// 扩展字段；各 authMethod 的字段组合不同，缺失字段为空串）。
type KiroProviderSpecificData struct {
	AuthMethod    string // builder-id / idc / google / github / imported / api_key / external_idp
	ClientId      string
	ClientSecret  string
	Region        string
	ProfileArn    string
	Provider      string // CLIProxyAPI / Enterprise / Imported / Google / Github
	TokenEndpoint string // external_idp 专用（Microsoft Entra）
	Scope         string // external_idp 专用
}

// KiroRefreshResult 对应 refreshKiroToken 的返回值。JS 的 refreshedCredentials 为
// 通用对象，merge 阶段按需读取以下可选字段（kiro 刷新结果通常只含前三项）。
type KiroRefreshResult struct {
	AccessToken           string
	RefreshToken          string
	ExpiresIn             int
	ExpiresAtMs           int64                     // JS expiresAt（epoch ms）
	ProviderSpecificData  *KiroProviderSpecificData // 可选（profileArn patch 或 external_idp 参数回传）
	Error                 string                    // 不可恢复错误标记（JS result.error，merge 时判别）
	ApiKey                string
	Token                 string
	IdToken               string
	ProjectId             string
	CopilotToken          string
	CopilotTokenExpiresAt string
}

// ===== tokenRefresh.js（kiro 相关） =====

// TokenExpiryBufferMs token 临近过期的刷新缓冲（TOKEN_EXPIRY_BUFFER_MS）。
const TokenExpiryBufferMs int64 = 5 * 60 * 1000

// GetRefreshLeadMs 返回 provider 的刷新提前量（getRefreshLeadMs）。
// REFRESH_LEAD_MS[provider] || TOKEN_EXPIRY_BUFFER_MS；kiro registry 未声明
// refreshLeadMs，恒为缓冲值。
func GetRefreshLeadMs(provider string) int64 {
	_ = provider // kiro 无 refreshLeadMs 配置，恒回退 TOKEN_EXPIRY_BUFFER_MS
	return TokenExpiryBufferMs
}

// IsUnrecoverableRefreshError 判断刷新结果是否为不可恢复错误
// （isUnrecoverableRefreshError）。
func IsUnrecoverableRefreshError(result *KiroRefreshResult) bool {
	if result == nil {
		return false
	}
	switch result.Error {
	case "unrecoverable_refresh_error", "refresh_token_reused", "invalid_request", "invalid_grant":
		return true
	}
	return false
}

// RefreshWithRetry 按 1s/2s 间隔重试刷新函数，最多 maxRetries 次
// （refreshWithRetry：attempt 延迟 = attempt*1000ms；全部失败返回 nil）。
func RefreshWithRetry(refreshFn func() (*KiroRefreshResult, error), maxRetries int) *KiroRefreshResult {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := attempt * 1000
			log.Printf("[TOKEN_REFRESH] Retry %d/%d after %dms", attempt, maxRetries, delay)
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		result, err := refreshFn()
		if err != nil {
			log.Printf("[TOKEN_REFRESH] Attempt %d/%d failed: %v", attempt+1, maxRetries, err)
			continue
		}
		if result != nil {
			return result
		}
	}
	log.Printf("[TOKEN_REFRESH] All %d retry attempts failed", maxRetries)
	return nil
}

// GetAccessToken 按 provider 取访问令牌（getAccessToken；阶段 3 仅 kiro 分支，
// 其余 provider 返回 nil）。
func GetAccessToken(provider string, c *KiroCredential, proxy string) *KiroRefreshResult {
	if c == nil || c.RefreshToken == "" {
		log.Printf("[TOKEN_REFRESH] No valid refresh token available for provider: %s", provider)
		return nil
	}
	if provider != "kiro" {
		log.Printf("[TOKEN_REFRESH] Unsupported provider for token refresh: %s", provider)
		return nil
	}
	return RefreshKiroToken(c.RefreshToken, c.ProviderSpecificData, proxy)
}

// RefreshTokenByProvider 按 provider 刷新令牌（refreshTokenByProvider；
// 阶段 3 仅 kiro 分支，其余 provider 返回 nil）。
func RefreshTokenByProvider(provider string, c *KiroCredential, proxy string) *KiroRefreshResult {
	if c == nil || c.RefreshToken == "" {
		return nil
	}
	if provider == "kiro" {
		return RefreshKiroToken(c.RefreshToken, c.ProviderSpecificData, proxy)
	}
	return nil
}

// ===== KiroService.listAvailableProfiles（统一方案） =====

var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d{1,2}$`)

// AssertValidAwsRegion 校验 AWS 区域格式（assertValidAwsRegion，防 SSRF）。
func AssertValidAwsRegion(region string) error {
	if !awsRegionPattern.MatchString(region) {
		return fmt.Errorf("invalid AWS region: %s", region)
	}
	return nil
}

// pickKiroProfileArn 对应 listAvailableProfiles 的匹配逻辑：
// arnOf = p.arn || p.profileArn；优先取 split(":")[3] === region 的 profile，
// 否则取 profiles[0]；无匹配返回空串。
func pickKiroProfileArn(profiles []map[string]interface{}, region string) string {
	arnOf := func(p map[string]interface{}) string {
		for _, key := range []string{"arn", "profileArn"} {
			if s, ok := p[key].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	var match map[string]interface{}
	for _, p := range profiles {
		if parts := strings.Split(arnOf(p), ":"); len(parts) > 3 && parts[3] == region {
			match = p
			break
		}
	}
	if match == nil && len(profiles) > 0 {
		match = profiles[0]
	}
	return arnOf(match)
}

// ListAvailableProfiles 列出 token（或 API key）可用的 CodeWhisperer profile，
// 返回区域匹配的 profileArn（listAvailableProfiles；无匹配回退 profiles[0]，
// 仍无则空串）。AWS SSO OIDC 登录不返回 profileArn，需单独拉取——同一调用
// 也适用于 API-key 鉴权。响应字段 `arn` 与 `profileArn` 均接受。
func ListAvailableProfiles(accessToken, region string, proxy ...string) (string, error) {
	if err := AssertValidAwsRegion(region); err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("https://codewhisperer.%s.amazonaws.com", region)

	body, _ := json.Marshal(map[string]interface{}{"maxResults": 10})
	status, respBody, err := kiroRefreshPOST(endpoint, map[string]string{
		"Content-Type":  "application/x-amz-json-1.0",
		"x-amz-target":  "AmazonCodeWhispererService.ListAvailableProfiles",
		"Authorization": "Bearer " + accessToken,
		"Accept":        "application/json",
	}, body, proxy...)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("Failed to list profiles: %s", truncate(string(respBody), 500))
	}

	var data map[string]interface{}
	if json.Unmarshal(respBody, &data) != nil {
		return "", nil
	}
	var profiles []map[string]interface{}
	if raw, ok := data["profiles"].([]interface{}); ok {
		for _, item := range raw {
			if p, ok := item.(map[string]interface{}); ok {
				profiles = append(profiles, p)
			}
		}
	}
	return pickKiroProfileArn(profiles, region), nil
}

// ===== refreshKiroToken（三分支） =====

// kiroSocialAuthService 社交刷新端点宿主（PROVIDERS.kiro.tokenUrl 同源）。
const kiroSocialAuthService = "https://prod.us-east-1.auth.desktop.kiro.dev"

// resolveKiroProfileArnPatch 对应 JS resolveKiroProfileArnPatch：
// 已有 profileArn 不覆盖；否则用刷新返回的 profileArn；再否则调
// ListAvailableProfiles 兜底（统一 KiroService 区域匹配方案）。
func resolveKiroProfileArnPatch(psd KiroProviderSpecificData, accessToken, refreshedArn, region, proxy string) *KiroProviderSpecificData {
	if psd.ProfileArn != "" {
		return nil
	}
	arn := strings.TrimSpace(refreshedArn)
	if arn == "" && accessToken != "" {
		r := region
		if r == "" {
			r = "us-east-1"
		}
		if a, err := ListAvailableProfiles(accessToken, r, proxy); err == nil {
			arn = a
		}
	}
	if arn == "" {
		return nil
	}
	return &KiroProviderSpecificData{ProfileArn: arn}
}

// ===== dedup.js（dedupRefresh） =====

const kiroRefreshResultTTL = 10 * time.Second

var (
	kiroRefreshDedupMu  sync.Mutex
	kiroRefreshDedupMap = map[string]*kiroRefreshDedupEntry{}
)

type kiroRefreshDedupEntry struct {
	done     chan struct{} // nil 表示已完成（TTL 内缓存结果）
	result   *KiroRefreshResult
	expireAt time.Time
}

// kiroDedupRefresh 并发刷新去重：key=kiro:refreshToken；in-flight 复用 +
// 10s 结果 TTL（dedupRefresh 同款）。失败时删除缓存条目并唤醒等待者
// （等待者重新走获取流程；JS 原版为等待者共享同一 rejection，此处为等价近似）。
func kiroDedupRefresh(key string, fn func() (*KiroRefreshResult, error)) (*KiroRefreshResult, error) {
	var created *kiroRefreshDedupEntry
	for {
		kiroRefreshDedupMu.Lock()
		e, ok := kiroRefreshDedupMap[key]
		if ok && time.Now().Before(e.expireAt) {
			if e.done == nil {
				// 10s TTL 内已完成：直接复用结果
				result := e.result
				kiroRefreshDedupMu.Unlock()
				return result, nil
			}
			// in-flight：等待完成
			done := e.done
			kiroRefreshDedupMu.Unlock()
			<-done
			kiroRefreshDedupMu.Lock()
			if e2, ok2 := kiroRefreshDedupMap[key]; ok2 && e2.done == nil {
				result := e2.result
				kiroRefreshDedupMu.Unlock()
				return result, nil
			}
			kiroRefreshDedupMu.Unlock()
			continue
		}
		e = &kiroRefreshDedupEntry{done: make(chan struct{}), expireAt: time.Now().Add(kiroRefreshResultTTL)}
		kiroRefreshDedupMap[key] = e
		created = e
		kiroRefreshDedupMu.Unlock()
		break
	}

	result, err := fn()
	done := created.done
	kiroRefreshDedupMu.Lock()
	if err != nil {
		// 失败：删除缓存并唤醒等待者（JS catch 语义）
		delete(kiroRefreshDedupMap, key)
		kiroRefreshDedupMu.Unlock()
		close(done)
		return nil, err
	}
	created.result = result
	created.done = nil
	kiroRefreshDedupMu.Unlock()
	close(done)
	return result, nil
}

// kiroRefreshPOST 令牌刷新用 POST 请求（对应 JS proxyAwareFetch；经 tls-client
// 走可选出口代理）。网络错误返回 error；HTTP 非 200 返回状态码与响应体。
func kiroRefreshPOST(url string, headers map[string]string, body []byte, proxy ...string) (int, []byte, error) {
	client := newClient(proxy...)
	req, err := fhttp.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

// expiresInOf 提取响应里的 expiresIn/expires_in（缺失或非法返回 0，与 JS 一致
// 不做默认值）。
func expiresInOf(v interface{}) int {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil && f > 0 {
			return int(f)
		}
	}
	return 0
}

// RefreshKiroToken 刷新 Kiro 令牌（refreshKiroToken），按 authMethod 三分支：
//  1. external_idp → Microsoft Entra 端点（x-www-form-urlencoded）
//  2. clientId+clientSecret → AWS SSO OIDC（IDC 走区域端点，其余 us-east-1）
//  3. 其余 → 社交刷新端点（kiro.dev/refreshToken，UA kiro-cli/1.0.0）
//
// 无 refreshToken 返回 nil；HTTP 失败记录日志后返回 nil（JS return null 语义）。
func RefreshKiroToken(refreshToken string, psd KiroProviderSpecificData, proxy string) *KiroRefreshResult {
	if refreshToken == "" {
		return nil
	}
	result, _ := kiroDedupRefresh("kiro:"+refreshToken, func() (*KiroRefreshResult, error) {
		return refreshKiroTokenInner(refreshToken, psd, proxy)
	})
	return result
}

func refreshKiroTokenInner(refreshToken string, psd KiroProviderSpecificData, proxy string) (*KiroRefreshResult, error) {
	authMethod := psd.AuthMethod
	clientId := psd.ClientId
	clientSecret := psd.ClientSecret
	region := psd.Region

	// 分支 1：external_idp（Microsoft Entra）
	if authMethod == "external_idp" {
		refreshRequest, err := BuildExternalIdpRefreshParams(refreshToken, psd)
		if err != nil {
			log.Printf("[TOKEN_REFRESH] Invalid Kiro external_idp refresh config: %v", err)
			return nil, nil
		}
		status, respBody, err := kiroRefreshPOST(refreshRequest.TokenEndpoint, map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
			"Accept":       "application/json",
		}, []byte(refreshRequest.Body.Encode()), proxy)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			log.Printf("[TOKEN_REFRESH] Failed to refresh Kiro external_idp token: status %d error %s",
				status, truncate(string(respBody), 500))
			return nil, nil
		}
		var tokens map[string]interface{}
		if json.Unmarshal(respBody, &tokens) != nil {
			return nil, nil
		}
		at, _ := tokens["access_token"].(string)
		rt, _ := tokens["refresh_token"].(string)
		if rt == "" {
			rt = refreshToken
		}
		log.Printf("[TOKEN_REFRESH] Successfully refreshed Kiro external_idp token: hasNewAccessToken=%t hasNewRefreshToken=%t expiresIn=%v",
			at != "", rt != refreshToken, tokens["expires_in"])
		return &KiroRefreshResult{
			AccessToken:          at,
			RefreshToken:         rt,
			ExpiresIn:            expiresInOf(tokens["expires_in"]),
			ProviderSpecificData: &refreshRequest.ProviderSpecificData,
		}, nil
	}

	// 分支 2：clientId + clientSecret（AWS SSO OIDC；IDC 走区域端点，其余 us-east-1）
	if clientId != "" && clientSecret != "" {
		isIDC := authMethod == "idc"
		endpoint := "https://oidc.us-east-1.amazonaws.com/token"
		if isIDC && region != "" {
			endpoint = fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
		}
		reqBody, _ := json.Marshal(map[string]string{
			"clientId":     clientId,
			"clientSecret": clientSecret,
			"refreshToken": refreshToken,
			"grantType":    "refresh_token",
		})
		status, respBody, err := kiroRefreshPOST(endpoint, map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		}, reqBody, proxy)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			log.Printf("[TOKEN_REFRESH] Failed to refresh Kiro AWS token: status %d error %s",
				status, truncate(string(respBody), 500))
			return nil, nil
		}
		var tokens map[string]interface{}
		if json.Unmarshal(respBody, &tokens) != nil {
			return nil, nil
		}
		at, _ := tokens["accessToken"].(string)
		rt, _ := tokens["refreshToken"].(string)
		if rt == "" {
			rt = refreshToken
		}
		log.Printf("[TOKEN_REFRESH] Successfully refreshed Kiro AWS token: hasNewAccessToken=%t expiresIn=%v",
			at != "", tokens["expiresIn"])
		patch := resolveKiroProfileArnPatch(psd, at, stringOf(tokens["profileArn"]), region, proxy)
		return &KiroRefreshResult{
			AccessToken:          at,
			RefreshToken:         rt,
			ExpiresIn:            expiresInOf(tokens["expiresIn"]),
			ProviderSpecificData: patch,
		}, nil
	}

	// 分支 3：社交/Builder ID 刷新端点
	reqBody, _ := json.Marshal(map[string]string{"refreshToken": refreshToken})
	status, respBody, err := kiroRefreshPOST(kiroSocialAuthService+"/refreshToken", map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"User-Agent":   "kiro-cli/1.0.0",
	}, reqBody, proxy)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		log.Printf("[TOKEN_REFRESH] Failed to refresh Kiro social token: status %d error %s",
			status, truncate(string(respBody), 500))
		return nil, nil
	}
	var tokens map[string]interface{}
	if json.Unmarshal(respBody, &tokens) != nil {
		return nil, nil
	}
	at, _ := tokens["accessToken"].(string)
	rt, _ := tokens["refreshToken"].(string)
	if rt == "" {
		rt = refreshToken
	}
	log.Printf("[TOKEN_REFRESH] Successfully refreshed Kiro social token: hasNewAccessToken=%t expiresIn=%v",
		at != "", tokens["expiresIn"])
	patch := resolveKiroProfileArnPatch(psd, at, stringOf(tokens["profileArn"]), region, proxy)
	return &KiroRefreshResult{
		AccessToken:          at,
		RefreshToken:         rt,
		ExpiresIn:            expiresInOf(tokens["expiresIn"]),
		ProviderSpecificData: patch,
	}, nil
}

func stringOf(v interface{}) string {
	s, _ := v.(string)
	return s
}
