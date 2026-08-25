package reverseproxy

// 本文件 1:1 迁移自 9router open-sse/services/kiroModels.js（阶段 6）：
// 实时模型目录抓取（CodeWhisperer ListAvailableModels）+ 9router 变体展开 +
// 按凭证 5 分钟缓存。
//
// 运行期 UA 必须与 Kiro IDE 自身一致：上游对畸形 User-Agent 返回 400
// "format of value 'os/win/10 lang/js ...' is invalid"。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

const (
	kiroRuntimeSDKVersion  = "1.0.0"
	kiroAgentOS            = "windows"
	kiroAgentOSVersion     = "10.0.26200"
	kiroNodeVersion        = "22.21.1"
	kiroVersion            = "0.10.32"
	kiroDefaultRegion      = "us-east-1"
	kiroModelsFetchTimeout = 30 * 1000
	kiroModelsCacheTTL     = 5 * 60 * 1000 // 每个凭证 5 分钟
)

// KiroCatalogModel 展开后的 9router 变体模型。
type KiroCatalogModel struct {
	ID              string
	Name            string
	Capabilities    map[string]bool // {thinking, agentic}
	ContextLength   int
	RateMultiplier  float64
	UpstreamModelID string
	Description     string
}

// KiroCatalogResult resolveKiroModels 返回值。
type KiroCatalogResult struct {
	Models    []KiroCatalogModel
	RawModels []map[string]interface{}
}

var (
	kiroCatalogCacheMu sync.Mutex
	kiroCatalogCache   = map[string]kiroCatalogEntry{}
)

type kiroCatalogEntry struct {
	ExpiresAt time.Time
	Models    []KiroCatalogModel
	RawModels []map[string]interface{}
}

// kiroStripSyntheticSuffixes 剥离 -agentic/-thinking 合成后缀（防御性展示用）。
func kiroStripSyntheticSuffixes(id string) string {
	out := id
	if strings.HasSuffix(out, "-agentic") {
		out = out[:len(out)-len("-agentic")]
	}
	if strings.HasSuffix(out, "-thinking") {
		out = out[:len(out)-len("-thinking")]
	}
	return out
}

// kiroRegionFromProfileArn 从 profileArn 提取区域
// （arn:aws:codewhisperer:us-east-1:... → us-east-1）。
func kiroRegionFromProfileArn(profileArn string) string {
	if profileArn == "" {
		return kiroDefaultRegion
	}
	parts := strings.Split(profileArn, ":")
	if len(parts) >= 4 && parts[3] != "" {
		return parts[3]
	}
	return kiroDefaultRegion
}

// BuildKiroFingerprintHeaders 构建上游校验的每账号指纹头
// （buildKiroFingerprintHeaders：以稳定标识为种子的 machineId，同账号恒同）。
func BuildKiroFingerprintHeaders(credentials *KiroCredential) map[string]string {
	seed := ""
	if credentials != nil {
		seed = credentials.ProviderSpecificData.ClientId
		if seed == "" {
			seed = credentials.RefreshToken
		}
		if seed == "" {
			seed = credentials.ProviderSpecificData.ProfileArn
		}
		if seed == "" {
			seed = credentials.AccessToken
		}
	}
	if seed == "" {
		seed = "kiro-anonymous"
	}
	sum := sha256.Sum256([]byte(seed))
	machineID := hex.EncodeToString(sum[:])

	userAgent := "aws-sdk-js/" + kiroRuntimeSDKVersion + " ua/2.1 " +
		"os/" + kiroAgentOS + "#" + kiroAgentOSVersion + " " +
		"lang/js md/nodejs#" + kiroNodeVersion + " " +
		"api/codewhispererruntime#" + kiroRuntimeSDKVersion + " m/N,E " +
		"KiroIDE-" + kiroVersion + "-" + machineID
	amzUserAgent := "aws-sdk-js/" + kiroRuntimeSDKVersion + " KiroIDE-" + kiroVersion + "-" + machineID

	return map[string]string{
		"User-Agent":                  userAgent,
		"x-amz-user-agent":            amzUserAgent,
		"x-amzn-kiro-agent-mode":      "vibe",
		"x-amzn-codewhisperer-optout": "true",
		"amz-sdk-request":             "attempt=1; max=1",
		"amz-sdk-invocation-id":       uuid.NewString(),
		"Accept":                      "application/json",
	}
}

// kiroFormatDisplayName 人类可读展示名（formatDisplayName：
// rateMultiplier ≠ 1 时附 "({rate}x credit)"）。
func kiroFormatDisplayName(modelName, modelId string, rateMultiplier interface{}) string {
	base := strings.TrimSpace(modelName)
	if base == "" {
		base = modelId
	}
	if base == "" {
		base = "Kiro"
	}
	rate, ok := asFloat(rateMultiplier)
	if !ok || math.IsNaN(rate) || math.IsInf(rate, 0) || math.Abs(rate-1.0) < 1e-9 || rate <= 0 {
		return "Kiro " + base
	}
	rateStr := fmt.Sprintf("%.1f", rate)
	return "Kiro " + base + " (" + rateStr + "x credit)"
}

// kiroBuildVariants 为单个上游模型构建 9router 合成变体集
// （buildVariants：base + -thinking 恒有；auto 模型跳过 -agentic 变体——
// Kiro 服务端选模型，分块写入提示对 coding-agent 无意义，对齐 CLIProxyAPIPlus）。
func kiroBuildVariants(upstream, displayName string) []KiroCatalogModel {
	safeUpstream := kiroStripSyntheticSuffixes(upstream)
	display := displayName
	if display == "" {
		display = "Kiro " + safeUpstream
	}
	isAuto := safeUpstream == "auto"

	variants := []KiroCatalogModel{
		{
			ID:           safeUpstream,
			Name:         display,
			Capabilities: map[string]bool{"thinking": false, "agentic": false},
		},
		{
			ID:           safeUpstream + "-thinking",
			Name:         display + " (Thinking)",
			Capabilities: map[string]bool{"thinking": true, "agentic": false},
		},
	}

	if !isAuto {
		variants = append(variants,
			KiroCatalogModel{
				ID:           safeUpstream + "-agentic",
				Name:         display + " (Agentic)",
				Capabilities: map[string]bool{"thinking": false, "agentic": true},
			},
			KiroCatalogModel{
				ID:           safeUpstream + "-thinking-agentic",
				Name:         display + " (Thinking + Agentic)",
				Capabilities: map[string]bool{"thinking": true, "agentic": true},
			},
		)
	}

	return variants
}

// kiroFetchCatalogRaw 从 Kiro 拉取原始模型目录（fetchKiroCatalogRaw）。
// 返回 API 响应的 .models 数组；网络/HTTP 错误返回 error。
func kiroFetchCatalogRaw(credentials *KiroCredential, proxy string) ([]map[string]interface{}, error) {
	profileArn := ""
	accessToken := ""
	if credentials != nil {
		profileArn = credentials.ProviderSpecificData.ProfileArn
		accessToken = credentials.AccessToken
	}
	region := kiroRegionFromProfileArn(profileArn)
	arn := ResolveUsageProfileArn(accessToken, region, proxy, profileArn)
	url := AvailableModelsURL(region, arn)

	headers := BuildKiroFingerprintHeaders(credentials)
	headers["User-Agent"] = strings.Replace(headers["User-Agent"], "KiroIDE-"+kiroVersion+"-", "KiroIDE-"+kiroRESTIDEVersion+"-", 1)
	headers["x-amz-user-agent"] = strings.Replace(headers["x-amz-user-agent"], "KiroIDE-"+kiroVersion+"-", "KiroIDE-"+kiroRESTIDEVersion+"-", 1)
	headers["Authorization"] = "Bearer " + accessToken

	client := newModelsClient(proxy)
	fetch := func(u string) ([]byte, int, error) {
		req, err := fhttp.NewRequest("GET", u, nil)
		if err != nil {
			return nil, 0, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		body := make([]byte, 0)
		if resp.Body != nil {
			buf := make([]byte, 4096)
			for {
				n, err := resp.Body.Read(buf)
				body = append(body, buf[:n]...)
				if err != nil {
					break
				}
			}
		}
		return body, resp.StatusCode, nil
	}
	body, status, err := fetch(url)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		text := strings.TrimSpace(string(body))
		if text == "" {
			text = fmt.Sprintf("HTTP %d", status)
		}
		return nil, fmt.Errorf("Kiro ListAvailableModels %d: %s", status, text)
	}
	var data map[string]interface{}
	if json.Unmarshal(body, &data) != nil {
		return nil, fmt.Errorf("Kiro ListAvailableModels parse error")
	}
	var models []map[string]interface{}
	if raw, ok := data["models"].([]interface{}); ok {
		for _, item := range raw {
			if m, ok := item.(map[string]interface{}); ok {
				models = append(models, m)
			}
		}
	}
	return models, nil
}

// newModelsClient 30s 超时的抓取客户端（JS AbortController 30s；其余参数
// 与项目既有 newClient 一致）。
func newModelsClient(proxy string) tls_client.HttpClient {
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_144),
		tls_client.WithInsecureSkipVerify(),
	}
	if proxy != "" {
		opts = append(opts, tls_client.WithProxyUrl(proxy))
	}
	c, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		panic(fmt.Sprintf("create models client: %v", err))
	}
	return c
}

// kiroCatalogCacheKey 构建凭证的稳定缓存键（cacheKey：
// psd.profileArn || psd.clientId || refreshToken || accessToken || "anonymous"）。
func kiroCatalogCacheKey(credentials *KiroCredential) string {
	seed := ""
	if credentials != nil {
		seed = credentials.ProviderSpecificData.ProfileArn
		if seed == "" {
			seed = credentials.ProviderSpecificData.ClientId
		}
		if seed == "" {
			seed = credentials.RefreshToken
		}
		if seed == "" {
			seed = credentials.AccessToken
		}
	}
	if seed == "" {
		seed = "anonymous"
	}
	sum := sha256.Sum256([]byte("kiro:" + seed))
	return hex.EncodeToString(sum[:])
}

// ResolveKiroModels 解析凭证的实时模型目录并展开为 9router 变体
// （resolveKiroModels）。任何错误（网络/4xx/5xx）返回 nil，调用方可回退静态
// 目录而不影响仪表盘或 /v1/models（fail-open）。
// onCredentialsRefreshed 在 401 触发 token 刷新后回调（JS 用于持久化）。
func ResolveKiroModels(credentials *KiroCredential, proxy string, forceRefresh bool, onCredentialsRefreshed func(refreshed *KiroRefreshResult)) *KiroCatalogResult {
	if credentials == nil || credentials.AccessToken == "" {
		return nil
	}

	key := kiroCatalogCacheKey(credentials)
	now := time.Now()
	if !forceRefresh {
		kiroCatalogCacheMu.Lock()
		cached, ok := kiroCatalogCache[key]
		kiroCatalogCacheMu.Unlock()
		if ok && cached.ExpiresAt.After(now) {
			return &KiroCatalogResult{Models: cached.Models, RawModels: cached.RawModels}
		}
	}

	raw, err := kiroFetchCatalogRaw(credentials, proxy)
	if err != nil {
		// 401 且有 refreshToken → 刷新后重试一次
		if strings.Contains(err.Error(), "401") && credentials.RefreshToken != "" {
			refreshed := RefreshKiroToken(credentials.RefreshToken, credentials.ProviderSpecificData, proxy)
			if refreshed != nil && refreshed.AccessToken != "" {
				nextAccessToken := refreshed.AccessToken
				nextRefreshToken := credentials.RefreshToken
				if refreshed.RefreshToken != "" {
					nextRefreshToken = refreshed.RefreshToken
				}
				if onCredentialsRefreshed != nil {
					onCredentialsRefreshed(refreshed)
				}
				// 更新内存凭证引用，重试逻辑使用新 token
				credentials.AccessToken = nextAccessToken
				credentials.RefreshToken = nextRefreshToken
				raw, err = kiroFetchCatalogRaw(credentials, proxy)
				if err != nil {
					return nil
				}
			} else {
				return nil
			}
		} else {
			return nil
		}
	}

	expanded := []KiroCatalogModel{}
	for _, m := range raw {
		if m == nil {
			continue
		}
		upstreamID := stringOf(m["modelId"])
		if upstreamID == "" {
			upstreamID = stringOf(m["id"])
		}
		if upstreamID == "" {
			continue
		}
		display := kiroFormatDisplayName(stringOf(m["modelName"]), upstreamID, m["rateMultiplier"])
		ctx := 200000
		if tl := asMap(m["tokenLimits"]); tl != nil {
			if v := asInt(tl["maxInputTokens"]); v != 0 {
				ctx = v
			}
		}
		rate := 1.0
		if r, ok := asFloat(m["rateMultiplier"]); ok {
			rate = r
		}
		for _, v := range kiroBuildVariants(upstreamID, display) {
			v.ContextLength = ctx
			v.RateMultiplier = rate
			v.UpstreamModelID = upstreamID
			v.Description = stringOf(m["description"])
			expanded = append(expanded, v)
		}
	}

	kiroCatalogCacheMu.Lock()
	kiroCatalogCache[key] = kiroCatalogEntry{
		ExpiresAt: now.Add(time.Duration(kiroModelsCacheTTL) * time.Millisecond),
		Models:    expanded,
		RawModels: raw,
	}
	kiroCatalogCacheMu.Unlock()

	return &KiroCatalogResult{Models: expanded, RawModels: raw}
}

// InvalidateKiroModelCache 丢弃该凭证的缓存目录（invalidateKiroModelCache：
// 轮换/导入 token 后调用，下次抓取为全新）。
func InvalidateKiroModelCache(credentials *KiroCredential) {
	if credentials == nil {
		return
	}
	kiroCatalogCacheMu.Lock()
	delete(kiroCatalogCache, kiroCatalogCacheKey(credentials))
	kiroCatalogCacheMu.Unlock()
}

// ClearKiroModelCache 清空整个内存缓存（clearKiroModelCache；测试/调试用）。
func ClearKiroModelCache() {
	kiroCatalogCacheMu.Lock()
	kiroCatalogCache = map[string]kiroCatalogEntry{}
	kiroCatalogCacheMu.Unlock()
}
