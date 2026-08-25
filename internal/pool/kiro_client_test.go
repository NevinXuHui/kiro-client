package pool

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

// TestNeedsRefresh 验证增量刷新判定（9router 惰性思想）
func TestNeedsRefresh(t *testing.T) {
	now := time.Now()
	old := now.Add(-10 * time.Minute).Format(time.RFC3339)
	fresh := now.Add(-30 * time.Second).Format(time.RFC3339)
	ttl := 5 * time.Minute

	cases := []struct {
		name string
		acc  *Account
		want bool
	}{
		{"nil", nil, false},
		{"healthy fresh", &Account{HealthStatus: "healthy", HealthCheckedAt: fresh}, false},
		{"healthy stale", &Account{HealthStatus: "healthy", HealthCheckedAt: old}, true},
		{"never checked", &Account{HealthStatus: "healthy"}, true},
		{"429 fresh", &Account{HealthStatus: "healthy", HealthCode: "429", HealthCheckedAt: fresh}, false},
		{"429 stale", &Account{HealthStatus: "healthy", HealthCode: "429", HealthCheckedAt: old}, true},
		{"auth dead", &Account{HealthStatus: "unhealthy", HealthCode: "AUTH", HealthCheckedAt: old}, false},
		{"net retry", &Account{HealthStatus: "unhealthy", HealthCode: "NET", HealthCheckedAt: fresh}, true},
		{"5xx retry", &Account{HealthStatus: "unhealthy", HealthCode: "502", HealthCheckedAt: fresh}, true},
		{"unhealthy no code", &Account{HealthStatus: "unhealthy", HealthCheckedAt: fresh}, true},
	}
	for _, c := range cases {
		if got := NeedsRefresh(c.acc, now, ttl); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestClassifyHealthError 验证 9router 同款健康细分分类
func TestClassifyHealthError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status string
		code   string
	}{
		{"nil", nil, "healthy", ""},
		{"401", errors.New("refresh token HTTP 401: InvalidClientTokenId"), "unhealthy", "AUTH"},
		{"403", errors.New("refresh token HTTP 403: forbidden"), "unhealthy", "AUTH"},
		{"429", errors.New("refresh token HTTP 429: rate limit"), "healthy", "429"},
		{"502", errors.New("refresh token HTTP 502: bad gateway"), "unhealthy", "502"},
		{"503", errors.New("refresh token HTTP 503: unavailable"), "unhealthy", "503"},
		{"504", errors.New("refresh token HTTP 504: timeout"), "unhealthy", "504"},
		{"other code", errors.New("refresh token HTTP 418: teapot"), "unhealthy", "418"},
		{"network", errors.New("dial tcp: connection refused"), "unhealthy", "NET"},
		{"random text", errors.New("some random error"), "unhealthy", "NET"},
	}
	for _, c := range cases {
		status, code := classifyHealthError(c.err)
		if status != c.status || code != c.code {
			t.Errorf("%s: got (%s,%s), want (%s,%s)", c.name, status, code, c.status, c.code)
		}
	}
}

func TestUsageQueryProfileArn(t *testing.T) {
	if got := usageQueryProfileArn(""); got != builderIDProfileARN {
		t.Fatalf("empty ARN should default to placeholder, got %q", got)
	}
	if got := usageQueryProfileArn(builderIDProfileARN); got != builderIDProfileARN {
		t.Fatalf("placeholder must be sent as-is, got %q", got)
	}
}

func TestUsageLimitsURLSendsPlaceholder(t *testing.T) {
	u := usageLimitsURL("us-east-1", "")
	if !strings.Contains(u, "profileArn="+url.QueryEscape(builderIDProfileARN)) {
		t.Fatalf("placeholder must be sent: %s", u)
	}
	if !strings.Contains(u, "isEmailRequired=true") {
		t.Fatalf("missing isEmailRequired: %s", u)
	}
}

func TestAvailableModelsURLSendsPlaceholder(t *testing.T) {
	u := availableModelsURL("us-east-1", "")
	if !strings.Contains(u, "profileArn="+url.QueryEscape(builderIDProfileARN)) {
		t.Fatalf("placeholder must be sent: %s", u)
	}
}

func TestApplyQRESTHeadersPinsModelsUA(t *testing.T) {
	h := make(fhttp.Header)
	applyQRESTHeaders(h, "tok")
	if got := h.Get("User-Agent"); !strings.Contains(got, "KiroIDE-2.3.0-") {
		t.Fatalf("User-Agent must pin 2.3.0, got %q", got)
	}
	if got := h.Get("x-amz-user-agent"); !strings.Contains(got, "KiroIDE-2.3.0-") {
		t.Fatalf("x-amz-user-agent must pin 2.3.0, got %q", got)
	}
}
