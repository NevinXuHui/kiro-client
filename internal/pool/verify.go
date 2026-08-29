package pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// VerifyResult 验活结果
type VerifyResult struct {
	Alive        bool    `json:"alive"`
	Status       string  `json:"status"`       // active / suspended / unknown
	Error        string  `json:"error"`
	Email        string  `json:"email"`
	Subscription string  `json:"subscription"`
	CreditUsed   float64 `json:"creditUsed"`
	CreditLimit  float64 `json:"creditLimit"`
	Provider     string  `json:"provider"`
	NewToken     string  `json:"newToken"`
	NewRefresh   string  `json:"newRefresh"`
}

// VerifyAccount 验活单个账号
func VerifyAccount(acc *Account, proxy string, timeout int) *VerifyResult {
	if timeout <= 0 {
		timeout = 15
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}

	// Step 1: 刷新 Token
	accessToken, newRefresh, provider, err := refreshToken(client, acc)
	if err != nil {
		if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "401") {
			return &VerifyResult{
				Alive:  false,
				Status: "suspended",
				Error:  "token 刷新失败（账号已吊销）: " + err.Error(),
			}
		}
		return &VerifyResult{
			Alive:  false,
			Status: "unknown",
			Error:  err.Error(),
		}
	}

	// Step 2: 并行探测用量和模型
	var (
		usage   *usageInfo
		uErr    error
		mStatus int
		wg      sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		usage, uErr = fetchUsage(client, accessToken)
	}()
	go func() {
		defer wg.Done()
		mStatus, _ = probeModels(client, accessToken)
	}()
	wg.Wait()

	// Step 3: 判定状态
	if uErr != nil && strings.Contains(uErr.Error(), "403") {
		return &VerifyResult{
			Alive:  false,
			Status: "suspended",
			Error:  "用量查询 403（账号已封禁）",
		}
	}
	if mStatus == 403 {
		return &VerifyResult{
			Alive:  false,
			Status: "suspended",
			Error:  "模型列表 403（账号已封禁）",
		}
	}

	result := &VerifyResult{
		Alive:      true,
		Status:     "active",
		NewToken:   accessToken,
		NewRefresh: newRefresh,
		Provider:   provider,
	}

	if usage != nil {
		result.Email = usage.Email
		result.Subscription = usage.Subscription
		result.CreditUsed = usage.CreditUsed
		result.CreditLimit = usage.CreditLimit
	}

	return result
}

// refreshToken 刷新 accessToken，优先尝试 IDC 端点，回退 Social 端点
func refreshToken(client *http.Client, acc *Account) (accessToken, newRefresh, provider string, err error) {
	// 优先尝试 IDC 端点（需要 clientId/clientSecret）
	if acc.ClientID != "" && acc.ClientSecret != "" {
		token, refresh, err := tryRefreshIDC(client, acc)
		if err == nil {
			return token, refresh, "idc", nil
		}
		if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "401") {
			return "", "", "idc", err
		}
		log.Printf("[验活] IDC 刷新失败，尝试 Social: %v", err)
	}

	// 回退 Social 端点（只需 refreshToken）
	token, refresh, err := tryRefreshSocial(client, acc.RefreshToken)
	if err != nil {
		return "", "", "social", err
	}
	return token, refresh, "social", nil
}

func tryRefreshIDC(client *http.Client, acc *Account) (string, string, error) {
	body, _ := json.Marshal(map[string]string{
		"clientId":     acc.ClientID,
		"clientSecret": acc.ClientSecret,
		"refreshToken": acc.RefreshToken,
		"grantType":    "refresh_token",
	})

	req, _ := http.NewRequest("POST", "https://oidc.us-east-1.amazonaws.com/token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("IDC 请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("IDC HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var tr struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", "", fmt.Errorf("IDC 解析失败: %w", err)
	}
	if tr.AccessToken == "" {
		return "", "", fmt.Errorf("IDC 返回空 accessToken")
	}
	if tr.RefreshToken == "" {
		tr.RefreshToken = acc.RefreshToken
	}
	return tr.AccessToken, tr.RefreshToken, nil
}

func tryRefreshSocial(client *http.Client, refreshToken string) (string, string, error) {
	body, _ := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
	})

	req, _ := http.NewRequest("POST", "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("Social 请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("Social HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var tr struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", "", fmt.Errorf("Social 解析失败: %w", err)
	}
	if tr.AccessToken == "" {
		return "", "", fmt.Errorf("Social 返回空 accessToken")
	}
	if tr.RefreshToken == "" {
		tr.RefreshToken = refreshToken
	}
	return tr.AccessToken, tr.RefreshToken, nil
}

type usageInfo struct {
	Email        string
	Subscription string
	CreditUsed   float64
	CreditLimit  float64
}

func fetchUsage(client *http.Client, accessToken string) (*usageInfo, error) {
	url := "https://q.us-east-1.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true&profileArn=" +
		"arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aws-sdk-js/1.0.0 KiroIDE-2.3.0-kiroclient")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("用量查询失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("用量查询 HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var usage map[string]interface{}
	json.Unmarshal(raw, &usage)

	info := &usageInfo{}
	if userInfo, ok := usage["userInfo"].(map[string]interface{}); ok {
		info.Email, _ = userInfo["email"].(string)
	}
	if subInfo, ok := usage["subscriptionInfo"].(map[string]interface{}); ok {
		info.Subscription, _ = subInfo["subscriptionTitle"].(string)
	}
	if info.Subscription == "" {
		info.Subscription = "Free"
	}

	// 解析额度
	if breakdown, ok := usage["usageBreakdownList"].([]interface{}); ok {
		for _, item := range breakdown {
			b, _ := item.(map[string]interface{})
			rt, _ := b["resourceType"].(string)
			if rt == "CREDIT" {
				info.CreditLimit, _ = b["usageLimitWithPrecision"].(float64)
				if info.CreditLimit == 0 {
					info.CreditLimit, _ = b["usageLimit"].(float64)
				}
				info.CreditUsed, _ = b["currentUsageWithPrecision"].(float64)
				if info.CreditUsed == 0 {
					info.CreditUsed, _ = b["currentUsage"].(float64)
				}

				// 免费试用额度
				if ft, ok := b["freeTrialInfo"].(map[string]interface{}); ok {
					if status, _ := ft["freeTrialStatus"].(string); status == "ACTIVE" {
						ftLimit, _ := ft["usageLimitWithPrecision"].(float64)
						ftUsed, _ := ft["currentUsageWithPrecision"].(float64)
						info.CreditLimit += ftLimit
						info.CreditUsed += ftUsed
					}
				}
				break
			}
		}
	}

	return info, nil
}

func probeModels(client *http.Client, accessToken string) (int, error) {
	url := "https://q.us-east-1.amazonaws.com/ListAvailableModels?origin=AI_EDITOR&profileArn=" +
		"arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aws-sdk-js/1.0.0 KiroIDE-2.3.0-kiroclient")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("模型列表请求失败: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // drain body

	return resp.StatusCode, nil
}

// VerifyAndUpdateAccount 验活并更新账号信息
func VerifyAndUpdateAccount(acc *Account, proxy string, timeout int) error {
	result := VerifyAccount(acc, proxy, timeout)

	acc.HealthStatus = result.Status
	acc.HealthError = result.Error
	acc.HealthCheckedAt = time.Now().Format(time.RFC3339)

	if result.Alive {
		if result.Email != "" {
			acc.Email = result.Email
		}
		if result.Subscription != "" {
			acc.Subscription = result.Subscription
		}
		acc.CreditUsed = int(result.CreditUsed)
		acc.CreditLimit = int(result.CreditLimit)
		if result.Provider != "" {
			acc.Provider = result.Provider
		}
		// 更新 Token（如果有轮换）
		if result.NewRefresh != "" && result.NewRefresh != acc.RefreshToken {
			acc.RefreshToken = result.NewRefresh
		}
	}

	return nil
}

// BatchVerify 批量验活账号
func BatchVerify(accounts []*Account, proxy string, concurrency int) {
	if concurrency <= 0 {
		concurrency = 3
	}

	jobs := make(chan *Account, len(accounts))
	var wg sync.WaitGroup

	// 启动 worker
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for acc := range jobs {
				log.Printf("[验活] 检查账号: %s", acc.Email)
				if err := VerifyAndUpdateAccount(acc, proxy, 15); err != nil {
					log.Printf("[验活] 失败 %s: %v", acc.Email, err)
				} else {
					log.Printf("[验活] 完成 %s: %s", acc.Email, acc.HealthStatus)
				}
			}
		}()
	}

	// 投递任务
	for _, acc := range accounts {
		jobs <- acc
	}
	close(jobs)

	wg.Wait()
	log.Printf("[验活] 批量检查完成，共 %d 个账号", len(accounts))
}
