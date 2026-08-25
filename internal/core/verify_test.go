package core

import (
	"net/url"
	"strings"
	"testing"
)

func TestQEndpointURLAlwaysSendsProfileArn(t *testing.T) {
	u := qEndpointURL("getUsageLimits", "origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true", "")
	if !strings.Contains(u, "profileArn="+url.QueryEscape(builderIDProfileARN)) {
		t.Fatalf("empty ARN should send placeholder: %s", u)
	}
	u = qEndpointURL("ListAvailableModels", "origin=AI_EDITOR", builderIDProfileARN)
	if !strings.Contains(u, "profileArn="+url.QueryEscape(builderIDProfileARN)) {
		t.Fatalf("placeholder must be sent: %s", u)
	}
	real := "arn:aws:codewhisperer:us-east-1:123456789012:profile/ABCD"
	u = qEndpointURL("ListAvailableModels", "origin=AI_EDITOR", real)
	if !strings.Contains(u, "profileArn="+url.QueryEscape(real)) {
		t.Fatalf("real ARN missing: %s", u)
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
