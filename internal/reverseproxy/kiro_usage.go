package reverseproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

// EffectiveProfileArn 过滤 BuilderID 占位 ARN。IdC 真实 ARN 原样返回。
func EffectiveProfileArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" || strings.Contains(arn, "AAAACCCCXXXX") {
		return ""
	}
	return arn
}

// UsageLimitsURL 构造 getUsageLimits GET URL；真实 ARN 以 query 追加。
func UsageLimitsURL(region, profileArn string) string {
	if region == "" {
		region = "us-east-1"
	}
	u := fmt.Sprintf("https://q.%s.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST", region)
	if arn := EffectiveProfileArn(profileArn); arn != "" {
		u += "&profileArn=" + url.QueryEscape(arn)
	}
	return u
}

// AvailableModelsURL 构造 ListAvailableModels GET URL。
func AvailableModelsURL(region, profileArn string) string {
	if region == "" {
		region = "us-east-1"
	}
	u := fmt.Sprintf("https://q.%s.amazonaws.com/ListAvailableModels?origin=AI_EDITOR", region)
	if arn := EffectiveProfileArn(profileArn); arn != "" {
		u += "&profileArn=" + url.QueryEscape(arn)
	}
	return u
}

// ResolveUsageProfileArn 优先用已有真实 ARN；否则 ListAvailableProfiles 解析并过滤占位符。
func ResolveUsageProfileArn(accessToken, region, proxy, profileArn string) string {
	if arn := EffectiveProfileArn(profileArn); arn != "" {
		return arn
	}
	if accessToken == "" {
		return ""
	}
	if region == "" {
		region = "us-east-1"
	}
	resolved, err := ListAvailableProfiles(accessToken, region, proxy)
	if err != nil {
		return ""
	}
	return EffectiveProfileArn(resolved)
}

// GetUsageLimits 查询额度。每个区域先带真实 ARN，403/400 再回退不带 ARN。
func GetUsageLimits(accessToken, region, proxy, profileArn string) (float64, float64, string, error) {
	if region == "" {
		region = "us-east-1"
	}
	arn := ResolveUsageProfileArn(accessToken, region, proxy, profileArn)
	client := newClient(proxy)

	type attempt struct {
		name string
		run  func() (*fhttp.Response, error)
	}
	makeAttempts := func(a string) []attempt {
		return []attempt{
			{
				name: "codewhisperer-post",
				run: func() (*fhttp.Response, error) {
					payload := map[string]interface{}{
						"origin":       "AI_EDITOR",
						"resourceType": "AGENTIC_REQUEST",
					}
					if a != "" {
						payload["profileArn"] = a
					}
					body, _ := json.Marshal(payload)
					u := fmt.Sprintf("https://codewhisperer.%s.amazonaws.com", region)
					req, _ := fhttp.NewRequest("POST", u, bytes.NewReader(body))
					req.Header.Set("Authorization", "Bearer "+accessToken)
					req.Header.Set("Content-Type", "application/x-amz-json-1.0")
					req.Header.Set("x-amz-target", "AmazonCodeWhispererService.GetUsageLimits")
					req.Header.Set("Accept", "application/json")
					return client.Do(req)
				},
			},
			{
				name: "q-get",
				run: func() (*fhttp.Response, error) {
					req, _ := fhttp.NewRequest("GET", UsageLimitsURL(region, a), nil)
					req.Header.Set("Authorization", "Bearer "+accessToken)
					req.Header.Set("Accept", "application/json")
					return client.Do(req)
				},
			},
		}
	}

	var attempts []attempt
	if arn != "" {
		attempts = append(attempts, makeAttempts(arn)...)
	}
	attempts = append(attempts, makeAttempts("")...)

	var lastErr error
	for _, a := range attempts {
		resp, err := a.run()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", a.name, err)
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("%s: HTTP %d", a.name, resp.StatusCode)
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal(respBody, &data); err != nil {
			lastErr = fmt.Errorf("%s: parse error", a.name)
			continue
		}
		used, limit, resetAt := parseUsageResponse(data)
		return used, limit, resetAt, nil
	}
	if lastErr != nil {
		return 0, 0, "", lastErr
	}
	return 0, 0, "", fmt.Errorf("all usage endpoints failed")
}

// NewQuotaRefreshFunc 创建额度刷新回调。
func NewQuotaRefreshFunc() QuotaRefreshFunc {
	return func(accessToken, region, proxy, profileArn string) (float64, float64, string, error) {
		return GetUsageLimits(accessToken, region, proxy, profileArn)
	}
}
