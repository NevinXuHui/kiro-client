package reverseproxy

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ===== kiro_extidp.go（kiroExternalIdp.js） =====

func TestValidateMicrosoftTokenEndpoint(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want string // 非空则校验返回值的 host 段
	}{
		{"", false, ""},
		{"http://login.microsoftonline.com/token", false, ""},
		{"https://evil.example.com/token", false, ""},
		{"https://login.microsoftonline.com/common/oauth2/v2.0/token", true, "login.microsoftonline.com"},
		{"https://LOGIN.MICROSOFT.COM/token", true, "LOGIN.MICROSOFT.COM"},
		{"https://login.windows.net/token", true, "login.windows.net"},
	}
	for _, c := range cases {
		got, err := ValidateMicrosoftTokenEndpoint(c.in)
		if c.ok != (err == nil) {
			t.Errorf("ValidateMicrosoftTokenEndpoint(%q) err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && !strings.Contains(got, c.want) {
			t.Errorf("ValidateMicrosoftTokenEndpoint(%q)=%q, want host %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeKiroExternalIdpAuth(t *testing.T) {
	// snake_case 全字段（含 JWT payload 取 email）
	jwt := "eyJhbGciOiJub25lIn0.eyJwcmVmZXJyZWRfdXNlcm5hbWUiOiJ1c2VyQGV4YW1wbGUuY29tIiwiZXhwIjo0MTAyNDQ0ODAwfQ."
	input := map[string]interface{}{
		"auth_method":    "external_idp",
		"access_token":   jwt,
		"refresh_token":  "rt",
		"client_id":      "cid",
		"token_endpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
		"profile_arn":    "arn:aws:codewhisperer:us-east-1:1:profile/p",
		"region":         "us-east-1",
		"scopes":         []interface{}{"openid", "email"},
	}
	auth, err := NormalizeKiroExternalIdpAuth(input)
	if err != nil {
		t.Fatalf("NormalizeKiroExternalIdpAuth: %v", err)
	}
	if auth.AccessToken != jwt || auth.RefreshToken != "rt" || auth.Email != "user@example.com" {
		t.Errorf("auth=%+v", auth)
	}
	psd := auth.ProviderSpecificData
	if psd.AuthMethod != "external_idp" || psd.Provider != "CLIProxyAPI" ||
		psd.Scope != "openid email" || psd.Region != "us-east-1" {
		t.Errorf("psd=%+v", psd)
	}
	// 预期 exp 2025 前的 epoch（4102444800 = 2100 年）被 resolveExpiresAt 采纳
	if auth.ExpiresAt.Unix() != 4102444800 {
		t.Errorf("ExpiresAt=%v", auth.ExpiresAt)
	}

	// 拒绝非 external_idp
	if _, err := NormalizeKiroExternalIdpAuth(map[string]interface{}{"auth_method": "idc"}); err == nil ||
		!strings.Contains(err.Error(), "Only external_idp") {
		t.Errorf("auth_method 拒绝缺失: %v", err)
	}
	// 必需字段缺失
	if _, err := NormalizeKiroExternalIdpAuth(map[string]interface{}{"auth_method": "external_idp"}); err == nil {
		t.Errorf("缺字段未报错")
	}
	// 无效 JSON 字符串
	if _, err := NormalizeKiroExternalIdpAuth("{bad"); err == nil ||
		!strings.Contains(err.Error(), "CLIProxyAPI auth JSON is invalid") {
		t.Errorf("坏 JSON 未报错: %v", err)
	}
	// 区域缺省 us-east-1
	input2 := map[string]interface{}{
		"access_token": jwt, "refresh_token": "rt", "client_id": "c",
		"token_endpoint": "https://login.microsoft.com/t", "profile_arn": "arn:p",
		"scope": "openid",
	}
	auth2, err := NormalizeKiroExternalIdpAuth(input2)
	if err != nil {
		t.Fatalf("auth2: %v", err)
	}
	if auth2.ProviderSpecificData.Region != "us-east-1" {
		t.Errorf("region default=%s", auth2.ProviderSpecificData.Region)
	}
}

func TestBuildExternalIdpRefreshParams(t *testing.T) {
	psd := KiroProviderSpecificData{
		AuthMethod:    "external_idp",
		ClientId:      "cid",
		TokenEndpoint: "https://login.microsoftonline.com/t",
		Scope:         "openid email",
	}
	req, err := BuildExternalIdpRefreshParams("rt", psd)
	if err != nil {
		t.Fatalf("BuildExternalIdpRefreshParams: %v", err)
	}
	body := req.Body.Encode()
	for _, kv := range []string{"grant_type=refresh_token", "client_id=cid", "refresh_token=rt", "scope=openid+email"} {
		if !strings.Contains(body, kv) {
			t.Errorf("form 缺少 %q: %s", kv, body)
		}
	}
	if req.ProviderSpecificData.AuthMethod != "external_idp" || req.ProviderSpecificData.Provider != "" {
		t.Errorf("psdOut=%+v", req.ProviderSpecificData)
	}
	if _, err := BuildExternalIdpRefreshParams("", psd); err == nil {
		t.Errorf("空 refreshToken 未报错")
	}
	bad := psd
	bad.TokenEndpoint = "https://evil.example.com/t"
	if _, err := BuildExternalIdpRefreshParams("rt", bad); err == nil {
		t.Errorf("非 Microsoft 端点未报错")
	}
}

func TestDecodeJwtPayload(t *testing.T) {
	if got := DecodeJwtPayload(""); got != nil {
		t.Errorf("空 JWT 应返回 nil")
	}
	if got := DecodeJwtPayload("a.b"); got != nil {
		t.Errorf("两段 JWT 应返回 nil")
	}
	if got := DecodeJwtPayload("!!.b.c"); got != nil {
		t.Errorf("坏 base64 应返回 nil")
	}
	// {"sub":"u1"} → base64url
	payload := DecodeJwtPayload("eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1MSJ9.")
	if payload["sub"] != "u1" {
		t.Errorf("payload=%v", payload)
	}
}

// ===== kiro_token.go（tokenRefresh.js / providers.js / dedup.js） =====

func TestPickKiroProfileArn(t *testing.T) {
	profiles := []map[string]interface{}{
		{"arn": "arn:aws:codewhisperer:eu-central-1:1:profile/eu"},
		{"profileArn": "arn:aws:codewhisperer:us-east-1:2:profile/us"},
	}
	if got := pickKiroProfileArn(profiles, "us-east-1"); got != "arn:aws:codewhisperer:us-east-1:2:profile/us" {
		t.Errorf("region 匹配失败: %q", got)
	}
	if got := pickKiroProfileArn(profiles, "ap-northeast-1"); got != "arn:aws:codewhisperer:eu-central-1:1:profile/eu" {
		t.Errorf("无匹配应回退 profiles[0]: %q", got)
	}
	if got := pickKiroProfileArn(nil, "us-east-1"); got != "" {
		t.Errorf("空表应返回空串: %q", got)
	}
	// 无 arn/profileArn 字段的条目
	if got := pickKiroProfileArn([]map[string]interface{}{{"x": "y"}}, "us-east-1"); got != "" {
		t.Errorf("无 ARN 字段应返回空串: %q", got)
	}
}

func TestAssertValidAwsRegion(t *testing.T) {
	for _, r := range []string{"us-east-1", "eu-central-1", "ap-northeast-2"} {
		if err := AssertValidAwsRegion(r); err != nil {
			t.Errorf("AssertValidAwsRegion(%q)=%v", r, err)
		}
	}
	for _, r := range []string{"", "US-EAST-1", "us", "us-east-1/../../", "https://evil.com"} {
		if err := AssertValidAwsRegion(r); err == nil {
			t.Errorf("AssertValidAwsRegion(%q) 应失败", r)
		}
	}
}

func TestIsUnrecoverableRefreshError(t *testing.T) {
	for _, e := range []string{"unrecoverable_refresh_error", "refresh_token_reused", "invalid_request", "invalid_grant"} {
		if !IsUnrecoverableRefreshError(&KiroRefreshResult{Error: e}) {
			t.Errorf("%q 应判为不可恢复", e)
		}
	}
	if IsUnrecoverableRefreshError(nil) || IsUnrecoverableRefreshError(&KiroRefreshResult{Error: "other"}) {
		t.Errorf("其他应判为可恢复")
	}
}

func TestKiroDedupRefresh(t *testing.T) {
	kiroRefreshDedupMu.Lock()
	kiroRefreshDedupMap = map[string]*kiroRefreshDedupEntry{}
	kiroRefreshDedupMu.Unlock()

	var calls int32
	fn := func() (*KiroRefreshResult, error) {
		atomic.AddInt32(&calls, 1)
		return &KiroRefreshResult{AccessToken: "tok"}, nil
	}

	// 顺序两次调用：第二次走 10s TTL 缓存
	r1, err := kiroDedupRefresh("kiro:rt", fn)
	if err != nil || r1 == nil {
		t.Fatalf("r1: %v %v", r1, err)
	}
	r2, _ := kiroDedupRefresh("kiro:rt", fn)
	if r2 == nil || r2.AccessToken != "tok" {
		t.Errorf("r2=%+v", r2)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls=%d, want 1（TTL 缓存应复用）", got)
	}

	// 并发 in-flight：两个 goroutine 同时调用，fn 只执行一次
	atomic.StoreInt32(&calls, 0)
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]*KiroRefreshResult, 8)
	blockFn := func() (*KiroRefreshResult, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return &KiroRefreshResult{AccessToken: "tok2"}, nil
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], _ = kiroDedupRefresh("kiro:rt2", blockFn)
		}(i)
	}
	close(start)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("并发 calls=%d, want 1（in-flight 复用）", got)
	}
	for _, r := range results {
		if r == nil || r.AccessToken != "tok2" {
			t.Errorf("并发结果异常: %+v", r)
		}
	}
}

// ===== kiro_credential.go（oauthCredentialManager.js） =====

func TestParseTimeMs(t *testing.T) {
	if got := ParseTimeMs(nil); got != 0 {
		t.Errorf("nil=%d", got)
	}
	if got := ParseTimeMs(""); got != 0 {
		t.Errorf("空串=%d", got)
	}
	// 秒 → ms
	if got := ParseTimeMs(float64(1700000000)); got != 1700000000*1000 {
		t.Errorf("秒→ms=%d", got)
	}
	// ms 原样
	if got := ParseTimeMs(float64(1700000000000)); got != 1700000000000 {
		t.Errorf("ms=%d", got)
	}
	// ISO
	iso := "2026-08-01T00:00:00Z"
	if got := ParseTimeMs(iso); got == 0 {
		t.Errorf("ISO 解析失败")
	}
	if got := ParseTimeMs("bad"); got != 0 {
		t.Errorf("坏字符串=%d", got)
	}
}

func TestShouldRefreshCredentials(t *testing.T) {
	now := time.Now().UnixMilli()
	// 未过期且缓冲充足 → 不刷新
	c := &KiroCredential{ExpiresAtMs: now + 10*60*1000}
	if ShouldRefreshCredentials("kiro", c, now) {
		t.Errorf("不应刷新")
	}
	// 距过期 < 5min 缓冲 → 刷新
	c2 := &KiroCredential{ExpiresAtMs: now + 2*60*1000}
	if !ShouldRefreshCredentials("kiro", c2, now) {
		t.Errorf("应刷新")
	}
	// 无过期时间 → 不刷新
	if ShouldRefreshCredentials("kiro", &KiroCredential{}, now) {
		t.Errorf("无过期时间不应刷新")
	}
	if ShouldRefreshCredentials("kiro", nil, now) {
		t.Errorf("nil 不应刷新")
	}
}

func TestMergeRefreshedCredentials(t *testing.T) {
	now := time.Now().UnixMilli()
	refreshed := &KiroRefreshResult{
		AccessToken:  "at",
		RefreshToken: "rt2",
		ExpiresIn:    3600,
	}
	merged := MergeRefreshedCredentials("kiro", &KiroCredential{RefreshToken: "rt1"}, refreshed, now)
	if merged == nil || merged.AccessToken != "at" || merged.RefreshToken != "rt2" {
		t.Fatalf("merged=%+v", merged)
	}
	if merged.ExpiresAtMs != now+3600*1000 {
		t.Errorf("ExpiresAtMs=%d", merged.ExpiresAtMs)
	}
	if merged.LastRefreshAtMs != now {
		t.Errorf("LastRefreshAtMs=%d", merged.LastRefreshAtMs)
	}

	// 刷新未返回新 refreshToken → 沿用 current
	merged2 := MergeRefreshedCredentials("kiro", &KiroCredential{RefreshToken: "rt1"}, &KiroRefreshResult{AccessToken: "at2", ExpiresIn: 60}, now)
	if merged2.RefreshToken != "rt1" {
		t.Errorf("refreshToken 应沿用: %q", merged2.RefreshToken)
	}

	// 不可恢复错误原样透传
	unrecoverable := MergeRefreshedCredentials("kiro", nil, &KiroRefreshResult{Error: "invalid_grant"}, now)
	if unrecoverable == nil || unrecoverable.RefreshError != "invalid_grant" {
		t.Errorf("unrecoverable=%+v", unrecoverable)
	}

	// nil 刷新结果 → nil
	if MergeRefreshedCredentials("kiro", nil, nil, now) != nil {
		t.Errorf("nil 应返回 nil")
	}
}

func TestWithCredentialRefreshLock(t *testing.T) {
	credentialRefreshLocksMu.Lock()
	credentialRefreshLocks = map[string]*credentialRefreshLock{}
	credentialRefreshLocksMu.Unlock()

	var calls int32
	lockFn := func() *KiroCredential {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return &KiroCredential{AccessToken: "at"}
	}

	var wg sync.WaitGroup
	results := make([]*KiroCredential, 8)
	c := &KiroCredential{Email: "a@b.c", RefreshToken: "rt"}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = WithCredentialRefreshLock("kiro", c, lockFn)
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls=%d, want 1（并发锁共享）", got)
	}
	for _, r := range results {
		if r == nil || r.AccessToken != "at" {
			t.Errorf("锁结果异常: %+v", r)
		}
	}

	// 锁键：connectionId 优先于 refreshToken 后 16 位
	if key := getRefreshLockKey("kiro", &KiroCredential{ConnectionId: "c1", Email: "e"}); key != "kiro:c1" {
		t.Errorf("key=%q", key)
	}
	if key := getRefreshLockKey("kiro", &KiroCredential{RefreshToken: "0123456789abcdef0123456789abcdef"}); key != "kiro:0123456789abcdef" {
		t.Errorf("key=%q", key)
	}
	if key := getRefreshLockKey("kiro", nil); key != "kiro:default" {
		t.Errorf("key=%q", key)
	}
}
