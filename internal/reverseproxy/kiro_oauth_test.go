package reverseproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== kiro_oauth.go（KiroService + 6 条登录路由） =====

func TestBuildKiroSocialLoginURL(t *testing.T) {
	got := BuildKiroSocialLoginURL("google", "ch", "st")
	wantPrefix := "https://prod.us-east-1.auth.desktop.kiro.dev/login?idp=Google"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("google URL=%s, want prefix %s", got, wantPrefix)
	}
	for _, part := range []string{
		"redirect_uri=kiro%3A%2F%2Fkiro.kiroAgent%2Fauthenticate-success",
		"code_challenge=ch",
		"code_challenge_method=S256",
		"state=st",
		"prompt=select_account",
	} {
		if !strings.Contains(got, part) {
			t.Errorf("URL 缺少 %q: %s", part, got)
		}
	}
	got2 := BuildKiroSocialLoginURL("github", "c", "s")
	if !strings.Contains(got2, "idp=Github") {
		t.Errorf("github URL=%s", got2)
	}
}

func TestExtractKiroEmailFromJWT(t *testing.T) {
	// {"email":"a@b.c","sub":"s1"} → base64url（补 padding）
	payload := `{"email":"a@b.c","sub":"s1"}`
	b64 := base64URLEncode([]byte(payload))
	jwt := "h." + b64 + ".sig"
	if got := ExtractKiroEmailFromJWT(jwt); got != "a@b.c" {
		t.Errorf("email=%q", got)
	}
	if got := ExtractKiroEmailFromJWT("bad"); got != "" {
		t.Errorf("bad JWT 应返回空串: %q", got)
	}
	// preferred_username 兜底
	p2 := `{"preferred_username":"u@x.io"}` + ""
	jwt2 := "h." + base64URLEncode([]byte(p2)) + ".sig"
	if got := ExtractKiroEmailFromJWT(jwt2); got != "u@x.io" {
		t.Errorf("preferred_username=%q", got)
	}
}

func base64URLEncode(b []byte) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 0, 4*((len(b)+2)/3))
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		n = uint32(b[i]) << 16
		if rem > 1 {
			n |= uint32(b[i+1]) << 8
		}
		if rem > 2 {
			n |= uint32(b[i+2])
		}
		out = append(out, chars[(n>>18)&63], chars[(n>>12)&63])
		if rem > 1 {
			out = append(out, chars[(n>>6)&63])
		} else {
			out = append(out, '=')
		}
		if rem > 2 {
			out = append(out, chars[n&63])
		} else {
			out = append(out, '=')
		}
	}
	return string(out)
}

func TestValidateKiroAPIKey(t *testing.T) {
	// 空/空白 key → 报错且不发请求
	if _, err := ValidateKiroAPIKey("", "us-east-1"); err == nil ||
		!strings.Contains(err.Error(), "API key is required") {
		t.Errorf("空 key err=%v", err)
	}
	if _, err := ValidateKiroAPIKey("   ", "us-east-1"); err == nil {
		t.Errorf("空白 key 未报错")
	}
}

func TestKiroImportToken(t *testing.T) {
	if _, err := KiroImportToken("", "", "", "", "", ""); err == nil ||
		!strings.Contains(err.Error(), "Refresh token is required") {
		t.Errorf("空 refreshToken err=%v", err)
	}
}

func TestKiroSocialAuthorize(t *testing.T) {
	if _, err := KiroSocialAuthorize("x"); err == nil ||
		!strings.Contains(err.Error(), "Invalid provider") {
		t.Errorf("非法 provider err=%v", err)
	}
	params, err := KiroSocialAuthorize("google")
	if err != nil {
		t.Fatalf("KiroSocialAuthorize: %v", err)
	}
	if !strings.Contains(params.AuthURL, "idp=Google") || !strings.Contains(params.AuthURL, "code_challenge="+params.CodeChallenge) {
		t.Errorf("authUrl=%s", params.AuthURL)
	}
	if len(params.State) == 0 || len(params.CodeVerifier) == 0 || len(params.CodeChallenge) == 0 {
		t.Errorf("PKCE 参数缺失: %+v", params)
	}
}

func TestValidateKiroImportToken(t *testing.T) {
	if _, err := ValidateKiroImportToken("badprefix"); err == nil ||
		!strings.Contains(err.Error(), "Invalid token format") {
		t.Errorf("前缀校验 err=%v", err)
	}
}

func TestGeneratePKCE(t *testing.T) {
	p1, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	p2, _ := GeneratePKCE()
	if p1.CodeVerifier == p2.CodeVerifier || p1.State == p2.State {
		t.Errorf("PKCE 应随机")
	}
	// 32 字节 base64url → 43 字符
	if len(p1.CodeVerifier) != 43 || len(p1.CodeChallenge) != 43 || len(p1.State) != 43 {
		t.Errorf("长度异常: %+v", p1)
	}
}

// ===== KiroAutoImport（auto-import 路由：文件系统会话初始化） =====

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestKiroAutoImport(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("APPDATA", filepath.Join(tmp, "AppData", "Roaming"))

	cache := filepath.Join(tmp, ".aws", "sso", "cache")

	// 场景 1：完整链路（kiro-auth-token.json + clientIdHash + profile.json 区域归一化）
	writeTestFile(t, filepath.Join(cache, "kiro-auth-token.json"),
		`{"refreshToken":"aorAAAAAGtoken","region":"eu-central-1","authMethod":"idc","clientIdHash":"abc123"}`)
	writeTestFile(t, filepath.Join(cache, "abc123.json"),
		`{"clientId":"cid","clientSecret":"csec"}`)
	writeTestFile(t, filepath.Join(tmp, "AppData", "Roaming", "Kiro", "User", "globalStorage", "kiro.kiroagent", "profile.json"),
		`{"arn":"arn:aws:codewhisperer:eu-central-1:123:profile/p"}`)

	r := KiroAutoImport()
	if !r.Found {
		t.Fatalf("场景1 found=false: %+v", r)
	}
	if r.RefreshToken != "aorAAAAAGtoken" || r.Source != "kiro-auth-token.json" ||
		r.ClientID != "cid" || r.ClientSecret != "csec" ||
		r.Region != "eu-central-1" || r.AuthMethod != "idc" {
		t.Errorf("场景1=%+v", r)
	}
	if r.ProfileArn != "arn:aws:codewhisperer:us-east-1:123:profile/p" {
		t.Errorf("ARN 区域未归一化: %q", r.ProfileArn)
	}

	// 场景 2：无 kiro-auth-token.json，遍历其他 .json（非 aor 前缀跳过）
	writeTestFile(t, filepath.Join(cache, "other.json"), `{"refreshToken":"not-kiro"}`)
	writeTestFile(t, filepath.Join(cache, "good.json"), `{"refreshToken":"aorAAAAAGgood"}`)
	if err := os.Remove(filepath.Join(cache, "kiro-auth-token.json")); err != nil {
		t.Fatal(err)
	}
	r2 := KiroAutoImport()
	if !r2.Found || r2.RefreshToken != "aorAAAAAGgood" || r2.Source != "good.json" {
		t.Errorf("场景2=%+v", r2)
	}

	// 场景 3：缓存目录不存在
	emptyHome := t.TempDir()
	t.Setenv("USERPROFILE", emptyHome)
	t.Setenv("HOME", emptyHome)
	r3 := KiroAutoImport()
	if r3.Found || !strings.Contains(r3.Error, "AWS SSO cache not found") {
		t.Errorf("场景3=%+v", r3)
	}

	// 场景 4：缓存存在但无有效 token
	os.MkdirAll(filepath.Join(emptyHome, ".aws", "sso", "cache"), 0755)
	r4 := KiroAutoImport()
	if r4.Found || !strings.Contains(r4.Error, "Kiro token not found") {
		t.Errorf("场景4=%+v", r4)
	}
}

func TestKiroImportCLIProxyAuth(t *testing.T) {
	// 畸形 JSON 字符串 → 报错（route 400 语义）
	body := map[string]interface{}{"json": "{bad"}
	if _, err := KiroImportCLIProxyAuth(body); err == nil {
		t.Errorf("坏 JSON 未报错")
	}
	// cliProxyAuth 字段优先
	good := map[string]interface{}{
		"cliProxyAuth": map[string]interface{}{
			"auth_method":    "external_idp",
			"access_token":   "tok",
			"refresh_token":  "rt",
			"client_id":      "cid",
			"token_endpoint": "https://login.microsoftonline.com/t",
			"profile_arn":    "arn:p",
			"scope":          "openid",
		},
	}
	conn, err := KiroImportCLIProxyAuth(good)
	if err != nil {
		t.Fatalf("KiroImportCLIProxyAuth: %v", err)
	}
	if conn.AuthType != "oauth" || conn.ProviderSpecificData.AuthMethod != "external_idp" ||
		conn.ProviderSpecificData.Provider != "CLIProxyAPI" {
		t.Errorf("conn=%+v", conn)
	}
}
