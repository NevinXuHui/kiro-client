package reverseproxy

import (
	"net/url"
	"strings"
	"testing"
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

func TestUsageLimitsURLOmitsPlaceholder(t *testing.T) {
	u := UsageLimitsURL("us-east-1", KiroDefaultProfileARN)
	if strings.Contains(u, "profileArn=") {
		t.Fatalf("placeholder must not be sent: %s", u)
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
	if strings.Contains(u, "profileArn=") {
		t.Fatalf("empty ARN leaked: %s", u)
	}
	arn := "arn:aws:codewhisperer:us-east-1:1:profile/X"
	u = AvailableModelsURL("us-east-1", arn)
	if !strings.Contains(u, "profileArn="+url.QueryEscape(arn)) {
		t.Fatalf("missing encoded ARN: %s", u)
	}
}
