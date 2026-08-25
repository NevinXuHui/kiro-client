package reverseproxy

import (
	"net/url"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestEffectiveProfileArn(t *testing.T) {
	placeholder := KiroDefaultProfileARN
	if got := EffectiveProfileArn(placeholder); got != "" {
		t.Fatalf("placeholder ARN should be omitted, got %q", got)
	}
	if got := EffectiveProfileArn("  "); got != "" {
		t.Fatalf("empty ARN should be omitted, got %q", got)
	}
	real := "arn:aws:codewhisperer:us-east-1:123456789012:profile/ABCD"
	if got := EffectiveProfileArn(real); got != real {
		t.Fatalf("real ARN = %q, want %q", got, real)
	}
}

func TestUsageQueryProfileArn(t *testing.T) {
	if got := UsageQueryProfileArn(""); got != KiroDefaultProfileARN {
		t.Fatalf("empty ARN should default to placeholder, got %q", got)
	}
	if got := UsageQueryProfileArn(KiroDefaultProfileARN); got != KiroDefaultProfileARN {
		t.Fatalf("placeholder must be sent as-is, got %q", got)
	}
	real := "arn:aws:codewhisperer:us-east-1:123456789012:profile/ABCD"
	if got := UsageQueryProfileArn(real); got != real {
		t.Fatalf("real ARN = %q, want %q", got, real)
	}
}

func TestUsageLimitsURLSendsPlaceholder(t *testing.T) {
	u := UsageLimitsURL("us-east-1", KiroDefaultProfileARN)
	if !strings.Contains(u, "profileArn="+url.QueryEscape(KiroDefaultProfileARN)) {
		t.Fatalf("placeholder must be sent: %s", u)
	}
	if !strings.Contains(u, "isEmailRequired=true") {
		t.Fatalf("missing isEmailRequired: %s", u)
	}
}

func TestUsageLimitsURLEncodesRealArn(t *testing.T) {
	arn := "arn:aws:codewhisperer:us-east-1:123456789012:profile/ABCD"
	u := UsageLimitsURL("us-west-2", arn)
	if !strings.Contains(u, "q.us-west-2.amazonaws.com/getUsageLimits") {
		t.Fatalf("unexpected host/path: %s", u)
	}
	if !strings.Contains(u, "profileArn="+url.QueryEscape(arn)) {
		t.Fatalf("ARN not encoded: %s", u)
	}
}

func TestAvailableModelsURL(t *testing.T) {
	u := AvailableModelsURL("us-east-1", "")
	if !strings.Contains(u, "profileArn="+url.QueryEscape(KiroDefaultProfileARN)) {
		t.Fatalf("empty ARN should send placeholder: %s", u)
	}
	arn := "arn:aws:codewhisperer:us-east-1:1:profile/X"
	u = AvailableModelsURL("us-east-1", arn)
	if !strings.Contains(u, "profileArn="+url.QueryEscape(arn)) {
		t.Fatalf("missing encoded ARN: %s", u)
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
	if h.Get("Authorization") != "Bearer tok" {
		t.Fatalf("Authorization = %q", h.Get("Authorization"))
	}
}
