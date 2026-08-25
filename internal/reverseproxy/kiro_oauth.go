package reverseproxy

// 本文件 1:1 迁移自 9router（阶段 4：OAuth 登录流程、会话初始化）：
//   - open-sse/providers/registry/kiro.js（oauth 配置块）
//   - src/lib/oauth/services/kiro.js（KiroService 全量；listAvailableProfiles 已于
//     阶段 3 落地，此处复用）
//   - src/lib/oauth/utils/pkce.js（PKCE 生成）
//   - src/app/api/oauth/kiro/ 下 6 条路由的业务逻辑（api-key / import /
//     import-cli-proxy / social-authorize / social-exchange / auto-import）
//
// 按既定决策：OAuth 双轨仅保留 KiroService 实现（providers.js 冗余分支不迁）；
// ProfileArn 解析统一 KiroService.listAvailableProfiles（阶段 3）。
// 路由原为 Next.js + DB 持久化；本文件实现为纯函数返回 KiroConnection，
// 持久化/连接 ID 在阶段 7 接入 Provider 抽象层时落地。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ===== KIRO_CONFIG（registry/kiro.js oauth 块） =====

const (
	kiroOIDCEndpoint       = "https://oidc.us-east-1.amazonaws.com"
	kiroRegisterClientURL  = "https://oidc.us-east-1.amazonaws.com/client/register"
	kiroDeviceAuthURL      = "https://oidc.us-east-1.amazonaws.com/device_authorization"
	kiroOIDCTokenURL       = "https://oidc.us-east-1.amazonaws.com/token"
	kiroStartURL           = "https://view.awsapps.com/start"
	kiroClientName         = "kiro-oauth-client"
	kiroClientType         = "public"
	kiroIssuerURL          = "https://identitycenter.amazonaws.com/ssoins-722374e8c3c8e6c6"
	kiroSocialAuthEndpoint = "https://prod.us-east-1.auth.desktop.kiro.dev"
	kiroSocialLoginURL     = "https://prod.us-east-1.auth.desktop.kiro.dev/login"
	kiroSocialTokenURL     = "https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token"
	kiroSocialRefreshURL   = "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"
	// kiroSocialRedirectURI AWS Cognito 白名单自定义协议（非 localhost）。
	kiroSocialRedirectURI = "kiro://kiro.kiroAgent/authenticate-success"
)

var (
	kiroScopes      = []string{"codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations"}
	kiroGrantTypes  = []string{"urn:ietf:params:oauth:grant-type:device_code", "refresh_token"}
	kiroAuthMethods = []string{"builder-id", "idc", "google", "github", "import"}
)

// ===== PKCE（pkce.js） =====

// KiroPKCE PKCE 三元组（generatePKCE 返回值）。
type KiroPKCE struct {
	CodeVerifier  string
	CodeChallenge string
	State         string
}

func randomBase64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GeneratePKCE 生成完整 PKCE 三元组（generatePKCE；默认 32 字节，
// codeChallenge 为 S256）。
func GeneratePKCE() (KiroPKCE, error) {
	codeVerifier, err := randomBase64URL(32)
	if err != nil {
		return KiroPKCE{}, err
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state, err := randomBase64URL(32)
	if err != nil {
		return KiroPKCE{}, err
	}
	return KiroPKCE{CodeVerifier: codeVerifier, CodeChallenge: codeChallenge, State: state}, nil
}

// ===== KiroService（services/kiro.js） =====

// KiroRegisteredClient 客户端注册结果（registerClient）。
type KiroRegisteredClient struct {
	ClientID              string
	ClientSecret          string
	ClientSecretExpiresAt interface{} // 透传上游原始值（可为 null）
}

// RegisterKiroClient 向 AWS SSO 注册 OIDC 客户端（registerClient），
// 返回设备码流程所需的 clientId/clientSecret。
func RegisterKiroClient(region string) (*KiroRegisteredClient, error) {
	if err := AssertValidAwsRegion(region); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/client/register", region)

	body, _ := json.Marshal(map[string]interface{}{
		"clientName": kiroClientName,
		"clientType": kiroClientType,
		"scopes":     kiroScopes,
		"grantTypes": kiroGrantTypes,
		"issuerUrl":  kiroIssuerURL,
	})
	status, respBody, err := kiroRefreshPOST(endpoint, map[string]string{
		"Content-Type": "application/json",
	}, body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Failed to register client: %s", truncate(string(respBody), 500))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, err
	}
	return &KiroRegisteredClient{
		ClientID:              stringOf(data["clientId"]),
		ClientSecret:          stringOf(data["clientSecret"]),
		ClientSecretExpiresAt: data["clientSecretExpiresAt"],
	}, nil
}

// KiroDeviceAuthData 设备授权结果（startDeviceAuthorization）。
type KiroDeviceAuthData struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

// StartKiroDeviceAuthorization 发起 AWS Builder ID / IDC 设备授权
// （startDeviceAuthorization）。
func StartKiroDeviceAuthorization(clientId, clientSecret, startUrl, region string) (*KiroDeviceAuthData, error) {
	if err := AssertValidAwsRegion(region); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/device_authorization", region)

	body, _ := json.Marshal(map[string]string{
		"clientId":     clientId,
		"clientSecret": clientSecret,
		"startUrl":     startUrl,
	})
	status, respBody, err := kiroRefreshPOST(endpoint, map[string]string{
		"Content-Type": "application/json",
	}, body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Failed to start device authorization: %s", truncate(string(respBody), 500))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, err
	}
	interval := 5 // data.interval || 5
	if v, ok := data["interval"].(float64); ok && v > 0 {
		interval = int(v)
	}
	return &KiroDeviceAuthData{
		DeviceCode:              stringOf(data["deviceCode"]),
		UserCode:                stringOf(data["userCode"]),
		VerificationURI:         stringOf(data["verificationUri"]),
		VerificationURIComplete: stringOf(data["verificationUriComplete"]),
		ExpiresIn:               expiresInOf(data["expiresIn"]),
		Interval:                interval,
	}, nil
}

// KiroDeviceTokens 设备码轮询成功时的令牌。
type KiroDeviceTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	TokenType    string
}

// KiroDeviceTokenResult 设备码轮询结果（pollDeviceToken）。
type KiroDeviceTokenResult struct {
	Success          bool
	Error            string
	ErrorDescription string
	Pending          bool
	Tokens           *KiroDeviceTokens
}

// PollKiroDeviceToken 用设备码轮询令牌（pollDeviceToken）。
// authorization_pending / slow_down 视为 pending，其余 error 立即失败。
func PollKiroDeviceToken(clientId, clientSecret, deviceCode, region string) (*KiroDeviceTokenResult, error) {
	if err := AssertValidAwsRegion(region); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)

	body, _ := json.Marshal(map[string]string{
		"clientId":     clientId,
		"clientSecret": clientSecret,
		"deviceCode":   deviceCode,
		"grantType":    "urn:ietf:params:oauth:grant-type:device_code",
	})
	status, respBody, err := kiroRefreshPOST(endpoint, map[string]string{
		"Content-Type": "application/json",
	}, body)
	if err != nil {
		return nil, err
	}
	var data map[string]interface{}
	if json.Unmarshal(respBody, &data) != nil {
		// JS 原版此处对畸形响应会抛错；Go 侧视为失败结果
		return &KiroDeviceTokenResult{Success: false}, nil
	}

	if status != 200 || data["error"] != nil {
		errorCode := stringOf(data["error"])
		return &KiroDeviceTokenResult{
			Success:          false,
			Error:            errorCode,
			ErrorDescription: stringOf(data["error_description"]),
			Pending:          errorCode == "authorization_pending" || errorCode == "slow_down",
		}, nil
	}

	return &KiroDeviceTokenResult{
		Success: true,
		Tokens: &KiroDeviceTokens{
			AccessToken:  stringOf(data["accessToken"]),
			RefreshToken: stringOf(data["refreshToken"]),
			ExpiresIn:    expiresInOf(data["expiresIn"]),
			TokenType:    stringOf(data["tokenType"]),
		},
	}, nil
}

// BuildKiroSocialLoginURL 拼接 Google/GitHub 社交登录 URL
// （buildSocialLoginUrl；provider 为 "google" 时 idp=Google，否则 Github）。
func BuildKiroSocialLoginURL(provider, codeChallenge, state string) string {
	idp := "Github"
	if provider == "google" {
		idp = "Google"
	}
	return fmt.Sprintf("%s/login?idp=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=%s&prompt=select_account",
		kiroSocialAuthEndpoint, idp, url.QueryEscape(kiroSocialRedirectURI), codeChallenge, state)
}

// KiroSocialTokens 社交授权码交换结果（exchangeSocialCode）。
type KiroSocialTokens struct {
	AccessToken  string
	RefreshToken string
	ProfileArn   string
	ExpiresIn    int
}

// ExchangeKiroSocialCode 用授权码交换令牌（exchangeSocialCode）。
// redirect_uri 必须与 buildSocialLoginUrl 中一致（硬编码 kiro:// 协议）。
func ExchangeKiroSocialCode(code, codeVerifier string) (*KiroSocialTokens, error) {
	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": codeVerifier,
		"redirect_uri":  kiroSocialRedirectURI,
	})
	status, respBody, err := kiroRefreshPOST(kiroSocialTokenURL, map[string]string{
		"Content-Type": "application/json",
	}, body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Token exchange failed: %s", truncate(string(respBody), 500))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, err
	}
	expiresIn := expiresInOf(data["expiresIn"])
	if expiresIn == 0 {
		expiresIn = 3600 // data.expiresIn || 3600
	}
	return &KiroSocialTokens{
		AccessToken:  stringOf(data["accessToken"]),
		RefreshToken: stringOf(data["refreshToken"]),
		ProfileArn:   stringOf(data["profileArn"]),
		ExpiresIn:    expiresIn,
	}, nil
}

// KiroServiceTokenData KiroService.refreshToken / validateImportToken 返回值。
type KiroServiceTokenData struct {
	AccessToken  string
	RefreshToken string
	ProfileArn   string
	ExpiresIn    int
	AuthMethod   string // 仅 validateImportToken 携带
}

// KiroServiceRefreshToken 用 refresh token 刷新（KiroService.refreshToken）。
// 双分支：clientId+clientSecret → AWS SSO OIDC 区域端点（校验区域）；
// 否则 → 社交刷新端点。失败返回 error（与 refreshKiroToken 的 null 语义不同）。
func KiroServiceRefreshToken(refreshToken string, psd KiroProviderSpecificData) (*KiroServiceTokenData, error) {
	// 注意：JS 原版声明 const authMethod = providerSpecificData?.authMethod 但未使用
	// （分支由 clientId/clientSecret 决定），Go 侧省略。
	clientId := psd.ClientId
	clientSecret := psd.ClientSecret
	region := psd.Region

	// AWS SSO OIDC 刷新（Builder ID 或 IDC）
	if clientId != "" && clientSecret != "" {
		safeRegion := region
		if safeRegion == "" {
			safeRegion = "us-east-1"
		}
		if err := AssertValidAwsRegion(safeRegion); err != nil {
			return nil, err
		}
		endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", safeRegion)

		reqBody, _ := json.Marshal(map[string]string{
			"clientId":     clientId,
			"clientSecret": clientSecret,
			"refreshToken": refreshToken,
			"grantType":    "refresh_token",
		})
		status, respBody, err := kiroRefreshPOST(endpoint, map[string]string{
			"Content-Type": "application/json",
		}, reqBody)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, fmt.Errorf("Token refresh failed: %s", truncate(string(respBody), 500))
		}
		var data map[string]interface{}
		if err := json.Unmarshal(respBody, &data); err != nil {
			return nil, err
		}
		rt := stringOf(data["refreshToken"])
		if rt == "" {
			rt = refreshToken
		}
		return &KiroServiceTokenData{
			AccessToken:  stringOf(data["accessToken"]),
			RefreshToken: rt,
			ProfileArn:   stringOf(data["profileArn"]),
			ExpiresIn:    expiresInOf(data["expiresIn"]),
		}, nil
	}

	// 社交刷新（Google/GitHub）
	reqBody, _ := json.Marshal(map[string]string{"refreshToken": refreshToken})
	status, respBody, err := kiroRefreshPOST(kiroSocialRefreshURL, map[string]string{
		"Content-Type": "application/json",
	}, reqBody)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Token refresh failed: %s", truncate(string(respBody), 500))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, err
	}
	rt := stringOf(data["refreshToken"])
	if rt == "" {
		rt = refreshToken
	}
	expiresIn := expiresInOf(data["expiresIn"])
	if expiresIn == 0 {
		expiresIn = 3600 // 社交分支 data.expiresIn || 3600
	}
	return &KiroServiceTokenData{
		AccessToken:  stringOf(data["accessToken"]),
		RefreshToken: rt,
		ProfileArn:   stringOf(data["profileArn"]),
		ExpiresIn:    expiresIn,
	}, nil
}

// ValidateKiroImportToken 校验并导入 refresh token（validateImportToken）：
// 前缀 aorAAAAAG 校验，随后刷新验证。
func ValidateKiroImportToken(refreshToken string) (*KiroServiceTokenData, error) {
	if !strings.HasPrefix(refreshToken, "aorAAAAAG") {
		return nil, errors.New("Invalid token format. Token should start with aorAAAAAG...")
	}
	result, err := KiroServiceRefreshToken(refreshToken, KiroProviderSpecificData{})
	if err != nil {
		return nil, fmt.Errorf("Token validation failed: %v", err)
	}
	rt := result.RefreshToken
	if rt == "" {
		rt = refreshToken
	}
	return &KiroServiceTokenData{
		AccessToken:  result.AccessToken,
		RefreshToken: rt,
		ProfileArn:   result.ProfileArn,
		ExpiresIn:    result.ExpiresIn,
		AuthMethod:   "imported",
	}, nil
}

// KiroAPIKeyCredential API key 校验结果（validateApiKey）。
type KiroAPIKeyCredential struct {
	AccessToken  string
	RefreshToken string
	ProfileArn   string
	Region       string
	AuthMethod   string
}

// ValidateKiroAPIKey 校验 API key（长生命周期 bearer，无 refresh）：
// 通过 ListAvailableProfiles 验证，返回 api_key 模式凭证对象。
func ValidateKiroAPIKey(apiKey, region string) (*KiroAPIKeyCredential, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("API key is required")
	}
	trimmed := strings.TrimSpace(apiKey)

	profileArn, err := ListAvailableProfiles(trimmed, region)
	if err != nil {
		return nil, fmt.Errorf("API key validation failed: %v", err)
	}

	return &KiroAPIKeyCredential{
		AccessToken:  trimmed,
		RefreshToken: "",
		ProfileArn:   profileArn,
		Region:       region,
		AuthMethod:   "api_key",
	}, nil
}

// KiroModelInfo CodeWhisperer 可用模型条目（listAvailableModels 返回项）。
type KiroModelInfo struct {
	ID             string
	Name           string
	Description    string
	RateMultiplier interface{} // 透传原始值
	RateUnit       interface{}
	MaxInputTokens int
}

// ListKiroAvailableModels 列出 CodeWhisperer 可用模型（listAvailableModels）。
func ListKiroAvailableModels(accessToken, profileArn string) ([]KiroModelInfo, error) {
	endpoint := "https://codewhisperer.us-east-1.amazonaws.com"

	payload := map[string]interface{}{"origin": "AI_EDITOR"}
	if arn := EffectiveProfileArn(profileArn); arn != "" {
		payload["profileArn"] = arn
	}
	body, _ := json.Marshal(payload)
	status, respBody, err := kiroRefreshPOST(endpoint, map[string]string{
		"Content-Type":  "application/x-amz-json-1.0",
		"x-amz-target":  "AmazonCodeWhispererService.ListAvailableModels",
		"Authorization": "Bearer " + accessToken,
		"Accept":        "application/json",
	}, body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Failed to list models: %s", truncate(string(respBody), 500))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, err
	}
	var models []KiroModelInfo
	if raw, ok := data["models"].([]interface{}); ok {
		for _, item := range raw {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			var maxInput int
			if tl, ok := m["tokenLimits"].(map[string]interface{}); ok {
				maxInput = expiresInOf(tl["maxInputTokens"])
			}
			name := stringOf(m["modelName"])
			if name == "" {
				name = stringOf(m["modelId"])
			}
			models = append(models, KiroModelInfo{
				ID:             stringOf(m["modelId"]),
				Name:           name,
				Description:    stringOf(m["description"]),
				RateMultiplier: m["rateMultiplier"],
				RateUnit:       m["rateUnit"],
				MaxInputTokens: maxInput,
			})
		}
	}
	return models, nil
}

// ExtractKiroEmailFromJWT 从 access token JWT 提取邮箱（extractEmailFromJWT：
// email || preferred_username || sub）。
func ExtractKiroEmailFromJWT(accessToken string) string {
	payload := DecodeJwtPayload(accessToken)
	if payload == nil {
		return ""
	}
	return firstOf(payload, "email", "preferred_username", "sub")
}

// ===== 登录流程后端（6 条路由业务逻辑） =====

// KiroConnection 对应 createProviderConnection 的入参（provider=kiro）。
// id 与持久化由阶段 7 的 Provider 抽象层决定。
type KiroConnection struct {
	Provider             string
	AuthType             string // api_key / oauth
	AccessToken          string
	RefreshToken         string
	ExpiresAt            time.Time
	Email                string
	ProviderSpecificData KiroProviderSpecificData
	TestStatus           string
}

// KiroImportAPIKey POST /api/oauth/kiro/api-key：校验 API key 并解析 profileArn，
// 长期有效（365 天）。失败不反射上游错误（SSRF hardening）。
func KiroImportAPIKey(apiKey, region string) (*KiroConnection, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("API key is required")
	}
	if region == "" {
		region = "us-east-1"
	}
	credential, err := ValidateKiroAPIKey(apiKey, region)
	if err != nil {
		return nil, errors.New("API key validation failed")
	}

	email := ExtractKiroEmailFromJWT(credential.AccessToken)
	return &KiroConnection{
		Provider:     "kiro",
		AuthType:     "api_key",
		AccessToken:  credential.AccessToken,
		RefreshToken: "",
		ExpiresAt:    time.Now().Add(365 * 24 * time.Hour),
		Email:        email,
		ProviderSpecificData: KiroProviderSpecificData{
			ProfileArn: credential.ProfileArn,
			Region:     credential.Region,
			AuthMethod: "api_key",
			Provider:   "API Key",
		},
		TestStatus: "active",
	}, nil
}

// KiroImportToken POST /api/oauth/kiro/import：导入并验证 Kiro IDE 的
// refresh token。IDC（组织）token 接受 clientId/clientSecret/region 走区域
// OIDC 端点刷新。
func KiroImportToken(refreshToken, clientId, clientSecret, region, authMethod, profileArn string) (*KiroConnection, error) {
	if refreshToken == "" {
		return nil, errors.New("Refresh token is required")
	}

	isIdc := clientId != "" && clientSecret != ""

	// IDC token 走区域 OIDC 端点；社交/builder-id 走标准社交刷新端点
	var psd KiroProviderSpecificData
	if isIdc {
		r := region
		if r == "" {
			r = "us-east-1"
		}
		psd = KiroProviderSpecificData{
			ClientId:     clientId,
			ClientSecret: clientSecret,
			Region:       r,
			AuthMethod:   "idc",
		}
	}

	tokenData, err := KiroServiceRefreshToken(strings.TrimSpace(refreshToken), psd)
	if err != nil {
		return nil, err
	}

	email := ExtractKiroEmailFromJWT(tokenData.AccessToken)
	resolvedAuthMethod := "imported"
	providerLabel := "Imported"
	if isIdc {
		resolvedAuthMethod = "idc"
		providerLabel = "Enterprise"
	}
	resolvedProfileArn := profileArn
	if resolvedProfileArn == "" {
		resolvedProfileArn = tokenData.ProfileArn
	}
	expiresIn := tokenData.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 3600
	}

	psdOut := KiroProviderSpecificData{
		ProfileArn: resolvedProfileArn,
		AuthMethod: resolvedAuthMethod,
		Provider:   providerLabel,
	}
	if isIdc {
		psdOut.ClientId = clientId
		psdOut.ClientSecret = clientSecret
		psdOut.Region = region
		if psdOut.Region == "" {
			psdOut.Region = "us-east-1"
		}
	}

	rt := tokenData.RefreshToken
	if rt == "" {
		rt = strings.TrimSpace(refreshToken)
	}
	return &KiroConnection{
		Provider:             "kiro",
		AuthType:             "oauth",
		AccessToken:          tokenData.AccessToken,
		RefreshToken:         rt,
		ExpiresAt:            time.Now().Add(time.Duration(expiresIn) * time.Second),
		Email:                email,
		ProviderSpecificData: psdOut,
		TestStatus:           "active",
	}, nil
}

// KiroImportCLIProxyAuth POST /api/oauth/kiro/import-cli-proxy：导入
// Microsoft external_idp 账号的 CLIProxyAPI auth JSON。body 字段查找顺序：
// cliProxyAuth ?? auth ?? json ?? body 本身。
func KiroImportCLIProxyAuth(body map[string]interface{}) (*KiroConnection, error) {
	var rawAuth interface{}
	switch {
	case body["cliProxyAuth"] != nil:
		rawAuth = body["cliProxyAuth"]
	case body["auth"] != nil:
		rawAuth = body["auth"]
	case body["json"] != nil:
		rawAuth = body["json"]
	default:
		rawAuth = body
	}

	tokenData, err := NormalizeKiroExternalIdpAuth(rawAuth)
	if err != nil {
		return nil, err
	}

	return &KiroConnection{
		Provider:             "kiro",
		AuthType:             "oauth",
		AccessToken:          tokenData.AccessToken,
		RefreshToken:         tokenData.RefreshToken,
		ExpiresAt:            tokenData.ExpiresAt,
		Email:                tokenData.Email,
		ProviderSpecificData: tokenData.ProviderSpecificData,
		TestStatus:           "active",
	}, nil
}

// KiroSocialAuthParams GET /api/oauth/kiro/social-authorize 返回值。
type KiroSocialAuthParams struct {
	AuthURL       string
	State         string
	CodeVerifier  string
	CodeChallenge string
	Provider      string
}

// KiroSocialAuthorize GET /api/oauth/kiro/social-authorize：生成 Google/GitHub
// 社交登录 URL（手动回调流程，kiro:// 自定义协议）。
func KiroSocialAuthorize(provider string) (*KiroSocialAuthParams, error) {
	if provider != "google" && provider != "github" {
		return nil, errors.New("Invalid provider. Use 'google' or 'github'")
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	authURL := BuildKiroSocialLoginURL(provider, pkce.CodeChallenge, pkce.State)
	return &KiroSocialAuthParams{
		AuthURL:       authURL,
		State:         pkce.State,
		CodeVerifier:  pkce.CodeVerifier,
		CodeChallenge: pkce.CodeChallenge,
		Provider:      provider,
	}, nil
}

// KiroSocialExchange POST /api/oauth/kiro/social-exchange：交换授权码为令牌
// （回调 URL 形如 kiro://kiro.kiroAgent/authenticate-success?code=XXX&state=YYY）。
func KiroSocialExchange(code, codeVerifier, provider string) (*KiroConnection, error) {
	if code == "" || codeVerifier == "" {
		return nil, errors.New("Missing required fields")
	}
	if provider != "google" && provider != "github" {
		return nil, errors.New("Invalid provider")
	}

	tokenData, err := ExchangeKiroSocialCode(code, codeVerifier)
	if err != nil {
		return nil, err
	}

	email := ExtractKiroEmailFromJWT(tokenData.AccessToken)
	providerLabel := strings.ToUpper(provider[:1]) + provider[1:] // Google / Github

	return &KiroConnection{
		Provider:     "kiro",
		AuthType:     "oauth",
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenData.ExpiresIn) * time.Second),
		Email:        email,
		ProviderSpecificData: KiroProviderSpecificData{
			ProfileArn: tokenData.ProfileArn,
			AuthMethod: provider, // "google" / "github"
			Provider:   providerLabel,
		},
		TestStatus: "active",
	}, nil
}

// KiroAutoImportResult GET /api/oauth/kiro/auto-import 返回值。
type KiroAutoImportResult struct {
	Found        bool
	Error        string
	RefreshToken string
	Source       string
	ClientID     string
	ClientSecret string
	Region       string
	AuthMethod   string
	ProfileArn   string
}

var kiroArnRegionPattern = regexp.MustCompile(`arn:aws:codewhisperer:[^:]+:`)

// KiroAutoImport GET /api/oauth/kiro/auto-import：从本地 AWS SSO 缓存 +
// Kiro IDE profile.json 自动提取刷新令牌与客户端凭证。所有失败路径均返回
// {found:false, error}（JS 语义，不产生 error 返回）。
func KiroAutoImport() *KiroAutoImportResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return &KiroAutoImportResult{Found: false, Error: err.Error()}
	}
	cachePath := filepath.Join(home, ".aws", "sso", "cache")

	files, err := os.ReadDir(cachePath)
	if err != nil {
		return &KiroAutoImportResult{
			Found: false,
			Error: "AWS SSO cache not found. Please login to Kiro IDE first.",
		}
	}

	// 1. 优先 kiro-auth-token.json
	refreshToken := ""
	foundFile := ""
	var tokenData map[string]interface{}
	if fileExists(cachePath, "kiro-auth-token.json") {
		if data, ok := readJSONFile(filepath.Join(cachePath, "kiro-auth-token.json")); ok {
			if rt := stringOf(data["refreshToken"]); strings.HasPrefix(rt, "aorAAAAAG") {
				refreshToken = rt
				foundFile = "kiro-auth-token.json"
				tokenData = data
			}
		}
	}

	// 2. 未找到则遍历全部 .json
	if refreshToken == "" {
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			if data, ok := readJSONFile(filepath.Join(cachePath, f.Name())); ok {
				if rt := stringOf(data["refreshToken"]); strings.HasPrefix(rt, "aorAAAAAG") {
					refreshToken = rt
					foundFile = f.Name()
					tokenData = data
					break
				}
			}
		}
	}

	if refreshToken == "" {
		return &KiroAutoImportResult{
			Found: false,
			Error: "Kiro token not found in AWS SSO cache. Please login to Kiro IDE first.",
		}
	}

	// 3. IDC/组织 token：由 clientIdHash 关联的客户端注册文件解析 clientId/clientSecret
	clientId := ""
	clientSecret := ""
	region := ""
	authMethod := ""
	if tokenData != nil {
		region = stringOf(tokenData["region"])
		authMethod = stringOf(tokenData["authMethod"])
		if ch := stringOf(tokenData["clientIdHash"]); ch != "" {
			if clientData, ok := readJSONFile(filepath.Join(cachePath, ch+".json")); ok {
				if cid, csec := stringOf(clientData["clientId"]), stringOf(clientData["clientSecret"]); cid != "" && csec != "" {
					clientId = cid
					clientSecret = csec
				}
			}
		}
	}

	// 4. 读 Kiro IDE profile.json。
	// 注意：runtime 网关强制 ARN 中的区域为 us-east-1，与 IDC 区域无关，
	// 因此把 ARN 区域归一化为 us-east-1。
	profileArn := ""
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		appdata = filepath.Join(home, "AppData", "Roaming")
	}
	kiroProfilePaths := []string{
		filepath.Join(appdata, "Kiro", "User", "globalStorage", "kiro.kiroagent", "profile.json"),
		filepath.Join(home, ".config", "Kiro", "User", "globalStorage", "kiro.kiroagent", "profile.json"),
	}
	for _, profilePath := range kiroProfilePaths {
		if profileData, ok := readJSONFile(profilePath); ok {
			if arn := stringOf(profileData["arn"]); arn != "" {
				profileArn = kiroArnRegionPattern.ReplaceAllString(arn, "arn:aws:codewhisperer:us-east-1:")
				break
			}
		}
	}

	return &KiroAutoImportResult{
		Found:        true,
		RefreshToken: refreshToken,
		Source:       foundFile,
		ClientID:     clientId,
		ClientSecret: clientSecret,
		Region:       region,
		AuthMethod:   authMethod,
		ProfileArn:   profileArn,
	}
}

// fileExists 判断目录下文件是否存在。
func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !info.IsDir()
}

// readJSONFile 读取并解析 JSON 文件（失败返回 ok=false，对应 JS try/catch continue）。
func readJSONFile(path string) (map[string]interface{}, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var data map[string]interface{}
	if json.Unmarshal(content, &data) != nil {
		return nil, false
	}
	return data, true
}
