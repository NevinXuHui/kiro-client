package email

import (
	"strings"
	"testing"
)

func TestParseHttpAPILineFormats(t *testing.T) {
	t.Parallel()

	url := "https://ourmail.top/api/v1/mailboxes/messages?limit=1&format=html&email=a@icloud.com"
	cases := []struct {
		name string
		in   string
		want int
		mail string
	}{
		{"dash", "a@icloud.com----" + url, 1, "a@icloud.com"},
		{"prefix-cn", "邮箱：a@icloud.com----" + url, 1, "a@icloud.com"},
		{"prefix-en", "email: a@icloud.com----" + url, 1, "a@icloud.com"},
		{"space", "a@icloud.com " + url, 1, "a@icloud.com"},
		{"comment", "# a@icloud.com----" + url, 0, ""},
		{"short", "a@icloud.com----not-a-url", 0, ""},
		{"no-at", "not-email----" + url, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseHttpAPILines(tc.in)
			if len(got) != tc.want {
				t.Fatalf("got %d, want %d: %+v", len(got), tc.want, got)
			}
			if tc.want == 1 && got[0].Email != tc.mail {
				t.Fatalf("email=%q want %q", got[0].Email, tc.mail)
			}
			if tc.want == 1 && got[0].APIURL != url {
				t.Fatalf("url=%q", got[0].APIURL)
			}
		})
	}
}

func TestParseHttpAPILinesMultiline(t *testing.T) {
	t.Parallel()
	data := strings.Join([]string{
		"邮箱：a@icloud.com----https://ourmail.top/api/v1/mailboxes/messages?limit=1&format=html&email=a@icloud.com",
		"",
		"# skip",
		"b@icloud.com----https://example.com/mail?email=b@icloud.com",
	}, "\n")
	got := ParseHttpAPILines(data)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].Email != "a@icloud.com" || got[1].Email != "b@icloud.com" {
		t.Fatalf("%+v", got)
	}
}

func TestPreferJSONURL(t *testing.T) {
	t.Parallel()
	in := "https://ourmail.top/api/v1/mailboxes/messages?&limit=1&format=html&email=a@icloud.com"
	out := preferJSONURL(in)
	if !strings.Contains(out, "format=json") {
		t.Fatalf("missing format=json: %s", out)
	}
	if strings.Contains(out, "format=html") {
		t.Fatalf("still html: %s", out)
	}
	if preferJSONURL(out) != out {
		t.Fatalf("json url should be stable")
	}
}

func TestExtractHttpAPICodesHTML(t *testing.T) {
	t.Parallel()
	html := `<article><h2>AWS</h2><span class="code">482913</span><p>验证码: 482913</p></article>`
	got := extractHttpAPICodes(html)
	if len(got) == 0 || got[0] != "482913" {
		t.Fatalf("got %v", got)
	}
}

func TestExtractHttpAPICodesJSONSkipStatus(t *testing.T) {
	t.Parallel()
	body := `{"code":0,"data":{"recipient":"a@icloud.com","messages":[]}}`
	got := extractHttpAPICodes(body)
	if len(got) != 0 {
		t.Fatalf("status code 0 should not be OTP, got %v", got)
	}
}

func TestExtractHttpAPICodesJSONMessage(t *testing.T) {
	t.Parallel()
	body := `{"code":0,"data":{"messages":[{"subject":"Verify","html":"<span class='code'>193746</span>"}]}}`
	got := extractHttpAPICodes(body)
	found := false
	for _, c := range got {
		if c == "193746" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing 193746 in %v", got)
	}
}

func TestPickNewOTPExcludesBaseline(t *testing.T) {
	t.Parallel()
	body := `<span class="code">111111</span>`
	if code := pickNewOTP(body, map[string]bool{"111111": true}); code != "" {
		t.Fatalf("excluded code leaked: %s", code)
	}
	if code := pickNewOTP(body, map[string]bool{}); code != "111111" {
		t.Fatalf("got %q", code)
	}
}
