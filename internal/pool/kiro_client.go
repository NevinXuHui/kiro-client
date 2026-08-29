package pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// KiroClient Kiro账号健康检测和额度查询客户端
type KiroClient struct {
	httpClient tls_client.HttpClient
}

// NewKiroClient 创建新的 Kiro 客户端
func NewKiroClient() *KiroClient {
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_144),
		tls_client.WithInsecureSkipVerify(),
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		panic(fmt.Sprintf("create kiro client: %v", err))
	}
	return &KiroClient{httpClient: client}
}

// oidcEndpoint 返回 OIDC token endpoint
func oidcEndpoint(region string) string {
	if region == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
}

// cwUsageEndpoint 返回 CodeWhisperer usage endpoint
func cwUsageEndpoint(region string) string {
	if region == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf("https://codewhisperer.%s.amazonaws.com", region)
}

// qUsageEndpoint 返回 Q usage endpoint
func qUsageEndpoint(region string) string {
	if region == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf("https://q.%s.amazonaws.com", region)
}

// RefreshAccessToken 刷新 access token
func (c *KiroClient) RefreshAccessToken(account *Account) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"clientId":     account.ClientID,
		"clientSecret": account.ClientSecret,
		"refreshToken": account.RefreshToken,
		"grantType":    "refresh_token",
	})

	req, _ := fhttp.NewRequest("POST", oidcEndpoint(account.Region), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("refresh token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("refresh token HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var tok map[string]interface{}
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	at, _ := tok["accessToken"].(string)
	if at == "" {
		return "", fmt.Errorf("response missing accessToken")
	}

	return at, nil
}

// CheckHealth 健康检测：尝试刷新 token
func (c *KiroClient) CheckHealth(account *Account) error {
	_, err := c.RefreshAccessToken(account)
	return err
}

// classifyHealthError 按 9router 语义将健康检测错误细分为状态 + 细分码
//
//	AUTH(401/403)   = 令牌失效/吊销 → 死（unhealthy）
//	429             = 上游限流 → 软失败，账号保留可用（9router: rate_limit 走 backoff 冷却，连接保持 active）
//	502/503/504     = 上游不可用 → 异常（unhealthy，显示具体码）
//	其他 HTTP ≥400  = 异常（显示数字码）
//	NET             = 网络错误 → 异常
func classifyHealthError(err error) (healthStatus, healthCode string) {
	if err == nil {
		return "healthy", ""
	}
	msg := err.Error()
	if i := strings.Index(msg, "HTTP "); i >= 0 {
		code := strings.TrimSpace(strings.Split(msg[i+5:], ":")[0])
		switch code {
		case "401", "403":
			return "unhealthy", "AUTH"
		case "429":
			return "healthy", "429"
		case "502", "503", "504":
			return "unhealthy", code
		default:
			if len(code) == 3 && code[0] >= '4' && code[0] <= '5' {
				digits := true
				for _, ch := range code[1:] {
					if ch < '0' || ch > '9' {
						digits = false
						break
					}
				}
				if digits {
					return "unhealthy", code
				}
			}
		}
	}
	return "unhealthy", "NET"
}

const kiroRESTIDEVersion = "2.3.0"
const builderIDProfileARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"

func usageQueryProfileArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return builderIDProfileARN
	}
	return arn
}

func applyQRESTHeaders(h fhttp.Header, accessToken string) {
	h.Set("Authorization", "Bearer "+accessToken)
	h.Set("Accept", "application/json")
	h.Set("User-Agent", "aws-sdk-js/1.0.0 ua/2.1 os/linux lang/js md/nodejs#22.22.0 api/codewhispererruntime#1.0.0 m/N,E KiroIDE-"+kiroRESTIDEVersion+"-kiroclient")
	h.Set("x-amz-user-agent", "aws-sdk-js/1.0.0 KiroIDE-"+kiroRESTIDEVersion+"-kiroclient")
}

func usageLimitsURL(region, profileArn string) string {
	return fmt.Sprintf("%s/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true&profileArn=%s",
		qUsageEndpoint(region), url.QueryEscape(usageQueryProfileArn(profileArn)))
}

// GetUsage 查询账号额度（参考 9router kiro.js 实现）
func (c *KiroClient) GetUsage(account *Account, accessToken string) (map[string]interface{}, error) {
	arn := ""
	if account != nil {
		arn = usageQueryProfileArn(account.ProfileArn)
	} else {
		arn = usageQueryProfileArn("")
	}
	attempts := []struct {
		name string
		run  func() (*fhttp.Response, error)
	}{
		{
			name: "codewhisperer-post",
			run: func() (*fhttp.Response, error) {
				payload := map[string]interface{}{
					"origin":       "AI_EDITOR",
					"resourceType": "AGENTIC_REQUEST",
					"profileArn":   arn,
				}
				body, _ := json.Marshal(payload)
				req, _ := fhttp.NewRequest("POST", cwUsageEndpoint(account.Region), bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+accessToken)
				req.Header.Set("Content-Type", "application/x-amz-json-1.0")
				req.Header.Set("x-amz-target", "AmazonCodeWhispererService.GetUsageLimits")
				req.Header.Set("Accept", "application/json")
				return c.httpClient.Do(req)
			},
		},
		{
			name: "q-get",
			run: func() (*fhttp.Response, error) {
				req, _ := fhttp.NewRequest("GET", usageLimitsURL(account.Region, arn), nil)
				applyQRESTHeaders(req.Header, accessToken)
				return c.httpClient.Do(req)
			},
		},
	}

	var lastErr error
	for _, attempt := range attempts {
		resp, err := attempt.run()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", attempt.name, err)
			continue
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("%s: HTTP %d", attempt.name, resp.StatusCode)
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal(respBody, &data); err != nil {
			lastErr = fmt.Errorf("%s: parse error", attempt.name)
			continue
		}

		// 解析成功，返回额度数据
		return parseKiroUsage(data), nil
	}

	// 所有尝试失败，返回空数据和错误
	if lastErr != nil {
		return map[string]interface{}{
			"message": fmt.Sprintf("Unable to fetch usage: %v", lastErr),
		}, lastErr
	}

	return map[string]interface{}{
		"message": "Unable to fetch usage",
	}, fmt.Errorf("all attempts failed")
}

// parseKiroUsage 解析 Kiro usage API 响应（参考 9router kiro.js）
func parseKiroUsage(data map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"plan":   "Kiro",
		"quotas": make(map[string]interface{}),
	}

	// 提取订阅信息
	if subInfo, ok := data["subscriptionInfo"].(map[string]interface{}); ok {
		if title, ok := subInfo["subscriptionTitle"].(string); ok {
			result["plan"] = title
		}
	}

	// 提取额度详情
	usageList, ok := data["usageBreakdownList"].([]interface{})
	if !ok {
		return result
	}

	quotas := make(map[string]interface{})
	resetAt := parseResetTime(data["nextDateReset"], data["resetDate"])

	for _, item := range usageList {
		breakdown, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		resourceType := "unknown"
		if rt, ok := breakdown["resourceType"].(string); ok {
			resourceType = rt
		}

		used := getFloat(breakdown, "currentUsageWithPrecision")
		total := getFloat(breakdown, "usageLimitWithPrecision")

		quotas[resourceType] = map[string]interface{}{
			"used":      used,
			"total":     total,
			"remaining": total - used,
			"resetAt":   resetAt,
			"unlimited": false,
		}

		// 处理免费试用额度
		if freeTrialInfo, ok := breakdown["freeTrialInfo"].(map[string]interface{}); ok {
			freeUsed := getFloat(freeTrialInfo, "currentUsageWithPrecision")
			freeTotal := getFloat(freeTrialInfo, "usageLimitWithPrecision")
			freeResetAt := parseResetTime(freeTrialInfo["freeTrialExpiry"], resetAt)

			quotas[resourceType+"_freetrial"] = map[string]interface{}{
				"used":      freeUsed,
				"total":     freeTotal,
				"remaining": freeTotal - freeUsed,
				"resetAt":   freeResetAt,
				"unlimited": false,
			}
		}
	}

	result["quotas"] = quotas
	return result
}

// parseResetTime 解析重置时间
func parseResetTime(values ...interface{}) string {
	for _, v := range values {
		if v == nil {
			continue
		}

		switch val := v.(type) {
		case string:
			if val != "" {
				if t, err := time.Parse(time.RFC3339, val); err == nil {
					return t.Format(time.RFC3339)
				}
				return val
			}
		case float64:
			// Unix timestamp (milliseconds or seconds)
			if val < 1e12 {
				return time.Unix(int64(val), 0).Format(time.RFC3339)
			}
			return time.Unix(int64(val)/1000, 0).Format(time.RFC3339)
		}
	}
	return ""
}

// getFloat 安全提取 float64
func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// GetAvailableModels 获取可用模型列表
func availableModelsURL(region, profileArn string) string {
	return fmt.Sprintf("%s/ListAvailableModels?origin=AI_EDITOR&profileArn=%s",
		qUsageEndpoint(region), url.QueryEscape(usageQueryProfileArn(profileArn)))
}

func (c *KiroClient) GetAvailableModels(account *Account, accessToken string) ([]string, int) {
	fallback := []string{
		"claude-opus-4.8",
		"claude-opus-4.7",
		"claude-opus-4.5",
		"claude-sonnet-5",
		"claude-sonnet-4.5",
		"claude-haiku-4.5",
		"deepseek-3.2",
		"qwen3-coder-next",
		"glm-5",
		"MiniMax-M2.5",
	}
	if accessToken == "" {
		return fallback, 0
	}
	arn := ""
	if account != nil {
		arn = usageQueryProfileArn(account.ProfileArn)
	} else {
		arn = usageQueryProfileArn("")
	}
	urls := []string{availableModelsURL(account.Region, arn)}
	for _, u := range urls {
		req, err := fhttp.NewRequest("GET", u, nil)
		if err != nil {
			continue
		}
		applyQRESTHeaders(req.Header, accessToken)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}
		statusCode := resp.StatusCode
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// 403 判死
		if statusCode == 403 {
			return nil, 403
		}
		if statusCode != 200 {
			continue
		}

		var data map[string]interface{}
		if json.Unmarshal(body, &data) != nil {
			continue
		}
		raw, _ := data["models"].([]interface{})
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := m["modelId"].(string)
			if id == "" {
				id, _ = m["modelName"].(string)
			}
			if id != "" {
				out = append(out, id)
			}
		}
		if len(out) > 0 {
			return out, 200
		}
	}
	return fallback, 0
}

// UpdateAccountInfo 更新账号运行时信息（健康状态、额度、模型）
// 从 GetUsageLimits 解析出 CreditUsed/CreditLimit，供无感轮询决策。
// 增强验活：403 判死（参考 core.VerifyAlive）
func (c *KiroClient) UpdateAccountInfo(account *Account) error {
	now := time.Now().Format(time.RFC3339)

	// 1. 刷新 access token（同时作为健康检测）
	accessToken, err := c.RefreshAccessToken(account)
	if err != nil {
		status, code := classifyHealthError(err)
		account.HealthStatus = status
		account.HealthCode = code
		account.HealthError = err.Error()
		account.HealthCheckedAt = now
		account.LastUpdatedAt = now

		// Token 刷新 401/403 → 账号吊销/封禁，判死
		if code == "AUTH" {
			account.HealthStatus = "suspended"
			account.HealthError = "Token 已吊销或账号已封禁 (401/403)"
		}
		return err
	}

	account.HealthStatus = "healthy"
	account.HealthCode = ""
	account.HealthError = ""
	account.HealthCheckedAt = now

	// 2. 查询额度（403 判死）
	usage, err := c.GetUsage(account, accessToken)
	if err != nil {
		// 检查是否是 403 封号信号
		if strings.Contains(err.Error(), "HTTP 403") {
			account.HealthStatus = "suspended"
			account.HealthCode = "AUTH"
			account.HealthError = "Q 端点 403，账号已封禁"
			account.LastUpdatedAt = now
			return fmt.Errorf("account suspended: Q endpoint 403")
		}

		// 额度查询失败不算健康问题，可能是 API 限制
		account.UsageQuotas = map[string]interface{}{
			"error": err.Error(),
		}
	} else {
		account.UsageQuotas = usage
		// 从 GetUsageLimits 结果提取 CreditUsed/CreditLimit（9router parseKiroQuotaData 同款）
		if quotas, ok := usage["quotas"].(map[string]interface{}); ok {
			// 取第一个有值的 resourceType
			for _, qv := range quotas {
				if q, ok := qv.(map[string]interface{}); ok {
					if used, ok := q["used"].(float64); ok {
						account.CreditUsed = int(used)
					}
					if total, ok := q["total"].(float64); ok {
						account.CreditLimit = int(total)
						// 记录 resetAt 时间
						if resetAt, ok := q["resetAt"].(string); ok && resetAt != "" {
							account.CreditResetAt = resetAt
						}
					}
					break
				}
			}
		}
	}

	// 3. 获取可用模型（403 判死）
	models, modelStatus := c.GetAvailableModels(account, accessToken)
	if modelStatus == 403 {
		account.HealthStatus = "suspended"
		account.HealthCode = "AUTH"
		account.HealthError = "ListAvailableModels 403，账号已封禁"
		account.LastUpdatedAt = now
		return fmt.Errorf("account suspended: models endpoint 403")
	}
	account.AvailableModels = models

	account.LastUpdatedAt = now
	return nil
}

// NeedsRefresh 增量刷新判定（9router 惰性思想）：只刷需要刷的账号
//
//	AUTH 死号不自动重试（省请求，留手动全量）
//	429 软失败 → 60s 后重试（限流恢复快）
//	其余异常（NET/5xx/UNKNOWN）→ 立即重试（瞬时错误尝试恢复）
//	健康且检测时间 < ttl（默认 5min）→ 跳过
func NeedsRefresh(acc *Account, now time.Time, ttl time.Duration) bool {
	if acc == nil {
		return false
	}
	if acc.HealthStatus == "unhealthy" {
		return acc.HealthCode != "AUTH"
	}
	if acc.HealthCheckedAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, acc.HealthCheckedAt)
	if err != nil {
		return true
	}
	elapsed := now.Sub(t)
	if acc.HealthCode == "429" {
		return elapsed > time.Minute
	}
	return elapsed > ttl
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
