package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reg_go/internal/core"
	"reg_go/internal/data"
	"reg_go/internal/email"
	"reg_go/internal/pool"
	"reg_go/internal/proxy"
	"reg_go/internal/reverseproxy"
	"reg_go/internal/storage"
	"reg_go/internal/subscription"
)

func gatewayAccountsPath() string {
	return filepath.Join(storage.GetDataDir(), "gateway_accounts.json")
}

func (s *Server) syncGatewayAccounts() error {
	mgr := pool.GetManager(storage.GetDataDir())
	seen := map[string]bool{}
	var list []reverseproxy.Account
	for _, p := range mgr.List() {
		for _, acc := range p.Accounts {
			if seen[acc.Email] {
				continue
			}
			seen[acc.Email] = true
			list = append(list, reverseproxy.Account{
				ClientID:        acc.ClientID,
				ClientSecret:    acc.ClientSecret,
				RefreshToken:    acc.RefreshToken,
				Region:          acc.Region,
				Email:           acc.Email,
				Subscription:    acc.Subscription,
				CreditLimit:     float64(acc.CreditLimit),
				CreditUsed:      float64(acc.CreditUsed),
				Provider:        acc.Provider,
				Time:            acc.Time,
				AvailableModels: acc.AvailableModels,
			})
		}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return os.WriteFile(gatewayAccountsPath(), b, 0600)
}

func (s *Server) ensureProxyServer() {
	if s.proxyServer != nil {
		return
	}
	port := s.gwPort
	if port <= 0 {
		port = 20130
	}
	s.proxyServer = reverseproxy.NewProxyServer(
		reverseproxy.Config{
			Port:             port,
			MaxAccountConc:   2,
			TotalConcurrency: 10,
			APIKey:           storage.GetGatewayAPIKey(),
			Proxy:            storage.GetProxy(),
		},
		gatewayAccountsPath(),
	)
}

func findOutputAccount(addr string) *subscription.Account {
	accounts, _ := data.LoadAccounts(storage.GetResultOutputDir())
	for _, acc := range accounts {
		if acc["email"] == addr {
			b, _ := json.Marshal(acc)
			var a subscription.Account
			json.Unmarshal(b, &a)
			return &a
		}
	}
	return nil
}

func addToDefaultPool(result map[string]interface{}) {
	at, _ := result["aws_token"].(map[string]interface{})
	if at == nil {
		return
	}
	refreshToken, _ := at["refreshToken"].(string)
	clientID, _ := result["client_id"].(string)
	clientSecret, _ := result["client_secret"].(string)
	emailAddr, _ := result["email"].(string)
	if refreshToken == "" || emailAddr == "" || clientID == "" || clientSecret == "" {
		return
	}
	acc := &pool.Account{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RefreshToken: refreshToken,
		Email:        emailAddr,
		Provider:     "BuilderId",
		Region:       "us-east-1",
		Time:         time.Now().Format("2006-01-02 15:04:05"),
	}
	if pw, _ := result["password"].(string); pw != "" {
		acc.Password = pw
	}
	mgr := pool.GetManager(storage.GetDataDir())
	for _, p := range mgr.List() {
		if p.Name == "默认号池" {
			_ = mgr.AddAccountToPool(p.ID, acc)
			return
		}
	}
	_, _ = mgr.Create("默认号池", pool.StrategyRoundRobin, []*pool.Account{acc})
}

// HandleDataDir GET 当前目录；POST {path} 设置
func (s *Server) HandleDataDir(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.respondJSON(w, map[string]interface{}{"path": storage.GetDataDir()})
	case http.MethodPost:
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.respondError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		p, err := storage.SetDataDirPath(req.Path)
		if err != nil {
			s.respondJSON(w, map[string]interface{}{"error": err.Error()})
			return
		}
		s.respondJSON(w, map[string]interface{}{"path": p})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) HandleDataDirReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, map[string]interface{}{"path": storage.ResetDataDirPath()})
}

func (s *Server) HandleOutputDir(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.respondJSON(w, map[string]interface{}{"path": storage.GetResultOutputDir()})
	case http.MethodPost:
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.respondError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		p, err := storage.SetResultOutputDir(req.Path)
		if err != nil {
			s.respondJSON(w, map[string]interface{}{"error": err.Error()})
			return
		}
		s.respondJSON(w, map[string]interface{}{"path": p})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) HandleOutputDirReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, map[string]interface{}{"path": storage.ResetResultOutputDir()})
}

func (s *Server) HandleGlobalProxy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.respondJSON(w, map[string]interface{}{"proxy": storage.GetProxy()})
	case http.MethodPost:
		var req struct {
			Proxy string `json:"proxy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.respondError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		p, err := storage.SetProxy(req.Proxy)
		if err != nil {
			s.respondJSON(w, map[string]interface{}{"error": err.Error()})
			return
		}
		s.proxyMu.Lock()
		if s.proxyServer != nil {
			s.proxyServer.Proxy = p
		}
		s.proxyMu.Unlock()
		s.respondJSON(w, map[string]interface{}{"proxy": p})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) HandleGlobalProxyReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	storage.ResetProxy()
	s.respondJSON(w, map[string]interface{}{"ok": true})
}

func (s *Server) HandleGlobalProxyDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Proxy string `json:"proxy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	info := proxy.Detect(req.Proxy)
	b, _ := json.Marshal(info)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	s.respondJSON(w, m)
}

func (s *Server) HandleDomainList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, email.ListDomainPool())
}

func (s *Server) HandleDomainEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Domain  string `json:"domain"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	email.SetDomainEnabled(req.Domain, req.Enabled)
	s.respondJSON(w, map[string]interface{}{"success": true})
}

func (s *Server) HandleDomainUnban(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	email.UnbanDomain(req.Domain)
	s.respondJSON(w, map[string]interface{}{"success": true})
}

func (s *Server) HandleOutputAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	accounts, err := data.LoadAccounts(storage.GetResultOutputDir())
	if err != nil {
		s.respondJSON(w, map[string]interface{}{
			"success": false, "accounts": []interface{}{}, "error": err.Error(),
			"outputDir": storage.GetResultOutputDir(),
		})
		return
	}
	s.respondJSON(w, map[string]interface{}{
		"success": true, "accounts": accounts, "outputDir": storage.GetResultOutputDir(),
	})
}

func (s *Server) HandleSubscriptionPlans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	acc := findOutputAccount(req.Email)
	if acc == nil {
		s.respondJSON(w, map[string]interface{}{"success": false, "error": "账号不存在"})
		return
	}
	token, err := subscription.RefreshAccessToken(*acc)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	plans, err := subscription.ListPlans(*acc, token)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	s.respondJSON(w, map[string]interface{}{"success": true, "plans": plans})
}

func (s *Server) HandleSubscriptionLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Email    string `json:"email"`
		PlanType string `json:"planType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	acc := findOutputAccount(req.Email)
	if acc == nil {
		s.respondJSON(w, map[string]interface{}{"success": false, "error": "账号不存在"})
		return
	}
	token, err := subscription.RefreshAccessToken(*acc)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	url, err := subscription.CreateSubscriptionLink(*acc, token, req.PlanType)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	s.respondJSON(w, map[string]interface{}{"success": true, "url": url})
}

func (s *Server) HandleManualRegisterStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.manualMu.Lock()
	if s.manualRun {
		s.manualMu.Unlock()
		s.respondJSON(w, map[string]interface{}{"error": "手动注册已在运行中"})
		return
	}
	s.manualRun = true
	s.manualMu.Unlock()

	go func() {
		defer func() {
			s.manualMu.Lock()
			s.manualRun = false
			s.manualMu.Unlock()
		}()
		log.Println("[Kiro][手动] === 手动注册开始 ===")
		cfg := core.NewConfig()
		cfg.Proxy = storage.GetProxy()
		reg := core.NewRegistrar(cfg)
		result := reg.ManualRegister()
		if result["status"] == "success" {
			if err := data.SaveKiroSuccess(result, storage.GetResultOutputDir()); err != nil {
				log.Printf("[Kiro][手动] 保存结果失败: %v", err)
			}
			addToDefaultPool(result)
			log.Printf("[Kiro][手动] === 手动注册成功，已入库 ===")
		} else {
			errMsg, _ := result["error"].(string)
			log.Printf("[Kiro][手动] === 手动注册失败: %s ===", errMsg)
		}
	}()
	s.respondJSON(w, map[string]interface{}{"ok": true})
}

func (s *Server) HandleManualRegisterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.manualMu.Lock()
	running := s.manualRun
	s.manualMu.Unlock()
	s.respondJSON(w, map[string]interface{}{"running": running})
}

func (s *Server) HandleGatewayConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cfg map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	s.ensureProxyServer()
	if v, ok := cfg["port"]; ok {
		if p, ok := v.(float64); ok && p > 0 {
			s.gwPort = int(p)
			s.proxyServer.Port = int(p)
		}
	}
	if v, ok := cfg["apiKey"]; ok {
		if key, ok := v.(string); ok && key != "" {
			_ = storage.SetGatewayAPIKey(key)
			s.proxyServer.APIKey = key
		}
	}
	s.respondJSON(w, map[string]interface{}{"success": true})
}

func (s *Server) HandleModelTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	s.respondJSON(w, s.testModelConnection(req.Model))
}

func (s *Server) testModelConnection(model string) map[string]interface{} {
	start := time.Now()
	s.proxyMu.Lock()
	s.ensureProxyServer()
	p := s.proxyServer
	s.proxyMu.Unlock()

	if p.AccountPool == nil || p.AccountPool.Count() == 0 {
		_ = s.syncGatewayAccounts()
		_ = p.ReloadAccounts()
	}
	if p.AccountPool == nil || p.AccountPool.Count() == 0 {
		return map[string]interface{}{"ok": false, "error": "无可用账号，请先添加号池"}
	}
	model = reverseproxy.SanitizeModel(model)
	acc := p.AccountPool.Acquire(model)
	if acc == nil {
		return map[string]interface{}{"ok": false, "error": "所有账号忙或冷却中"}
	}
	defer p.AccountPool.Release(acc)

	egress := acc.Proxy
	if egress == "" {
		egress = storage.GetProxy()
	}
	cred := reverseproxy.KiroCredentialFromAccount(acc)
	if err := reverseproxy.KiroEnsureAccessToken(cred, egress); err != nil {
		p.AccountPool.Suspend(acc)
		return map[string]interface{}{"ok": false, "error": "token 刷新失败: " + err.Error(), "account": acc.Email}
	}
	acc.SetAccessToken(cred.AccessToken)
	chatReq := &reverseproxy.ChatRequest{
		Model:     model,
		MaxTokens: 16,
		Messages:  []reverseproxy.ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	tr, err := reverseproxy.KiroTranslateChatRequest(chatReq, model, cred, nil)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": "翻译失败: " + err.Error(), "account": acc.Email}
	}
	resp, err := reverseproxy.KiroSendChat(tr, cred, egress)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error(), "account": acc.Email}
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		b := string(body)
		if len(b) > 200 {
			b = b[:200]
		}
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("上游 HTTP %d: %s", resp.StatusCode, b), "account": acc.Email}
	}
	chunks := make(chan reverseproxy.SSEEvent, 64)
	go reverseproxy.ParseEventStream(resp.Body, chunks, model)
	content := ""
	for ch := range chunks {
		if len(ch.Choices) > 0 {
			content += ch.Choices[0].Delta.Content
		}
	}
	if strings.TrimSpace(content) == "" {
		return map[string]interface{}{"ok": false, "error": "上游 200 但返回空内容", "account": acc.Email}
	}
	return map[string]interface{}{
		"ok": true, "latencyMs": time.Since(start).Milliseconds(),
		"account": acc.Email, "preview": content,
	}
}
