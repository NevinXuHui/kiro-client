package email

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"reg_go/internal/storage"
)

// OutlookAccount Outlook 邮箱账号
// 卡密两种顺序均支持：
//
//	email----密码----clientid----refresh_token
//	email----密码----refresh_token----clientid
type OutlookAccount struct {
	Email        string
	Password     string
	ClientID     string
	RefreshToken string
	AccessToken  string
}

var outlookClientIDRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func looksLikeOutlookClientID(s string) bool {
	return outlookClientIDRe.MatchString(s)
}

func looksLikeOutlookRefresh(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "M.") || strings.HasPrefix(s, "0.A") {
		return true
	}
	return len(s) >= 80
}

// classifyClientAndRefresh 识别 clientid / refresh_token 顺序。
// UUID → clientid；M. 前缀或超长串 → refresh_token；无法判断时保持传入顺序。
func classifyClientAndRefresh(a, b string) (clientID, refresh string) {
	aID, bID := looksLikeOutlookClientID(a), looksLikeOutlookClientID(b)
	switch {
	case aID && !bID:
		return a, b
	case bID && !aID:
		return b, a
	}
	aRT, bRT := looksLikeOutlookRefresh(a), looksLikeOutlookRefresh(b)
	switch {
	case aRT && !bRT:
		return b, a
	case bRT && !aRT:
		return a, b
	default:
		return a, b
	}
}

// parseOutlookLine 解析一行卡密（clientid / refresh_token 两种顺序均可）
func parseOutlookLine(line string) (OutlookAccount, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return OutlookAccount{}, false
	}
	parts := strings.Split(line, "----")
	if len(parts) < 4 {
		return OutlookAccount{}, false
	}
	clientID, refresh := classifyClientAndRefresh(strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3]))
	acc := OutlookAccount{
		Email:        strings.TrimSpace(parts[0]),
		Password:     strings.TrimSpace(parts[1]),
		ClientID:     clientID,
		RefreshToken: refresh,
	}
	if len(parts) >= 5 {
		acc.AccessToken = strings.TrimSpace(parts[4])
	}
	if acc.Email == "" || !strings.Contains(acc.Email, "@") || acc.ClientID == "" || acc.RefreshToken == "" {
		return OutlookAccount{}, false
	}
	return acc, true
}

// ParseOutlookCSV 解析 outlook.csv / txt（每行一条卡密）
func ParseOutlookCSV(path string) ([]OutlookAccount, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseOutlookLines(string(data)), nil
}

// ParseOutlookLines 从文本解析 Outlook 账号。
// 换行分隔优先；单行时再按空白拆多条卡密。
func ParseOutlookLines(data string) []OutlookAccount {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil
	}
	lines := strings.Split(data, "\n")
	if len(lines) == 1 {
		parts := strings.Fields(data)
		if len(parts) > 1 {
			var accounts []OutlookAccount
			for _, part := range parts {
				if acc, ok := parseOutlookLine(part); ok {
					accounts = append(accounts, acc)
				}
			}
			if len(accounts) > 0 {
				return accounts
			}
		}
		if acc, ok := parseOutlookLine(lines[0]); ok {
			return []OutlookAccount{acc}
		}
		return nil
	}
	var accounts []OutlookAccount
	for _, line := range lines {
		if acc, ok := parseOutlookLine(line); ok {
			accounts = append(accounts, acc)
		} else if strings.TrimSpace(line) != "" {
			preview := line
			if len(preview) > 50 {
				preview = preview[:50]
			}
			log.Printf("跳过格式错误的行: %s", preview)
		}
	}
	return accounts
}

// outlookTokenEndpoints 与常见卡密签发端对齐：先 common（不带 scope），再 consumers。
var outlookTokenEndpoints = []string{
	"https://login.microsoftonline.com/common/oauth2/v2.0/token",
	"https://login.microsoftonline.com/consumers/oauth2/v2.0/token",
}

// RefreshOutlookToken 用 refresh_token 换 access_token（优先走全局代理，失败时降级直连）
func RefreshOutlookToken(acc OutlookAccount) (string, error) {
	if acc.ClientID == "" || acc.RefreshToken == "" {
		return "", fmt.Errorf("缺少 client_id 或 refresh_token，无法 OAuth2 收信")
	}
	form := url.Values{
		"client_id":     {acc.ClientID},
		"refresh_token": {acc.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	body := strings.NewReader(form.Encode())

	proxyURL := storage.GetProxy()
	var lastErr error
	for _, endpoint := range outlookTokenEndpoints {
		token, newRT, err := postOutlookToken(endpoint, body, proxyURL)
		if err != nil && proxyURL != "" {
			log.Printf("[Outlook OAuth] 代理请求失败，降级直连：%v", err)
			token, newRT, err = postOutlookToken(endpoint, strings.NewReader(form.Encode()), "")
		}
		if err != nil {
			lastErr = err
			body = strings.NewReader(form.Encode())
			continue
		}
		if newRT != "" && newRT != acc.RefreshToken {
			persistOutlookRefreshToken(acc.Email, newRT)
		}
		return token, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("刷新 Outlook Token 失败")
}

func postOutlookToken(endpoint string, body *strings.Reader, proxyURL string) (accessToken, newRefresh string, err error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")

	client := httpClientWithProxy(proxyURL, 30*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	_ = json.Unmarshal(raw, &result)
	if desc, _ := result["error_description"].(string); strings.Contains(desc, "service abuse mode") {
		return "", "", fmt.Errorf("账号已被封禁或凭据无效")
	}
	if resp.StatusCode != 200 {
		preview := string(raw)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return "", "", fmt.Errorf("刷新失败 %d: %s", resp.StatusCode, preview)
	}
	token, _ := result["access_token"].(string)
	if token == "" {
		return "", "", fmt.Errorf("响应中无 access_token")
	}
	newRT, _ := result["refresh_token"].(string)
	return token, newRT, nil
}

func persistOutlookRefreshToken(email, refresh string) {
	if email == "" || refresh == "" {
		return
	}
	storage.ModifyAccountsCached(func(accounts []map[string]interface{}) []map[string]interface{} {
		for i, acc := range accounts {
			if acc["email"] == email {
				accounts[i]["refreshToken"] = refresh
				break
			}
		}
		return accounts
	})
}

// buildXOAuth2 构建 XOAUTH2 认证字符串
func buildXOAuth2(email, accessToken string) string {
	auth := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", email, accessToken)
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// imapClient 简易 IMAP 客户端
type imapClient struct {
	conn   net.Conn
	reader *bufio.Reader
	tag    int
}

// newIMAPClient 连接 Outlook IMAP（优先走全局代理，代理被封端口时自动降级直连）
func newIMAPClient() (*imapClient, error) {
	const target = "outlook.office365.com:993"
	proxyURL := storage.GetProxy()
	rawConn, err := dialThroughProxy(proxyURL, "tcp", target, 15*time.Second)
	if err != nil && proxyURL != "" {
		log.Printf("[IMAP] 代理拨号失败，降级直连：%v", err)
		rawConn, err = dialThroughProxy("", "tcp", target, 15*time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("连接失败: %v", err)
	}
	tlsConfig := &tls.Config{ServerName: "outlook.office365.com"}
	conn := tls.Client(rawConn, tlsConfig)
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err == nil {
		err = conn.Handshake()
		conn.SetDeadline(time.Time{})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("TLS 握手失败: %v", err)
		}
	}

	c := &imapClient{conn: conn, reader: bufio.NewReader(conn), tag: 0}
	greeting, err := c.readLine()
	if err != nil {
		conn.Close()
		return nil, err
	}
	log.Printf("[IMAP] %s", greeting)
	return c, nil
}

func (c *imapClient) sendCommand(cmd string) (string, error) {
	c.tag++
	tagStr := fmt.Sprintf("A%03d", c.tag)
	line := fmt.Sprintf("%s %s\r\n", tagStr, cmd)
	_, err := c.conn.Write([]byte(line))
	if err != nil {
		return "", err
	}
	return tagStr, nil
}

func (c *imapClient) readLine() (string, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *imapClient) readUntilTag(tag string) ([]string, string, error) {
	var lines []string
	for {
		line, err := c.readLine()
		if err != nil {
			return lines, "", err
		}
		if strings.HasPrefix(line, tag+" ") {
			return lines, line, nil
		}
		lines = append(lines, line)
	}
}

func (c *imapClient) authenticate(email, accessToken string) error {
	xoauth2 := buildXOAuth2(email, accessToken)
	tag, err := c.sendCommand("AUTHENTICATE XOAUTH2 " + xoauth2)
	if err != nil {
		return err
	}
	_, result, err := c.readUntilTag(tag)
	if err != nil {
		return err
	}
	if !strings.Contains(result, "OK") {
		return fmt.Errorf("认证失败: %s", result)
	}
	log.Println("[IMAP] 认证成功")

	// 发送 NOOP 确保会话完全就绪（Outlook 有时认证后需要额外握手）
	for i := 0; i < 3; i++ {
		noopTag, err := c.sendCommand("NOOP")
		if err != nil {
			return err
		}
		_, noopResult, err := c.readUntilTag(noopTag)
		if err != nil {
			return err
		}
		if strings.Contains(noopResult, "OK") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Outlook Exchange 后端认证后需要额外时间建立 mailbox 连接，否则 SELECT 会返回 "not connected"
	time.Sleep(2 * time.Second)
	return nil
}

func (c *imapClient) selectInbox() (int, error) {
	tag, err := c.sendCommand("SELECT INBOX")
	if err != nil {
		return 0, err
	}
	lines, result, err := c.readUntilTag(tag)
	if err != nil {
		return 0, err
	}
	if strings.Contains(result, "OK") {
		total := 0
		for _, line := range lines {
			if strings.Contains(line, "EXISTS") {
				fmt.Sscanf(line, "* %d EXISTS", &total)
			}
		}
		return total, nil
	}
	// "not connected" 表示 Outlook 后端尚未就绪，同连接重试无效，由调用方重连后重试
	errMsg := strings.TrimSpace(result)
	if len(errMsg) > 80 {
		errMsg = errMsg[:80] + "..."
	}
	return 0, fmt.Errorf("SELECT 失败: %s", errMsg)
}

func (c *imapClient) close() {
	c.sendCommand("LOGOUT")
	c.conn.Close()
}

// fetchLatestBody 获取指定邮件的正文并解码
func (c *imapClient) fetchLatestBody(seq int) (string, error) {
	if seq <= 0 {
		return "", fmt.Errorf("无效的邮件序号")
	}
	tag, err := c.sendCommand(fmt.Sprintf("FETCH %d (BODY.PEEK[TEXT])", seq))
	if err != nil {
		return "", err
	}
	lines, result, err := c.readUntilTag(tag)
	if err != nil {
		return "", err
	}
	if !strings.Contains(result, "OK") {
		return "", fmt.Errorf("FETCH TEXT 失败: %s", result)
	}

	var rawLines []string
	inBody := false
	for _, line := range lines {
		if strings.Contains(line, "FETCH") {
			inBody = true
			continue
		}
		if line == ")" {
			continue
		}
		if inBody {
			rawLines = append(rawLines, line)
		}
	}

	raw := strings.Join(rawLines, "\n")

	// 尝试解码 MIME base64 内容
	parts := strings.Split(raw, "------=_Part_")
	var decoded string
	for _, part := range parts {
		if strings.Contains(part, "base64") {
			idx := strings.Index(part, "base64")
			content := part[idx+6:]
			b64 := strings.Map(func(r rune) rune {
				if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
					return -1
				}
				return r
			}, content)
			if data, err := base64.StdEncoding.DecodeString(b64); err == nil {
				decoded += string(data) + " "
			}
		}
	}
	if decoded != "" {
		return decoded, nil
	}

	// 整体 base64 解码
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, raw)
	if data, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return string(data), nil
	}

	return raw, nil
}

// WaitForOTP 通过 IMAP 轮询等待 AWS 验证码
func WaitForOTP(acc OutlookAccount, beforeCount, timeout, interval int) (string, error) {
	log.Printf("[Outlook IMAP] 等待验证码, 邮箱=%s, 发送前邮件数=%d", acc.Email, beforeCount)

	accessToken, err := RefreshOutlookToken(acc)
	if err != nil {
		return "", fmt.Errorf("刷新 Outlook Token 失败: %v", err)
	}

	codeRegex := regexp.MustCompile(`\b(\d{6})\b`)
	maxRetries := timeout / interval
	consecutiveSelectFail := 0
	maxConsecutiveSelectFail := 3 // 连续 3 次 SELECT 失败则提前放弃，避免单账号卡住整批
	for attempt := 1; attempt <= maxRetries; attempt++ {
		client, err := newIMAPClient()
		if err != nil {
			if attempt%5 == 0 {
				log.Printf("[Outlook IMAP] 连接失败: %v, 重试中...", err)
			}
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		if err := client.authenticate(acc.Email, accessToken); err != nil {
			client.close()
			accessToken, _ = RefreshOutlookToken(acc)
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		total, err := client.selectInbox()
		if err != nil {
			client.close()
			consecutiveSelectFail++
			if consecutiveSelectFail >= maxConsecutiveSelectFail {
				log.Printf("[Outlook IMAP] 邮箱 %s 连续 %d 次 SELECT 失败，放弃等待", acc.Email, consecutiveSelectFail)
				return "", fmt.Errorf("IMAP SELECT 连续失败 %d 次: %v", consecutiveSelectFail, err)
			}
			log.Printf("[Outlook IMAP] SELECT 失败 (%d/%d): %v", consecutiveSelectFail, maxConsecutiveSelectFail, err)
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}
		consecutiveSelectFail = 0 // 成功则重置

		if total <= beforeCount {
			client.close()
			if attempt%5 == 0 {
				log.Printf("[Outlook IMAP] [%d/%d] 暂无新邮件 (当前%d封)...", attempt, maxRetries, total)
			}
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		for i := total; i > beforeCount; i-- {
			body, err := client.fetchLatestBody(i)
			if err != nil {
				continue
			}
			code := extractCodeFromText(body, codeRegex)
			if code != "" {
				log.Printf("[Outlook IMAP] 获取到验证码: %s", code)
				client.close()
				return code, nil
			}
		}

		client.close()
		if attempt%5 == 0 {
			log.Printf("[Outlook IMAP] [%d/%d] 新邮件中未找到验证码...", attempt, maxRetries)
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
	return "", fmt.Errorf("等待验证码超时 (%ds)", timeout)
}

// GetInboxCount 获取收件箱当前邮件数量（带完整重连重试）
func GetInboxCount(acc OutlookAccount) (int, error) {
	accessToken, err := RefreshOutlookToken(acc)
	if err != nil {
		return 0, fmt.Errorf("刷新 Outlook Token 失败: %v", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1+attempt) * time.Second)
		}
		client, err := newIMAPClient()
		if err != nil {
			lastErr = fmt.Errorf("连接 IMAP 失败: %v", err)
			continue
		}
		if err := client.authenticate(acc.Email, accessToken); err != nil {
			client.close()
			lastErr = fmt.Errorf("IMAP 认证失败: %v", err)
			continue
		}
		total, err := client.selectInbox()
		if err != nil {
			client.close()
			lastErr = fmt.Errorf("选择收件箱失败: %v", err)
			log.Printf("[IMAP] GetInboxCount 失败，重连重试 %d/3...", attempt+1)
			continue
		}
		client.close()
		return total, nil
	}
	return 0, lastErr
}
