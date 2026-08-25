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

const kiroRESTIDEVersion = "2.3.0"

// EffectiveProfileArn 过滤 BuilderID 占位 ARN。IdC 真实 ARN 原样返回。
// 聊天/企业解析用这个；用量与 ListAvailableModels 必须用 UsageQueryProfileArn。
func EffectiveProfileArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" || strings.Contains(arn, "AAAACCCCXXXX") {
		return ""
	}
	return arn
}

// UsageQueryProfileArn 用量/模型 REST 实际发送的 ARN。
// 空则回退 BuilderID 占位符；占位符原样发送（省略会 400 Invalid profileArn）。
func UsageQueryProfileArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return KiroDefaultProfileARN
	}
	return arn
}

func applyQRESTHeaders(h fhttp.Header, accessToken string) {
	h.Set("Authorization", "Bearer "+accessToken)
	h.Set("Accept", "application/json")
	h.Set("User-Agent", "aws-sdk-js/1.0.0 ua/2.1 os/linux lang/js md/nodejs#22.22.0 api/codewhispererruntime#1.0.0 m/N,E KiroIDE-"+kiroRESTIDEVersion+"-kiroclient")
	h.Set("x-amz-user-agent", "aws-sdk-js/1.0.0 KiroIDE-"+kiroRESTIDEVersion+"-kiroclient")
}

// UsageLimitsURL 构造 getUsageLimits GET URL，始终带 profileArn。
func UsageLimitsURL(region, profileArn string) string {
	if region == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf(
		"https://q.%s.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true&profileArn=%s",
		region,
		url.QueryEscape(UsageQueryProfileArn(profileArn)),
	)
}

// AvailableModelsURL 构造 ListAvailableModels GET URL，始终带 profileArn。
func AvailableModelsURL(region, profileArn string) string {
	if region == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf(
		"https://q.%s.amazonaws.com/ListAvailableModels?origin=AI_EDITOR&profileArn=%s",
		region,
		url.QueryEscape(UsageQueryProfileArn(profileArn)),
	)
}

// ResolveUsageProfileArn 优先真实 ARN；否则 ListAvailableProfiles；再否则 BuilderID 占位符。
func ResolveUsageProfileArn(accessToken, region, proxy, profileArn string) string {
	if arn := EffectiveProfileArn(profileArn); arn != "" {
		return arn
	}
	if accessToken != "" {
		if region == "" {
			region = "us-east-1"
		}
		if resolved, err := ListAvailableProfiles(accessToken, region, proxy); err == nil {
			if arn := EffectiveProfileArn(resolved); arn != "" {
				return arn
			}
		}
	}
	return UsageQueryProfileArn(profileArn)
}

// GetUsageLimits 查询额度。始终带 profileArn（含 BuilderID 占位符）。
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
	sendArn := UsageQueryProfileArn(arn)
	attempts := []attempt{
		{
			name: "codewhisperer-post",
			run: func() (*fhttp.Response, error) {
				payload := map[string]interface{}{
					"origin":       "AI_EDITOR",
					"resourceType": "AGENTIC_REQUEST",
					"profileArn":   sendArn,
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
				req, _ := fhttp.NewRequest("GET", UsageLimitsURL(region, sendArn), nil)
				applyQRESTHeaders(req.Header, accessToken)
				return client.Do(req)
			},
		},
	}

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
