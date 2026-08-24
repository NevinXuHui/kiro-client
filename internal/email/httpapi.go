package email

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"reg_go/internal/storage"
)

// HttpAPIAccount HTTP API 邮箱：用 GET 接口拉信提取验证码。
// 卡密：email----https://host/api?...  （支持「邮箱：」前缀）
type HttpAPIAccount struct {
	Email  string
	APIURL string
}

var (
	httpAPIPrefixRe    = regexp.MustCompile(`(?i)^(邮箱[：:]|email[：:]?)\s*`)
	httpAPISpanCodeRe  = regexp.MustCompile(`(?i)<span[^>]*class=["'][^"']*\bcode\b[^"']*["'][^>]*>\s*(\d{4,8})\s*</span>`)
	httpAPILabelCodeRe = regexp.MustCompile(`验证码[：:]\s*(\d{4,8})`)
	httpAPISixDigitRe  = regexp.MustCompile(`\b(\d{6})\b`)
	httpAPITagRe       = regexp.MustCompile(`<[^>]+>`)
)

func looksLikeOTP(s string) bool {
	if len(s) < 4 || len(s) > 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseHttpAPILine(line string) (HttpAPIAccount, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return HttpAPIAccount{}, false
	}
	line = httpAPIPrefixRe.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)

	var addr, apiURL string
	if strings.Contains(line, "----") {
		parts := strings.SplitN(line, "----", 2)
		addr = strings.TrimSpace(parts[0])
		apiURL = strings.TrimSpace(parts[1])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return HttpAPIAccount{}, false
		}
		apiURL = fields[len(fields)-1]
		addr = strings.TrimSpace(strings.Join(fields[:len(fields)-1], " "))
	}

	if addr == "" || !strings.Contains(addr, "@") {
		return HttpAPIAccount{}, false
	}
	if !strings.HasPrefix(apiURL, "http://") && !strings.HasPrefix(apiURL, "https://") {
		return HttpAPIAccount{}, false
	}
	return HttpAPIAccount{Email: addr, APIURL: apiURL}, true
}

// ParseHttpAPILines 从文本解析 HTTP API 邮箱（换行分隔，# 开头为注释）。
func ParseHttpAPILines(data string) []HttpAPIAccount {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil
	}
	var accounts []HttpAPIAccount
	for _, line := range strings.Split(data, "\n") {
		if acc, ok := parseHttpAPILine(line); ok {
			accounts = append(accounts, acc)
		} else if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			preview := strings.TrimSpace(line)
			if len(preview) > 50 {
				preview = preview[:50]
			}
			log.Printf("[HTTP API] 跳过格式错误的行: %s", preview)
		}
	}
	return accounts
}

// preferJSONURL 把 format=html 换成 json，便于结构化解析；失败时调用方回退原 URL。
func preferJSONURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	if strings.EqualFold(q.Get("format"), "json") {
		return raw
	}
	q.Set("format", "json")
	u.RawQuery = q.Encode()
	return u.String()
}

func fetchHttpAPI(apiURL string) (string, error) {
	client := httpClientWithProxy(storage.GetProxy(), 20*time.Second)

	try := func(u string, acceptJSON bool) (string, error) {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")
		if acceptJSON {
			req.Header.Set("Accept", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 429 {
			return "", fmt.Errorf("接口限流")
		}
		if resp.StatusCode != 200 {
			preview := string(raw)
			if len(preview) > 200 {
				preview = preview[:200]
			}
			return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, preview)
		}
		return string(raw), nil
	}

	jsonURL := preferJSONURL(apiURL)
	body, err := try(jsonURL, true)
	if err == nil {
		return body, nil
	}
	if jsonURL != apiURL {
		log.Printf("[HTTP API] JSON 拉取失败，回退原地址: %v", err)
		return try(apiURL, false)
	}
	return "", err
}

func collectOTP(dst *[]string, seen map[string]bool, code string) {
	if !looksLikeOTP(code) || seen[code] {
		return
	}
	seen[code] = true
	*dst = append(*dst, code)
}

func collectOTPFromText(dst *[]string, seen map[string]bool, text string) {
	if text == "" {
		return
	}
	if m := httpAPISpanCodeRe.FindStringSubmatch(text); len(m) > 1 {
		collectOTP(dst, seen, m[1])
	}
	if m := httpAPILabelCodeRe.FindStringSubmatch(text); len(m) > 1 {
		collectOTP(dst, seen, m[1])
	}
	plain := httpAPITagRe.ReplaceAllString(text, " ")
	for _, m := range httpAPISixDigitRe.FindAllStringSubmatch(plain, -1) {
		collectOTP(dst, seen, m[1])
	}
}

func walkJSONForOTP(v interface{}, key string, dst *[]string, seen map[string]bool) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			walkJSONForOTP(val, k, dst, seen)
		}
	case []interface{}:
		for _, item := range t {
			walkJSONForOTP(item, key, dst, seen)
		}
	case string:
		if key == "code" || key == "otp" || key == "verification_code" {
			collectOTP(dst, seen, strings.TrimSpace(t))
		}
		collectOTPFromText(dst, seen, t)
	case float64:
		if key == "code" || key == "otp" || key == "verification_code" {
			collectOTP(dst, seen, strconv.Itoa(int(t)))
		}
	}
}

func extractCodesFromJSON(body string) ([]string, bool) {
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, false
	}
	var out []string
	seen := map[string]bool{}
	walkJSONForOTP(v, "", &out, seen)
	return out, true
}

func extractCodesFromHTML(body string) []string {
	var out []string
	seen := map[string]bool{}
	collectOTPFromText(&out, seen, body)
	return out
}

// extractHttpAPICodes 从 API 响应中提取 4–8 位数字码（JSON 优先）。
func extractHttpAPICodes(body string) []string {
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if codes, ok := extractCodesFromJSON(trimmed); ok {
			return codes
		}
	}
	return extractCodesFromHTML(body)
}

func pickNewOTP(body string, exclude map[string]bool) string {
	for _, code := range extractHttpAPICodes(body) {
		if len(code) == 6 && !exclude[code] {
			return code
		}
	}
	return ""
}

// SnapshotHttpAPICodes 发送 OTP 前快照已有验证码，避免误用旧信。
func SnapshotHttpAPICodes(acc HttpAPIAccount) map[string]bool {
	out := map[string]bool{}
	body, err := fetchHttpAPI(acc.APIURL)
	if err != nil {
		log.Printf("[HTTP API] 基线拉取失败: %v", err)
		return out
	}
	for _, c := range extractHttpAPICodes(body) {
		out[c] = true
	}
	return out
}

// WaitForHttpAPICode 轮询 HTTP 接口等待 6 位验证码。
func WaitForHttpAPICode(acc HttpAPIAccount, exclude map[string]bool, timeout, interval int) (string, error) {
	if interval <= 0 {
		interval = 3
	}
	if exclude == nil {
		exclude = map[string]bool{}
	}
	maxRetries := timeout / interval
	if maxRetries < 1 {
		maxRetries = 1
	}
	log.Printf("[HTTP API] 等待验证码, 邮箱=%s", acc.Email)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		body, err := fetchHttpAPI(acc.APIURL)
		if err != nil {
			if attempt%5 == 0 {
				log.Printf("[HTTP API] 拉取失败: %v，重试中...", err)
			}
		} else if code := pickNewOTP(body, exclude); code != "" {
			log.Printf("[HTTP API] 获取到验证码: %s", code)
			return code, nil
		} else if attempt%5 == 0 {
			log.Printf("[HTTP API] [%d/%d] 暂无新验证码...", attempt, maxRetries)
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
	return "", fmt.Errorf("等待验证码超时 (%ds)", timeout)
}
