package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	httputil "reg_go/internal/http"
)

// VerifyAlive 验活: 刷新 Token + 查用量 + 查模型。
//
// 判死逻辑（v2）：
//   - Token 刷新成功（200）→ 账号存活，即使 Q 端点暂时不可达也返回 alive
//   - Token 刷新返回 401/403 → 账号确实已吊销/封禁，返回 suspended
//   - Q 端点（getUsageLimits / ListAvailableModels）403 → 仅 warn，不阻断
//     新注册账号 Q 侧 profile 可能尚未同步，或出口 IP 被 Q 风控，不等同于封号
func (r *Registrar) VerifyAlive(awsToken map[string]interface{}) map[string]interface{} {
	log.Println("[验活] 刷新 Token + 查用量")
	client := httputil.NewTLSClient(r.Cfg.Proxy, true, r.Identity.ChromeVer)

	refreshToken, _ := awsToken["refreshToken"].(string)

	tokenBody, _ := json.Marshal(map[string]string{
		"clientId":     r.ClientID,
		"clientSecret": r.ClientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	})
	req, _ := fhttp.NewRequest("POST", "https://oidc.us-east-1.amazonaws.com/token",
		bytes.NewReader(tokenBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("验活异常: %v", err)
		return map[string]interface{}{"alive": false, "error": err.Error()}
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Token 刷新失败 → 真正的封号/吊销信号
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		log.Printf("Token 刷新被拒 (%d)，账号已封禁或吊销", resp.StatusCode)
		return map[string]interface{}{"alive": false, "suspended": true, "error": "token revoked"}
	}
	if resp.StatusCode != 200 {
		log.Printf("Token 刷新失败: %d", resp.StatusCode)
		return map[string]interface{}{"alive": false, "error": fmt.Sprintf("refresh failed: %d", resp.StatusCode)}
	}

	var tok map[string]interface{}
	json.Unmarshal(body, &tok)
	access, _ := tok["accessToken"].(string)
	expiresIn, _ := tok["expiresIn"].(float64)
	log.Printf("Token 刷新成功, expiresIn=%ds", int(expiresIn))

	// Q 端点查询（非致命：新号/IP 风控可能导致 403，不等同于封号）
	usageURL := "https://q.us-east-1.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true"
	usageRes := queryGetEndpointWithRetry(client, access, usageURL)
	if !usageRes.ok {
		log.Printf("[验活] getUsageLimits 不可达 (status=%d)，跳过用量查询", usageRes.statusCode)
	}

	modelRes := queryGetEndpointWithRetry(client, access, "https://q.us-east-1.amazonaws.com/ListAvailableModels?origin=AI_EDITOR")
	if !modelRes.ok {
		log.Printf("[验活] ListAvailableModels 不可达 (status=%d)，跳过模型查询", modelRes.statusCode)
	}

	// Token 刷新成功 = 账号存活。Q 端点不可达不影响此判定。
	if usageRes.ok && len(usageRes.body) > 0 {
		return r.parseUsage(usageRes.body)
	}

	log.Println("[验活] Token 有效，Q 端点暂不可达（新号同步中或 IP 风控），账号仍视为存活")
	return map[string]interface{}{
		"alive":          true,
		"token_valid":    true,
		"q_endpoints_ok": false,
	}
}

type endpointResult struct {
	body       []byte
	ok         bool
	statusCode int
}

func checkEndpointResponse(url string, statusCode int, body []byte) endpointResult {
	label := endpointLabel(url)
	if statusCode == 403 {
		log.Printf("[验活] Q 端点 403 [%s]（可能为新号同步延迟或 IP 风控，不视为封号）", label)
		return endpointResult{statusCode: statusCode}
	}
	if statusCode != 200 {
		log.Printf("端点查询失败 [%s]: %d", label, statusCode)
		return endpointResult{statusCode: statusCode}
	}
	return endpointResult{body: body, ok: true, statusCode: statusCode}
}

// endpointLabel 把完整 URL 归一到简短标签，避免日志泄露后端。
func endpointLabel(url string) string {
	switch {
	case strings.Contains(url, "getUsageLimits"):
		return "usage"
	case strings.Contains(url, "ListAvailableModels"):
		return "models"
	case strings.Contains(url, "refreshToken"):
		return "kiro-refresh"
	case strings.Contains(url, "/token"):
		return "oidc-token"
	default:
		return "endpoint"
	}
}

// queryGetEndpointWithRetry 带重试的 GET 端点查询。
// 新注册账号 Q 端点可能暂时 403（profile 同步延迟），重试 3 次，间隔 10s。
func queryGetEndpointWithRetry(client interface {
	Do(req *fhttp.Request) (*fhttp.Response, error)
}, access, url string) endpointResult {
	const maxRetries = 3
	const retryDelay = 10 * time.Second

	var lastRes endpointResult
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[验活] %s 第 %d/%d 次重试（%v 后）...", endpointLabel(url), attempt+1, maxRetries, retryDelay)
			time.Sleep(retryDelay)
		}

		req, _ := fhttp.NewRequest("GET", url, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("User-Agent", "aws-sdk-js/1.0.18 ua/2.1 os/windows lang/js md/nodejs#20.16.0 api/codewhispererstreaming#1.0.18 m/E KiroIDE-0.6.18")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("端点查询异常 [%s]: %s", endpointLabel(url), scrubURLs(err.Error()))
			lastRes = endpointResult{}
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		lastRes = checkEndpointResponse(url, resp.StatusCode, body)
		if lastRes.ok {
			return lastRes
		}

		// 非 403 的错误不重试（如 400/404/500 重试无意义）
		if resp.StatusCode != 403 {
			break
		}
	}
	return lastRes
}

func queryGetEndpoint(client interface {
	Do(req *fhttp.Request) (*fhttp.Response, error)
}, access, url string) endpointResult {
	req, _ := fhttp.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("User-Agent", "aws-sdk-js/1.0.18 ua/2.1 os/windows lang/js md/nodejs#20.16.0 api/codewhispererstreaming#1.0.18 m/E KiroIDE-0.6.18")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("端点查询异常 [%s]: %s", endpointLabel(url), scrubURLs(err.Error()))
		return endpointResult{}
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return checkEndpointResponse(url, resp.StatusCode, body)
}

func queryPostEndpoint(client interface {
	Do(req *fhttp.Request) (*fhttp.Response, error)
}, url string, payload []byte) endpointResult {
	req, _ := fhttp.NewRequest("POST", url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("端点查询异常 [%s]: %s", endpointLabel(url), scrubURLs(err.Error()))
		return endpointResult{}
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return checkEndpointResponse(url, resp.StatusCode, body)
}

func (r *Registrar) parseUsage(body []byte) map[string]interface{} {
	var usage map[string]interface{}
	json.Unmarshal(body, &usage)

	userInfo, _ := usage["userInfo"].(map[string]interface{})
	emailAddr, _ := userInfo["email"].(string)
	subInfo, _ := usage["subscriptionInfo"].(map[string]interface{})
	sub, _ := subInfo["subscriptionTitle"].(string)
	if sub == "" {
		sub = "Free"
	}

	var totalLimit, totalUsed float64
	if breakdown, ok := usage["usageBreakdownList"].([]interface{}); ok {
		for _, item := range breakdown {
			b, _ := item.(map[string]interface{})
			rt, _ := b["resourceType"].(string)
			dn, _ := b["displayName"].(string)
			if rt == "CREDIT" || dn == "Credits" {
				baseLimit, _ := b["usageLimitWithPrecision"].(float64)
				if baseLimit == 0 {
					baseLimit, _ = b["usageLimit"].(float64)
				}
				baseUsed, _ := b["currentUsageWithPrecision"].(float64)
				if baseUsed == 0 {
					baseUsed, _ = b["currentUsage"].(float64)
				}
				totalLimit = baseLimit
				totalUsed = baseUsed

				if ft, ok := b["freeTrialInfo"].(map[string]interface{}); ok {
					if ftStatus, _ := ft["freeTrialStatus"].(string); ftStatus == "ACTIVE" {
						ftLimit, _ := ft["usageLimitWithPrecision"].(float64)
						ftUsed, _ := ft["currentUsageWithPrecision"].(float64)
						totalLimit += ftLimit
						totalUsed += ftUsed
					}
				}
				break
			}
		}
	}

	log.Printf("验活成功! 邮箱=%s 订阅=%s Credit=%.1f/%.1f", emailAddr, sub, totalUsed, totalLimit)
	return map[string]interface{}{
		"alive": true, "email": emailAddr, "subscription": sub,
		"credit_used": totalUsed, "credit_limit": totalLimit,
	}
}
