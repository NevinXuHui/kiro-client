package reverseproxy

// 本文件 1:1 迁移自 9router src/lib/oauth/kiroExternalIdp.js（阶段 3）。
// 处理 Kiro CLIProxyAPI 导入的 Microsoft External IDP（Entra ID / federated identity）
// 认证 JSON 的规范化解析与刷新参数构建。

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var microsoftTokenEndpointHosts = map[string]struct{}{
	"login.microsoftonline.com": {},
	"login.microsoft.com":       {},
	"login.windows.net":         {},
}

const (
	kiroExtIdpDefaultRegion    = "us-east-1"
	kiroExtIdpDefaultExpiresIn = 3600
)

// normalizeString 对应 JS normalizeString：非字符串返回空串，字符串 trim。
func normalizeString(s string) string {
	return strings.TrimSpace(s)
}

// ValidateMicrosoftTokenEndpoint 校验 token_endpoint 必须为 https 且 host 属于
// Microsoft 登录域名白名单（防 SSRF）。返回标准化 URL 字符串。
func ValidateMicrosoftTokenEndpoint(rawEndpoint string) (string, error) {
	tokenEndpoint := normalizeString(rawEndpoint)
	if tokenEndpoint == "" {
		return "", errors.New("token_endpoint is required")
	}

	parsed, err := url.Parse(tokenEndpoint)
	if err != nil {
		return "", errors.New("token_endpoint must be a valid URL")
	}

	if parsed.Scheme != "https" {
		return "", errors.New("token_endpoint must use https")
	}

	host := strings.ToLower(parsed.Hostname())
	if _, ok := microsoftTokenEndpointHosts[host]; !ok {
		return "", errors.New("token_endpoint must be a Microsoft login endpoint")
	}

	return parsed.String(), nil
}

// NormalizeScope 对应 JS normalizeScope：数组 join 空格（逐项 trim 过滤空串），
// 否则字符串 trim。
func NormalizeScope(scopes interface{}) string {
	switch v := scopes.(type) {
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if t := normalizeString(s); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, " ")
	case []string:
		parts := make([]string, 0, len(v))
		for _, s := range v {
			if t := normalizeString(s); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, " ")
	default:
		if s, ok := scopes.(string); ok {
			return normalizeString(s)
		}
		return ""
	}
}

// DecodeJwtPayload Base64 URL-safe 解码 JWT payload（失败返回 nil，对应 JS try/catch）。
func DecodeJwtPayload(jwt string) map[string]interface{} {
	if jwt == "" {
		return nil
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil
	}
	b64 := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
	if pad := (4 - len(b64)%4) % 4; pad > 0 {
		b64 += strings.Repeat("=", pad)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	var payload map[string]interface{}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	return payload
}

// resolveExpiresAt 对应 JS resolveExpiresAt，解析顺序：
// 1. 显式时间 expired/expires_at/expiresAt（ISO 或毫秒时间戳）
// 2. expires_in/expiresIn（>0 时 now+ei*1000）
// 3. JWT payload.exp（秒）
// 4. 兜底 now+3600s
func resolveExpiresAt(input map[string]interface{}) time.Time {
	// 1. 显式时间：expired || expires_at || expiresAt（取首个真值）
	for _, key := range []string{"expired", "expires_at", "expiresAt"} {
		if v, ok := input[key]; ok && v != nil {
			if t, err := parseExplicitTime(v); err == nil {
				return t
			}
		}
	}

	// 2. expires_in || expiresIn || 0
	var expiresIn float64
	hasExpiresIn := false
	for _, key := range []string{"expires_in", "expiresIn"} {
		if v, ok := input[key]; ok && v != nil {
			switch n := v.(type) {
			case float64:
				expiresIn, hasExpiresIn = n, true
			case int:
				expiresIn, hasExpiresIn = float64(n), true
			case string:
				if f, err := parseFloat64(n); err == nil {
					expiresIn, hasExpiresIn = f, true
				}
			}
			if hasExpiresIn {
				break
			}
		}
	}
	if hasExpiresIn && expiresIn > 0 {
		return time.Now().Add(time.Duration(expiresIn) * time.Second)
	}

	// 3. JWT exp（秒）
	accessToken := ""
	for _, key := range []string{"access_token", "accessToken"} {
		if s, ok := input[key].(string); ok && s != "" {
			accessToken = s
			break
		}
	}
	if payload := DecodeJwtPayload(accessToken); payload != nil {
		if exp, ok := payload["exp"].(float64); ok && exp > 0 {
			return time.Unix(int64(exp), 0)
		}
	}

	// 4. 兜底
	return time.Now().Add(kiroExtIdpDefaultExpiresIn * time.Second)
}

// parseExplicitTime 解析 ISO 字符串或毫秒时间戳（对应 JS new Date(v).getTime()）。
func parseExplicitTime(v interface{}) (time.Time, error) {
	switch n := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, n); err == nil {
			return t, nil
		}
		return time.Time{}, errors.New("invalid time string")
	case float64:
		return msToTime(n), nil
	case int:
		return msToTime(float64(n)), nil
	default:
		return time.Time{}, errors.New("invalid time type")
	}
}

func msToTime(ms float64) time.Time {
	return time.UnixMilli(int64(ms))
}

func parseFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// KiroExternalIdpAuth CLIProxyAPI 外部 IdP 认证归一化结果。
type KiroExternalIdpAuth struct {
	AccessToken          string
	RefreshToken         string
	ExpiresAt            time.Time
	Email                string
	ProviderSpecificData KiroProviderSpecificData
}

// NormalizeKiroExternalIdpAuth 归一化 CLIProxyAPI 导入的 auth JSON（字符串或对象，
// 字段名同时兼容 camelCase 与 snake_case）。仅接受 auth_method=external_idp。
func NormalizeKiroExternalIdpAuth(rawAuth interface{}) (*KiroExternalIdpAuth, error) {
	input := rawAuth
	if s, ok := input.(string); ok {
		var parsed interface{}
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, errors.New("CLIProxyAPI auth JSON is invalid")
		}
		input = parsed
	}

	m, ok := input.(map[string]interface{})
	if !ok {
		return nil, errors.New("CLIProxyAPI auth JSON is required")
	}

	// 字段读取：camelCase 与 snake_case 双名兼容，取首个非空
	first := func(keys ...string) string {
		for _, k := range keys {
			if v, exists := m[k]; exists && v != nil {
				if s, isStr := v.(string); isStr {
					return s
				}
			}
		}
		return ""
	}

	authMethod := normalizeString(first("auth_method", "authMethod"))
	if authMethod != "" && authMethod != "external_idp" {
		return nil, errors.New("Only external_idp Kiro auth is supported by this importer")
	}

	accessToken := normalizeString(first("access_token", "accessToken"))
	refreshToken := normalizeString(first("refresh_token", "refreshToken"))
	clientId := normalizeString(first("client_id", "clientId"))
	tokenEndpoint, err := ValidateMicrosoftTokenEndpoint(first("token_endpoint", "tokenEndpoint"))
	if err != nil {
		return nil, err
	}
	profileArn := normalizeString(first("profile_arn", "profileArn"))
	region := normalizeString(first("region"))
	if region == "" {
		region = kiroExtIdpDefaultRegion
	}
	scope := NormalizeScope(pick(m, "scopes", "scope"))

	if accessToken == "" {
		return nil, errors.New("access_token is required")
	}
	if refreshToken == "" {
		return nil, errors.New("refresh_token is required")
	}
	if clientId == "" {
		return nil, errors.New("client_id is required")
	}
	if scope == "" {
		return nil, errors.New("scopes is required")
	}
	if profileArn == "" {
		return nil, errors.New("profile_arn is required")
	}

	payload := DecodeJwtPayload(accessToken)
	email := first("email")
	if email == "" && payload != nil {
		email = firstOf(payload, "email", "preferred_username", "upn", "sub")
	}

	return &KiroExternalIdpAuth{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    resolveExpiresAt(m),
		Email:        email,
		ProviderSpecificData: KiroProviderSpecificData{
			ProfileArn:    profileArn,
			Region:        region,
			AuthMethod:    "external_idp",
			Provider:      "CLIProxyAPI",
			ClientId:      clientId,
			TokenEndpoint: tokenEndpoint,
			Scope:         scope,
		},
	}, nil
}

// pick 从 map 取首个存在的键（原值，不转字符串；与 first 的差异：scope 需保留数组形态）。
func pick(m map[string]interface{}, keys ...string) interface{} {
	for _, k := range keys {
		if v, exists := m[k]; exists {
			return v
		}
	}
	return nil
}

// firstOf 从 map 取首个非空字符串值。
func firstOf(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ExternalIdpRefreshRequest 外部 IdP 刷新请求参数（buildExternalIdpRefreshParams 返回值）。
type ExternalIdpRefreshRequest struct {
	TokenEndpoint        string
	Body                 url.Values
	ProviderSpecificData KiroProviderSpecificData
}

// BuildExternalIdpRefreshParams 构建 OAuth2 refresh_token 请求参数。
// 校验 clientId/tokenEndpoint/scope 不可缺。
func BuildExternalIdpRefreshParams(refreshToken string, providerSpecificData KiroProviderSpecificData) (*ExternalIdpRefreshRequest, error) {
	clientId := normalizeString(providerSpecificData.ClientId)
	tokenEndpoint, err := ValidateMicrosoftTokenEndpoint(providerSpecificData.TokenEndpoint)
	if err != nil {
		return nil, err
	}
	scope := NormalizeScope(providerSpecificData.Scope)

	if refreshToken == "" {
		return nil, errors.New("refresh token is required")
	}
	if clientId == "" {
		return nil, errors.New("clientId is required for external_idp refresh")
	}
	if scope == "" {
		return nil, errors.New("scope is required for external_idp refresh")
	}

	body := url.Values{}
	body.Set("grant_type", "refresh_token")
	body.Set("client_id", clientId)
	body.Set("refresh_token", refreshToken)
	body.Set("scope", scope)

	// 展开 providerSpecificData 后强制覆盖 authMethod/clientId/tokenEndpoint/scope
	psdOut := providerSpecificData
	psdOut.AuthMethod = "external_idp"
	psdOut.ClientId = clientId
	psdOut.TokenEndpoint = tokenEndpoint
	psdOut.Scope = scope

	return &ExternalIdpRefreshRequest{
		TokenEndpoint:        tokenEndpoint,
		Body:                 body,
		ProviderSpecificData: psdOut,
	}, nil
}
