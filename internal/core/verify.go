package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	httputil "reg_go/internal/http"
)

// VerifyAlive 验活: 刷新 Token + 查用量 + 查模型。
//
// 判死逻辑：
//   - Token 刷新返回 401/403 → 账号已吊销/封禁，返回 suspended
//   - Q 端点（getUsageLimits / ListAvailableModels）出现一次 403 → 注册失败
//   - Token 刷新成功且 Q 端点非 403 → 账号存活
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

	arn := resolveVerifyProfileArn(client, access)
	usageRes := queryQEndpoint(client, access, "getUsageLimits", "origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true", arn)
	if !usageRes.ok {
		log.Printf("[验活] getUsageLimits 不可达 (status=%d)，跳过用量查询", usageRes.statusCode)
	}

	modelRes := queryQEndpoint(client, access, "ListAvailableModels", "origin=AI_EDITOR", arn)
	if usageRes.statusCode == 403 {
		return map[string]interface{}{"alive": false, "error": "Q 端点 403 [usage]"}
	}
	if modelRes.statusCode == 403 {
		return map[string]interface{}{"alive": false, "error": "Q 端点 403 [models]"}
	}
	if !modelRes.ok {
		log.Printf("[验活] ListAvailableModels 不可达 (status=%d)，跳过模型查询", modelRes.statusCode)
	}

	if usageRes.ok && len(usageRes.body) > 0 {
		return r.parseUsage(usageRes.body)
	}

	log.Println("[验活] Token 有效，Q 端点暂不可达，账号仍视为存活")
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
		log.Printf("[验活] Q 端点 403 [%s]，注册失败", label)
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


func effectiveVerifyArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" || strings.Contains(arn, "AAAACCCCXXXX") {
		return ""
	}
	return arn
}

func qEndpointURL(path, query, profileArn string) string {
	u := "https://q.us-east-1.amazonaws.com/" + path + "?" + query
	if arn := effectiveVerifyArn(profileArn); arn != "" {
		u += "&profileArn=" + url.QueryEscape(arn)
	}
	return u
}

func resolveVerifyProfileArn(client interface {
	Do(req *fhttp.Request) (*fhttp.Response, error)
}, access string) string {
	body, _ := json.Marshal(map[string]interface{}{"maxResults": 10})
	req, err := fhttp.NewRequest("POST", "https://codewhisperer.us-east-1.amazonaws.com", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("x-amz-target", "AmazonCodeWhispererService.ListAvailableProfiles")
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var data map[string]interface{}
	if json.Unmarshal(respBody, &data) != nil {
		return ""
	}
	raw, _ := data["profiles"].([]interface{})
	for _, item := range raw {
		p, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range []string{"arn", "profileArn"} {
			if s, _ := p[key].(string); effectiveVerifyArn(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func queryQEndpoint(client interface {
	Do(req *fhttp.Request) (*fhttp.Response, error)
}, access, path, query, profileArn string) endpointResult {
	res := queryGetEndpointWithRetry(client, access, qEndpointURL(path, query, profileArn))
	if res.ok {
		return res
	}
	if profileArn != "" {
		return queryGetEndpointWithRetry(client, access, qEndpointURL(path, query, ""))
	}
	return res
}

// queryGetEndpointWithRetry 带重试的 GET 端点查询。
// 网络错误最多重试 3 次；403 立即返回，不重试。
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
		break
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
