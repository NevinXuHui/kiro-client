package pool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToExportFieldSet(t *testing.T) {
	t.Parallel()
	acc := &Account{
		ClientID:     "cid",
		ClientSecret: "sec",
		CreditLimit:  50,
		CreditUsed:   1,
		Email:        "a@b.com",
		Password:     "pw",
		Provider:     "BuilderId",
		ProxyIP:      "1.2.3.4",
		ProxyRegion:  "US California",
		RefreshToken: "aorAAAAAGxxx",
		Region:       "us-east-1",
		Subscription: "KIRO FREE",
	}
	raw, err := ExportAccountsJSON([]*Account{acc})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"clientId", "clientSecret", "creditLimit", "creditUsed",
		"email", "password", "provider", "proxy_ip", "proxy_region",
		"refreshToken", "region", "subscription",
	} {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing key %s in %s", k, raw)
		}
	}
	if got["password"] != "pw" || got["proxy_ip"] != "1.2.3.4" {
		t.Fatalf("values: %s", raw)
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		t.Fatalf("single account should export as object, got %s", raw)
	}
}

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                "account",
		"a@b.com":         "a@b.com",
		"a/b\\c:d":        "a_b_c_d",
		"...":             "account",
		"user name@x.com": "user_name@x.com",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Fatalf("SanitizeFilename(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseAccountJSONObjectAndArray(t *testing.T) {
	t.Parallel()
	one, err := ParseAccountJSON([]byte(`{"email":"a@b.com","clientId":"c","clientSecret":"s","refreshToken":"aorAAAAAGx"}`))
	if err != nil || len(one) != 1 || one[0].Email != "a@b.com" {
		t.Fatalf("object: %v %+v", err, one)
	}
	many, err := ParseAccountJSON([]byte(`[{"email":"a@b.com"},{"email":"b@c.com"}]`))
	if err != nil || len(many) != 2 {
		t.Fatalf("array: %v %+v", err, many)
	}
}
