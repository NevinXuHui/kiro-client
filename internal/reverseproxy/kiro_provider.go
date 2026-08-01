package reverseproxy

// 本文件（阶段 7）为 Provider 抽象层：把阶段 2-6 迁移的 9router Kiro 模块
// （翻译器/刷新/冷却/会话）串成完整 Provider 流水线，并向项目既有网关
// （proxy.go / pool.go）提供接入点。
//
// 对齐的 TS 源：
//   - open-sse/executors/kiro.js buildHeaders（L18-47）、getOrderedBaseUrls（L64-85）
//   - open-sse/executors/base.js execute 的主机轮换语义（429 切主机、网络/5xx 换端）
//   - open-sse/handlers/chatCore.js 的令牌确保 + 刷新重试（L327-346）
//   - open-sse/services/accountFallback.js 错误→冷却（阶段 6 已迁，此处接入）
//
// 接入示例（proxy.go handleChat 已按此整合，legacy 链路已删除）：
//
//	cred := KiroCredentialFromAccount(acc)
//	tr, err := KiroTranslateChatRequest(&req, model, cred, headerMap(r.Header))
//	// 令牌确保：KiroEnsureAccessToken(cred, proxy)
//	resp, err := KiroSendChat(tr, cred, proxy)
//	// 失败冷却：KiroApplyGatewayError(acc, model, status, errText, time.Now())
//	// 流式响应：ParseEventStream（已对齐 KiroExecutor 事件语义）。
//
// 熔断保护：KiroCircuitTripped() 开启时网关直接 503 短路。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

// ===== 凭证桥接 =====

// KiroCredentialFromAccount 网关账号 → KiroCredential。
// AuthMethod 由 Provider 标签推导（注册机产物默认 builder-id；OAuth 导入的
// 连接标签见 KiroConnection 的 ProviderSpecificData）。
func KiroCredentialFromAccount(acc *Account) *KiroCredential {
	if acc == nil {
		return nil
	}
	authMethod := "builder-id"
	switch acc.Provider {
	case "API Key":
		authMethod = "api_key"
	case "Enterprise":
		authMethod = "idc"
	case "Imported":
		authMethod = "imported"
	case "Google":
		authMethod = "google"
	case "Github":
		authMethod = "github"
	}
	return &KiroCredential{
		ConnectionId: acc.Email,
		Email:        acc.Email,
		AccessToken:  acc.accessToken,
		RefreshToken: acc.RefreshToken,
		ExpiresAtMs:  acc.accessTokenExpiresAt / 1e6, // unix nano → epoch ms（0=未知）
		ProviderSpecificData: KiroProviderSpecificData{
			AuthMethod:   authMethod,
			ClientId:     acc.ClientID,
			ClientSecret: acc.ClientSecret,
			Region:       acc.Region,
			ProfileArn:   acc.ProfileArn,
		},
	}
}

// KiroCredentialFromConnection OAuth 连接（阶段 4 KiroConnection）→ KiroCredential。
func KiroCredentialFromConnection(conn *KiroConnection) *KiroCredential {
	if conn == nil {
		return nil
	}
	return &KiroCredential{
		ConnectionId:         conn.Email,
		Email:                conn.Email,
		AccessToken:          conn.AccessToken,
		RefreshToken:         conn.RefreshToken,
		ExpiresAtMs:          conn.ExpiresAt.UnixMilli(),
		ProviderSpecificData: conn.ProviderSpecificData,
	}
}

// ChatRequestToBody ChatRequest → 通用 body map（接入 OpenAIToKiroRequest 的桥）。
func ChatRequestToBody(req *ChatRequest) map[string]interface{} {
	if req == nil {
		return map[string]interface{}{}
	}
	b, err := json.Marshal(req)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if json.Unmarshal(b, &m) != nil {
		return map[string]interface{}{}
	}
	return m
}

// headerMap http.Header → map[string]string（小写键，供会话解析/翻译器使用）。
func headerMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			m[strings.ToLower(k)] = v[0]
		}
	}
	return m
}

// KiroTranslateChatRequest 接入示例：OpenAI ChatRequest → Kiro 翻译请求
// （OpenAIToKiroRequest + 凭证桥接）。
func KiroTranslateChatRequest(req *ChatRequest, model string, cred *KiroCredential, headers map[string]string) (*KiroTranslatedRequest, error) {
	if cred == nil {
		return nil, errors.New("no credentials")
	}
	return OpenAIToKiroRequest(model, ChatRequestToBody(req), &KiroTranslatorCredentials{
		RawHeaders:           headers,
		ConnectionID:         cred.ConnectionId,
		ProviderSpecificData: cred.ProviderSpecificData,
	})
}

// ===== executor 请求头与主机序（kiro.js buildHeaders / getOrderedBaseUrls） =====

// KiroExecutorBuildHeaders 对齐 KiroExecutor.buildHeaders：
//   - 固定头组（registry transport.headers）+ Amz-Sdk-Request/Invocation-Id
//   - api_key → Bearer + `tokentype: API_KEY`（长寿命 key 语义）
//   - accessToken → Bearer；external_idp 追加 `TokenType: EXTERNAL_IDP`（大写）
//   - 无 accessToken → 不带 Authorization
func KiroExecutorBuildHeaders(cred *KiroCredential) map[string]string {
	headers := map[string]string{
		"Content-Type":          "application/json",
		"Accept":                "application/vnd.amazon.eventstream",
		"X-Amz-Target":          "AmazonCodeWhispererStreamingService.GenerateAssistantResponse",
		"User-Agent":            "AWS-SDK-JS/3.0.0 kiro-ide/1.0.0",
		"X-Amz-User-Agent":      "aws-sdk-js/3.0.0 kiro-ide/1.0.0",
		"Amz-Sdk-Request":       "attempt=1; max=3",
		"Amz-Sdk-Invocation-Id": uuid.NewString(),
	}
	if cred == nil {
		return headers
	}
	authMethod := cred.ProviderSpecificData.AuthMethod
	isApiKey := authMethod == "api_key"
	isExternalIdp := authMethod == "external_idp"

	apiKey := cred.ApiKey
	if apiKey == "" && isApiKey {
		apiKey = cred.AccessToken
	}
	if isApiKey && apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
		headers["tokentype"] = "API_KEY"
	} else if cred.AccessToken != "" {
		headers["Authorization"] = "Bearer " + cred.AccessToken
		if isExternalIdp {
			headers["TokenType"] = "EXTERNAL_IDP"
		}
	}
	return headers
}

var kiroAmazonHostRE = regexp.MustCompile(`([a-z]+)\.[a-z0-9-]+\.amazonaws\.com`)

// KiroOrderedBaseURLs 对齐 KiroExecutor.getOrderedBaseUrls：
// api_key / external_idp / idc 走 CodeWhisperer 面（amazonaws 主机置前并按
// 区域改写，kiro.dev 网关拒绝这三类 token）；其余 authMethod 保持默认序
// （kiro.dev 优先）。
func KiroOrderedBaseURLs(authMethod, region string) []string {
	isCodeWhispererSurface := authMethod == "api_key" || authMethod == "external_idp" || authMethod == "idc"
	if !isCodeWhispererSurface {
		return append([]string{}, kiroEndpoints...)
	}

	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	regionalize := func(u string) string {
		if region != "" && region != "us-east-1" && strings.Contains(u, "amazonaws.com") {
			return kiroAmazonHostRE.ReplaceAllString(u, "$1."+region+".amazonaws.com")
		}
		return u
	}

	var amazon []string
	var others []string
	for _, u := range kiroEndpoints {
		if strings.Contains(u, "amazonaws.com") {
			amazon = append(amazon, regionalize(u))
		} else {
			others = append(others, u)
		}
	}
	if len(amazon) > 0 {
		return append(amazon, others...)
	}
	return append([]string{}, kiroEndpoints...)
}

// ===== 发送与令牌确保 =====

// newKiroChatClient 60s 连接超时的对话客户端（JS AbortController
// timeoutMs = config.timeoutMs || FETCH_CONNECT_TIMEOUT_MS；其余参数与项目
// 既有 newClient 一致）。
func newKiroChatClient(proxy string) tls_client.HttpClient {
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(FetchConnectTimeoutMs / 1000)),
		tls_client.WithClientProfile(profiles.Chrome_144),
		tls_client.WithInsecureSkipVerify(),
	}
	if proxy != "" {
		opts = append(opts, tls_client.WithProxyUrl(proxy))
	}
	c, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		panic(fmt.Sprintf("create chat client: %v", err))
	}
	return c
}

// KiroSendChat 对齐 BaseExecutor.execute：每 URL 原地重试（retryConfig 按状态码）
// + 主机轮换（429 shouldRetry 语义），经 getOrderedBaseUrls 有序主机 +
// buildHeaders 请求头。
//
//   - 200 → 返回原始 resp（body 未消费，供调用方解析事件流）
//   - 非 200（重试耗尽）→ **返回 resp 而非 error**（JS 语义：executor 不吞响应，
//     由调用方读 body、分类状态码、触发账号回退/冷却）
//   - 429 → 有后续主机则切换（shouldRetry，不消耗重试次数），末位主机返回 resp
//   - 网络/连接异常 → 映射 502 重试配置（3×3000ms）原地重试，耗尽后换端
//
// retryConfig = {...DEFAULT_RETRY_CONFIG, ...kiro registry retry {"429": 0}}：
// 502: 3×3000ms / 503: 3×2000ms / 504: 2×3000ms。
func KiroSendChat(tr *KiroTranslatedRequest, cred *KiroCredential, proxy string) (*fhttp.Response, error) {
	if tr == nil || tr.Payload == nil {
		return nil, errors.New("nil translated request")
	}
	body, err := json.Marshal(tr.Payload)
	if err != nil {
		return nil, err
	}
	headers := KiroExecutorBuildHeaders(cred)

	region := ""
	authMethod := ""
	if cred != nil {
		region = cred.ProviderSpecificData.Region
		authMethod = cred.ProviderSpecificData.AuthMethod
	}
	urls := KiroOrderedBaseURLs(authMethod, region)
	fallbackCount := len(urls)

	// retryConfig = { ...DEFAULT_RETRY_CONFIG, ...config.retry }
	retryConfig := make(map[int]RetryEntry, len(DefaultRetryConfig))
	for k, v := range DefaultRetryConfig {
		retryConfig[k] = v
	}
	retryConfig[429] = RetryEntry{Attempts: 0, DelayMs: 0} // kiro registry retry: {"429": 0}

	client := newKiroChatClient(proxy)
	var lastError error
	lastStatus := 0
	retryAttemptsByURL := map[int]int{}

	// tryRetry：按状态码的原地重试；attempts<=0 或已达上限返回 false（换主机/返回响应）
	tryRetry := func(urlIndex, statusKey int) bool {
		entry := ResolveRetryEntry(retryConfig[statusKey])
		if entry.Attempts <= 0 || retryAttemptsByURL[urlIndex] >= entry.Attempts {
			return false
		}
		retryAttemptsByURL[urlIndex]++
		time.Sleep(time.Duration(entry.DelayMs) * time.Millisecond)
		return true
	}

	for urlIndex := 0; urlIndex < fallbackCount; urlIndex++ {
		url := urls[urlIndex]
		httpReq, _ := fhttp.NewRequest("POST", url, bytes.NewReader(body))
		for k, v := range headers {
			httpReq.Header.Set(k, v)
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			// 网络/抓取异常 → 映射 502 重试配置（连接超时亦归此路径）
			lastError = fmt.Errorf("%s: %w", url, err)
			if tryRetry(urlIndex, HTTPStatusBadGateway) {
				urlIndex--
				continue
			}
			if urlIndex+1 < fallbackCount {
				continue
			}
			return nil, lastError
		}
		if resp.StatusCode == 200 {
			return resp, nil
		}
		// 非 200：不消费 body（JS executor 返回原始 response，由调用方读 body 分类）
		lastStatus = resp.StatusCode

		if tryRetry(urlIndex, resp.StatusCode) {
			urlIndex--
			continue
		}

		// shouldRetry：429 && 有后续主机 → 切主机（不消耗重试次数）
		if resp.StatusCode == 429 && urlIndex+1 < fallbackCount {
			continue
		}

		// 重试耗尽/无配置：返回原始响应（JS executor 不吞非 200）
		return resp, nil
	}

	if lastError != nil {
		return nil, lastError
	}
	return nil, fmt.Errorf("all %d URLs failed with status %d", fallbackCount, lastStatus)
}

// KiroEnsureAccessToken 对齐 chatCore 的令牌确保：accessToken 缺失或临近过期
// （5min 缓冲，TOKEN_EXPIRY_BUFFER_MS）时经 RefreshKiroToken（三分支 + 并发去重）
// 刷新，失败重试 3 次（refreshWithRetry 语义）。刷新成功回写凭证。
func KiroEnsureAccessToken(cred *KiroCredential, proxy string) error {
	if cred == nil {
		return errors.New("no credentials")
	}
	if cred.AccessToken != "" && (cred.ExpiresAtMs <= 0 || time.Now().UnixMilli() < cred.ExpiresAtMs-TokenExpiryBufferMs) {
		return nil // 未临期（未知过期时间视为不刷，同 9router isTokenExpiringSoon）
	}

	refreshed := RefreshWithRetry(func() (*KiroRefreshResult, error) {
		return RefreshKiroToken(cred.RefreshToken, cred.ProviderSpecificData, proxy), nil
	}, 3)
	if refreshed == nil || refreshed.AccessToken == "" {
		return errors.New("token refresh failed")
	}
	cred.AccessToken = refreshed.AccessToken
	if refreshed.RefreshToken != "" {
		cred.RefreshToken = refreshed.RefreshToken
	}
	if refreshed.ExpiresIn > 0 {
		cred.ExpiresAtMs = time.Now().UnixMilli() + int64(refreshed.ExpiresIn)*1000
	}
	return nil
}

// ===== 错误 → 冷却接入（accountFallback 语义落到网关账号） =====

// KiroApplyGatewayError 把上游错误经 KiroCheckFallbackError（ERROR_RULES 顺序
// 匹配）写入网关账号：per-model 锁（modelLock_<model> 语义）+ 退避级别 +
// lastErrorCode。429 走指数退避，401/402/403/404 固定 2min，文本规则优先。
func KiroApplyGatewayError(acc *Account, model string, status int, errText string, now time.Time) {
	if acc == nil {
		return
	}
	res := KiroCheckFallbackError(status, errText, acc.backoffLevel)
	var cooldownUntil int64
	if res.CooldownMs > 0 {
		cooldownUntil = now.Add(time.Duration(res.CooldownMs) * time.Millisecond).UnixNano()
	}
	if acc.modelLocks == nil {
		acc.modelLocks = map[string]int64{}
	}
	acc.modelLocks[model] = cooldownUntil
	if res.HasNewBackoffLevel {
		acc.backoffLevel = res.NewBackoffLevel
	}
	acc.lastErrorCode = status
}

// KiroApplyGatewaySuccess 请求成功：退避级别归零 + 清该模型锁
// （9router clearAccountError 语义）。
func KiroApplyGatewaySuccess(acc *Account, model string) {
	if acc == nil {
		return
	}
	acc.backoffLevel = 0
	delete(acc.modelLocks, model)
}
