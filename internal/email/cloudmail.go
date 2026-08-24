package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CloudMailConfig cloud-mail 配置（参考 https://github.com/cloud-mail/cloud-mail）
type CloudMailConfig struct {
	Name     string   `json:"name"`     // 配置名（用户自定义）
	URL      string   `json:"url"`      // 基础 URL，例：https://mail.example.com
	Email    string   `json:"email"`    // 管理员邮箱
	Password string   `json:"password"` // 管理员密码
	Domains  []string `json:"domains"`  // 服务器允许的域名列表（与 env.domain 对齐）
}

// tokenSlot 同一台 Cloud-Mail（URL+管理员）共享一份 token。
// 服务端 genToken 会覆盖旧 token，多任务各自登录会把别人踢下线。
type tokenSlot struct {
	mu    sync.Mutex
	token string
}

var (
	tokenCache   = map[string]*tokenSlot{}
	tokenCacheMu sync.Mutex
)

func tokenCacheKey(cfg CloudMailConfig) string {
	return strings.ToLower(strings.TrimRight(cfg.URL, "/")) + "\x00" + strings.ToLower(strings.TrimSpace(cfg.Email))
}

func slotFor(cfg CloudMailConfig) *tokenSlot {
	key := tokenCacheKey(cfg)
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()
	if s := tokenCache[key]; s != nil {
		return s
	}
	s := &tokenSlot{}
	tokenCache[key] = s
	return s
}

func resetCloudMailTokenCache() {
	tokenCacheMu.Lock()
	tokenCache = map[string]*tokenSlot{}
	tokenCacheMu.Unlock()
}

// CloudMailClient cloud-mail HTTP 客户端
type CloudMailClient struct {
	config CloudMailConfig
	client *http.Client
	slot   *tokenSlot
}

// CloudMailMessage 邮件信息（与 /api/public/emailList 响应对齐）
type CloudMailMessage struct {
	EmailID    int64  `json:"emailId"`
	SendEmail  string `json:"sendEmail"`
	SendName   string `json:"sendName"`
	Subject    string `json:"subject"`
	ToEmail    string `json:"toEmail"`
	ToName     string `json:"toName"`
	CreateTime string `json:"createTime"`
	Type       int    `json:"type"`
	Content    string `json:"content"`
	Text       string `json:"text"`
	IsDel      int    `json:"isDel"`
}

// cloudMailResp 通用响应包装
type cloudMailResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// NewCloudMailClient 创建客户端
func NewCloudMailClient(config CloudMailConfig) *CloudMailClient {
	return &CloudMailClient{
		config: config,
		client: &http.Client{Timeout: 20 * time.Second},
		slot:   slotFor(config),
	}
}

func (c *CloudMailClient) currentToken() string {
	c.slot.mu.Lock()
	defer c.slot.mu.Unlock()
	return c.slot.token
}

// GenToken 通过 /api/public/genToken 获取并写入共享缓存
func (c *CloudMailClient) GenToken() error {
	c.slot.mu.Lock()
	defer c.slot.mu.Unlock()
	return c.genTokenLocked()
}

func (c *CloudMailClient) genTokenLocked() error {
	body, _ := json.Marshal(map[string]string{
		"email":    c.config.Email,
		"password": c.config.Password,
	})
	url := strings.TrimRight(c.config.URL, "/") + "/api/public/genToken"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("genToken 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("genToken HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var wrap cloudMailResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return fmt.Errorf("genToken 解析失败: %w, body=%s", err, string(respBody))
	}
	if wrap.Code != 200 {
		return fmt.Errorf("genToken 业务错误 [%d]: %s", wrap.Code, wrap.Message)
	}

	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(wrap.Data, &data); err != nil || data.Token == "" {
		return fmt.Errorf("genToken 未返回 token: %s", string(wrap.Data))
	}

	c.slot.token = data.Token
	return nil
}

// ensureToken 已有共享 token 就复用，没有才登录一次
func (c *CloudMailClient) ensureToken() error {
	c.slot.mu.Lock()
	defer c.slot.mu.Unlock()
	if c.slot.token != "" {
		return nil
	}
	return c.genTokenLocked()
}

func unauthorized(httpStatus int, body []byte) bool {
	if httpStatus == 401 {
		return true
	}
	var wrap cloudMailResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return false
	}
	return wrap.Code == 401
}

// doAuthorized 调用需要鉴权的接口。HTTP/业务 401 时刷新共享 token 再试一次。
func (c *CloudMailClient) doAuthorized(path string, payload interface{}) ([]byte, error) {
	if err := c.ensureToken(); err != nil {
		return nil, err
	}

	doOnce := func() (int, string, []byte, error) {
		body, _ := json.Marshal(payload)
		url := strings.TrimRight(c.config.URL, "/") + path
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			return 0, "", nil, err
		}
		tok := c.currentToken()
		req.Header.Set("Authorization", tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.client.Do(req)
		if err != nil {
			return 0, tok, nil, err
		}
		defer resp.Body.Close()
		bs, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, tok, bs, nil
	}

	status, usedTok, respBody, err := doOnce()
	if err != nil {
		return nil, fmt.Errorf("%s 请求失败: %w", path, err)
	}
	if unauthorized(status, respBody) {
		c.slot.mu.Lock()
		if c.slot.token == usedTok || c.slot.token == "" {
			log.Printf("[CloudMail] token 失效，重新获取 (%s)", c.config.Email)
			if err := c.genTokenLocked(); err != nil {
				c.slot.mu.Unlock()
				return nil, fmt.Errorf("token 失效后重新登录失败: %w", err)
			}
		}
		c.slot.mu.Unlock()
		status, _, respBody, err = doOnce()
		if err != nil {
			return nil, fmt.Errorf("%s 重试失败: %w", path, err)
		}
		if unauthorized(status, respBody) {
			return nil, fmt.Errorf("%s token 验证失败", path)
		}
	}
	if status != 200 {
		return nil, fmt.Errorf("%s HTTP %d: %s", path, status, string(respBody))
	}
	return respBody, nil
}

// AddUser 在 cloud-mail 上创建用户（邮箱）
func (c *CloudMailClient) AddUser(emailAddr, password string) error {
	user := map[string]interface{}{"email": emailAddr}
	if password != "" {
		user["password"] = password
	}
	payload := map[string]interface{}{
		"list": []map[string]interface{}{user},
	}
	respBody, err := c.doAuthorized("/api/public/addUser", payload)
	if err != nil {
		return err
	}
	var wrap cloudMailResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return fmt.Errorf("addUser 解析失败: %w, body=%s", err, string(respBody))
	}
	if wrap.Code != 200 {
		return fmt.Errorf("addUser 业务错误 [%d]: %s", wrap.Code, wrap.Message)
	}
	return nil
}

// EmailList 获取邮件列表
func (c *CloudMailClient) EmailList(toEmail string, num int) ([]CloudMailMessage, error) {
	if num <= 0 {
		num = 20
	}
	payload := map[string]interface{}{
		"timeSort": "desc",
		"size":     num,
		"num":      1,
		"type":     0,
		"isDel":    0,
	}
	if toEmail != "" {
		payload["toEmail"] = toEmail
	}
	respBody, err := c.doAuthorized("/api/public/emailList", payload)
	if err != nil {
		return nil, err
	}
	var wrap cloudMailResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("emailList 解析失败: %w, body=%s", err, string(respBody))
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("emailList 业务错误 [%d]: %s", wrap.Code, wrap.Message)
	}
	var list []CloudMailMessage
	if len(wrap.Data) > 0 && string(wrap.Data) != "null" {
		if err := json.Unmarshal(wrap.Data, &list); err != nil {
			return nil, fmt.Errorf("emailList data 解析失败: %w, body=%s", err, string(wrap.Data))
		}
	}
	return list, nil
}

// GetWebsiteConfig 调用 /api/setting/websiteConfig 拉取服务器域名列表
// 该接口在 cloud-mail 的 security exclude 列表中，无需鉴权；
// 但若服务器开启 loginDomain 隐私开关且未带 token，会返回空列表。
// 因此实现：先不带 token 拉一次，空则带 token 重试一次（用 genToken 拿的 UUID 当 Bearer）。
func (c *CloudMailClient) GetWebsiteConfig() ([]string, error) {
	url := strings.TrimRight(c.config.URL, "/") + "/api/setting/websiteConfig"

	fetch := func(withToken bool) ([]string, error) {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		if withToken {
			if tok := c.currentToken(); tok != "" {
				req.Header.Set("Authorization", tok)
			}
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("websiteConfig 请求失败: %w", err)
		}
		defer resp.Body.Close()
		bs, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("websiteConfig HTTP %d: %s", resp.StatusCode, string(bs))
		}
		var wrap cloudMailResp
		if err := json.Unmarshal(bs, &wrap); err != nil {
			return nil, fmt.Errorf("websiteConfig 解析失败: %w", err)
		}
		if wrap.Code != 200 {
			return nil, fmt.Errorf("websiteConfig 业务错误 [%d]: %s", wrap.Code, wrap.Message)
		}
		var data struct {
			DomainList []string `json:"domainList"`
		}
		if err := json.Unmarshal(wrap.Data, &data); err != nil {
			return nil, fmt.Errorf("websiteConfig data 解析失败: %w", err)
		}
		out := make([]string, 0, len(data.DomainList))
		for _, d := range data.DomainList {
			d = strings.TrimPrefix(strings.TrimSpace(d), "@")
			if d != "" {
				out = append(out, d)
			}
		}
		return out, nil
	}

	domains, err := fetch(false)
	if err != nil {
		return nil, err
	}
	if len(domains) > 0 {
		return domains, nil
	}
	// 空返回：尝试带 token 重试一次
	if c.currentToken() != "" {
		if d2, err2 := fetch(true); err2 == nil && len(d2) > 0 {
			return d2, nil
		}
	}
	return domains, nil
}

// TestConnection 测试：genToken + websiteConfig + 轻量 emailList。
// 返回拉取到的域名列表（可能为空，表示服务器开启了 loginDomain 隐私开关）。
func (c *CloudMailClient) TestConnection() ([]string, error) {
	if err := c.ensureToken(); err != nil {
		return nil, err
	}
	domains, err := c.GetWebsiteConfig()
	if err != nil {
		return nil, err
	}
	if _, err := c.EmailList("", 1); err != nil {
		return domains, err
	}
	return domains, nil
}

// CloudMailProvider 实现 EmailProvider 接口
type CloudMailProvider struct {
	client            *CloudMailClient
	address           string
	initialMaxEmailID int64
}

// NewCloudMailProvider 创建一个 cloud-mail 邮箱（执行 addUser）
func NewCloudMailProvider(config CloudMailConfig, name, domain string) (*CloudMailProvider, error) {
	client := NewCloudMailClient(config)

	if domain == "" {
		if len(config.Domains) > 0 {
			domain = config.Domains[0]
		} else {
			// 配置中没填域名：尝试从服务器拉
			domains, err := client.GetWebsiteConfig()
			if err != nil {
				return nil, fmt.Errorf("cloud-mail 配置 %s 未提供域名且自动拉取失败: %w", config.Name, err)
			}
			if len(domains) == 0 {
				return nil, fmt.Errorf("cloud-mail 配置 %s 未提供域名且服务器未返回域名（可能开启了 loginDomain 隐私开关）", config.Name)
			}
			domain = domains[0]
		}
	}
	addr := name + "@" + domain

	if err := client.AddUser(addr, ""); err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	var baseline int64
	msgs, err := client.EmailList(addr, 5)
	if err != nil {
		log.Printf("[CloudMail] 获取初始邮件失败: %v，基线设为 0", err)
	} else {
		for _, m := range msgs {
			if m.EmailID > baseline {
				baseline = m.EmailID
			}
		}
	}
	log.Printf("[CloudMail] 邮箱创建完成: %s，初始最大 emailId: %d", addr, baseline)

	return &CloudMailProvider{
		client:            client,
		address:           addr,
		initialMaxEmailID: baseline,
	}, nil
}

// GetAddress 返回邮箱地址
func (p *CloudMailProvider) GetAddress() string { return p.address }

// WaitForCode 轮询等待 6 位数字验证码
func (p *CloudMailProvider) WaitForCode(timeout, interval int) (string, error) {
	if interval <= 0 {
		interval = 3
	}
	maxRetries := timeout / interval
	if maxRetries < 1 {
		maxRetries = 1
	}
	codeRegex := regexp.MustCompile(`\b(\d{6})\b`)
	log.Printf("[CloudMail] 开始等待验证码 %s，基线 emailId=%d", p.address, p.initialMaxEmailID)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		msgs, err := p.client.EmailList(p.address, 20)
		if err != nil {
			if attempt%5 == 0 {
				log.Printf("[CloudMail] 获取邮件失败: %v，重试中...", err)
			}
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		for _, m := range msgs {
			if m.EmailID <= p.initialMaxEmailID {
				continue
			}
			if code := extractCodeFromText(m.Text, codeRegex); code != "" {
				log.Printf("[CloudMail] 从新邮件中获取到验证码: %s", code)
				return code, nil
			}
			if code := extractCodeFromText(m.Content, codeRegex); code != "" {
				log.Printf("[CloudMail] 从新邮件中获取到验证码: %s", code)
				return code, nil
			}
			if code := extractCodeFromText(m.Subject, codeRegex); code != "" {
				log.Printf("[CloudMail] 从新邮件中获取到验证码: %s", code)
				return code, nil
			}
		}

		if attempt%5 == 0 {
			log.Printf("[CloudMail] [%d/%d] 暂无新邮件...", attempt, maxRetries)
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}

	return "", fmt.Errorf("等待验证码超时 (%ds)", timeout)
}
