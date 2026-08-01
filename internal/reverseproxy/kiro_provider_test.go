package reverseproxy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ===== kiro_provider.go（Provider 抽象层） =====

func TestKiroExecutorBuildHeaders(t *testing.T) {
	// 固定头组
	h := KiroExecutorBuildHeaders(nil)
	if h["Amz-Sdk-Request"] != "attempt=1; max=3" || h["X-Amz-Target"] != "AmazonCodeWhispererStreamingService.GenerateAssistantResponse" ||
		h["Accept"] != "application/vnd.amazon.eventstream" || h["User-Agent"] != "AWS-SDK-JS/3.0.0 kiro-ide/1.0.0" {
		t.Errorf("固定头缺失: %+v", h)
	}
	if _, ok := h["Authorization"]; ok {
		t.Errorf("无凭证不应带 Authorization")
	}
	if h["Amz-Sdk-Invocation-Id"] == "" {
		t.Errorf("缺 invocation id")
	}

	// builder-id → Bearer only
	cred := &KiroCredential{AccessToken: "at", ProviderSpecificData: KiroProviderSpecificData{AuthMethod: "builder-id"}}
	h2 := KiroExecutorBuildHeaders(cred)
	if h2["Authorization"] != "Bearer at" {
		t.Errorf("builder-id=%+v", h2)
	}
	if _, ok := h2["tokentype"]; ok {
		t.Errorf("builder-id 不应有 tokentype")
	}

	// api_key → Bearer + 小写 tokentype
	apiKey := &KiroCredential{AccessToken: "key", ProviderSpecificData: KiroProviderSpecificData{AuthMethod: "api_key"}}
	h3 := KiroExecutorBuildHeaders(apiKey)
	if h3["Authorization"] != "Bearer key" || h3["tokentype"] != "API_KEY" {
		t.Errorf("api_key=%+v", h3)
	}
	if _, ok := h3["TokenType"]; ok {
		t.Errorf("api_key 不应有 TokenType")
	}

	// external_idp → Bearer + 大写 TokenType
	ext := &KiroCredential{AccessToken: "et", ProviderSpecificData: KiroProviderSpecificData{AuthMethod: "external_idp"}}
	h4 := KiroExecutorBuildHeaders(ext)
	if h4["Authorization"] != "Bearer et" || h4["TokenType"] != "EXTERNAL_IDP" {
		t.Errorf("external_idp=%+v", h4)
	}
	if _, ok := h4["tokentype"]; ok {
		t.Errorf("external_idp 不应有小写 tokentype")
	}

	// apiKey 字段优先
	apiKey2 := &KiroCredential{ApiKey: "custom", AccessToken: "at", ProviderSpecificData: KiroProviderSpecificData{AuthMethod: "api_key"}}
	h5 := KiroExecutorBuildHeaders(apiKey2)
	if h5["Authorization"] != "Bearer custom" {
		t.Errorf("apiKey 字段优先=%+v", h5)
	}
}

func TestKiroOrderedBaseURLs(t *testing.T) {
	// 默认序（OAuth/social）：kiro.dev 优先
	base := KiroOrderedBaseURLs("builder-id", "")
	if len(base) != 3 || !strings.HasPrefix(base[0], "https://runtime.us-east-1.kiro.dev") {
		t.Errorf("builder-id 序=%v", base)
	}

	// api_key us-east-1 → amazonaws 前置（原区域）
	cw := KiroOrderedBaseURLs("api_key", "us-east-1")
	if !strings.HasPrefix(cw[0], "https://codewhisperer.us-east-1.amazonaws.com") ||
		!strings.HasPrefix(cw[1], "https://q.us-east-1.amazonaws.com") ||
		!strings.HasPrefix(cw[2], "https://runtime.us-east-1.kiro.dev") {
		t.Errorf("api_key 序=%v", cw)
	}

	// idc eu-central-1 → amazonaws 区域改写
	idc := KiroOrderedBaseURLs("idc", "eu-central-1")
	if !strings.HasPrefix(idc[0], "https://codewhisperer.eu-central-1.amazonaws.com") ||
		!strings.HasPrefix(idc[1], "https://q.eu-central-1.amazonaws.com") {
		t.Errorf("idc 区域改写=%v", idc)
	}

	// external_idp 同面
	ext := KiroOrderedBaseURLs("external_idp", "us-east-1")
	if !strings.HasPrefix(ext[0], "https://codewhisperer.us-east-1.amazonaws.com") {
		t.Errorf("external_idp 序=%v", ext)
	}
}

func TestKiroCredentialFromAccount(t *testing.T) {
	acc := &Account{
		ClientID:             "cid",
		ClientSecret:         "csec",
		RefreshToken:         "rt",
		Region:               "us-east-1",
		Email:                "a@b.c",
		Provider:             "BuilderId",
		ProfileArn:           "arn:aws:codewhisperer:us-east-1:1:profile/p",
		accessToken:          "at",
		accessTokenExpiresAt: 1700000000000000000, // unix nano → 1700000000 s
	}
	cred := KiroCredentialFromAccount(acc)
	if cred == nil || cred.AccessToken != "at" || cred.RefreshToken != "rt" ||
		cred.ProviderSpecificData.AuthMethod != "builder-id" ||
		cred.ProviderSpecificData.ClientId != "cid" ||
		cred.ProviderSpecificData.ProfileArn != acc.ProfileArn {
		t.Errorf("桥接=%+v", cred)
	}
	if cred.ExpiresAtMs != 1700000000000 {
		t.Errorf("ExpiresAtMs=%d", cred.ExpiresAtMs)
	}
	// Provider 标签映射
	if KiroCredentialFromAccount(&Account{Provider: "API Key"}).ProviderSpecificData.AuthMethod != "api_key" {
		t.Errorf("API Key 映射失败")
	}
	if KiroCredentialFromAccount(&Account{Provider: "Enterprise"}).ProviderSpecificData.AuthMethod != "idc" {
		t.Errorf("Enterprise 映射失败")
	}
	if KiroCredentialFromAccount(nil) != nil {
		t.Errorf("nil 账号应返回 nil")
	}
}

func TestKiroTranslateChatRequest(t *testing.T) {
	ClearKiroSessionReplayStore()
	req := &ChatRequest{Model: "claude-sonnet-4.5", Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}}
	cred := &KiroCredential{
		ConnectionId: "c1",
		ProviderSpecificData: KiroProviderSpecificData{
			AuthMethod: "google",
			Region:     "us-east-1",
		},
	}
	tr, err := KiroTranslateChatRequest(req, "claude-sonnet-4.5", cred, map[string]string{"x-session-id": "s1"})
	if err != nil {
		t.Fatalf("KiroTranslateChatRequest: %v", err)
	}
	if tr.UpstreamModel != "claude-sonnet-4.5" {
		t.Errorf("UpstreamModel=%q", tr.UpstreamModel)
	}
	// google → 默认 social ARN
	if tr.Payload.ProfileArn != KiroDefaultProfileARNs["social"] {
		t.Errorf("social ARN=%q", tr.Payload.ProfileArn)
	}
	if tr.Payload.ConversationState.ConversationID != "s1" {
		t.Errorf("conversationId=%q", tr.Payload.ConversationState.ConversationID)
	}
	if tr.Payload.InferenceConfig == nil || tr.Payload.InferenceConfig.MaxTokens != 32000 {
		t.Errorf("Bug① maxTokens=%+v", tr.Payload.InferenceConfig)
	}
	if _, err := KiroTranslateChatRequest(req, "m", nil, nil); err == nil {
		t.Errorf("nil 凭证应报错")
	}
}

func TestKiroApplyGatewayError(t *testing.T) {
	now := time.Now()
	acc := &Account{modelLocks: map[string]int64{}}
	// 429 → 退避级别 1 + 模型锁
	KiroApplyGatewayError(acc, "claude-sonnet-4.5", 429, "rate limit exceeded", now)
	if acc.backoffLevel != 1 || acc.lastErrorCode != 429 {
		t.Errorf("429 冷却态=%+v", acc)
	}
	if acc.modelLocks["claude-sonnet-4.5"] == 0 {
		t.Errorf("模型锁未设置")
	}
	// 再 429 → 级别 2
	KiroApplyGatewayError(acc, "claude-sonnet-4.5", 429, "too many requests", now)
	if acc.backoffLevel != 2 {
		t.Errorf("二次 429 级别=%d", acc.backoffLevel)
	}
	// 401 固定冷却：级别不递增
	acc2 := &Account{backoffLevel: 5, modelLocks: map[string]int64{}}
	KiroApplyGatewayError(acc2, "m", 401, "unauthorized", now)
	if acc2.backoffLevel != 5 {
		t.Errorf("401 不应递增级别: %d", acc2.backoffLevel)
	}
	// 成功 → 清锁 + 归零
	KiroApplyGatewaySuccess(acc, "claude-sonnet-4.5")
	if acc.backoffLevel != 0 {
		t.Errorf("成功应归零级别")
	}
	if _, ok := acc.modelLocks["claude-sonnet-4.5"]; ok {
		t.Errorf("成功应清模型锁")
	}
	// 文本规则优先：403 + quota exceeded → backoff
	acc3 := &Account{modelLocks: map[string]int64{}}
	KiroApplyGatewayError(acc3, "m", 403, "quota exceeded for this model", now)
	if acc3.backoffLevel != 1 {
		t.Errorf("quota exceeded 文本应走退避: %d", acc3.backoffLevel)
	}
}

func TestKiroEnsureAccessToken(t *testing.T) {
	// 未临期（未知过期时间）→ 不刷新
	cred := &KiroCredential{AccessToken: "at", RefreshToken: "", ProviderSpecificData: KiroProviderSpecificData{AuthMethod: "api_key"}}
	if err := KiroEnsureAccessToken(cred, ""); err != nil {
		t.Errorf("api_key 未临期不应刷新: %v", err)
	}
	// 过期 + 无 refreshToken → 刷新失败报错
	cred2 := &KiroCredential{AccessToken: "at", ExpiresAtMs: time.Now().UnixMilli() - 1000}
	if err := KiroEnsureAccessToken(cred2, ""); err == nil {
		t.Errorf("过期应触发刷新并失败")
	}
	// 未临期（过期时间充足）→ 不刷新
	cred3 := &KiroCredential{AccessToken: "at", ExpiresAtMs: time.Now().UnixMilli() + 3600*1000}
	if err := KiroEnsureAccessToken(cred3, ""); err != nil {
		t.Errorf("充足过期时间不应刷新: %v", err)
	}
	// nil
	if err := KiroEnsureAccessToken(nil, ""); err == nil {
		t.Errorf("nil 应报错")
	}
}
