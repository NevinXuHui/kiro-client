package reverseproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

type Config struct {
	Port             int    `json:"port"`
	MaxAccountConc   int    `json:"maxAccountConc"`
	TotalConcurrency int    `json:"totalConcurrency"`
	APIKey           string `json:"apiKey"` // Bearer token required from clients
	Proxy            string `json:"proxy"`  // global egress proxy; per-account proxy wins
}

// QuotaRefreshFunc 刷新账号额度的回调（9router getKiroUsage 同款语义）。
// 参数：accessToken, region, proxy → (creditUsed, creditLimit, resetAt, error)
type QuotaRefreshFunc func(accessToken, region, proxy string) (used, limit float64, resetAt string, err error)

// egressProxy 解析账号的出口代理：账号自带代理优先，否则用全局设置。
// kiro.dev 按出口 IP 限流，直连住宅 IP 很快就会 429，因此全局代理必须生效。
func (p *ProxyServer) egressProxy(acc *Account) string {
	if acc != nil && acc.Proxy != "" {
		return acc.Proxy
	}
	return p.Proxy
}

type ProxyServer struct {
	Config
	AccountPool *AccountPool
	Stats       *RequestStats

	srv          *http.Server
	running      int32
	accountsPath string
	mu           sync.Mutex
}

func NewProxyServer(cfg Config, accountsPath string) *ProxyServer {
	return &ProxyServer{
		Config:       cfg,
		Stats:        NewRequestStats(),
		accountsPath: accountsPath,
	}
}

func (p *ProxyServer) Start() error {
	if !atomic.CompareAndSwapInt32(&p.running, 0, 1) {
		return fmt.Errorf("already running")
	}

	if p.AccountPool == nil {
		p.AccountPool = NewAccountPool(p.accountsPath, p.MaxAccountConc, p.TotalConcurrency, func(acc *Account) (string, error) {
			// 三分支刷新（external_idp / IDC / social，10s 并发去重）
			cred := KiroCredentialFromAccount(acc)
			if err := KiroEnsureAccessToken(cred, p.egressProxy(acc)); err != nil {
				return "", err
			}
			acc.SetAccessToken(cred.AccessToken)
			if cred.ExpiresAtMs > 0 {
				acc.SetTokenExpiresAt(cred.ExpiresAtMs * 1e6)
			}
			return cred.AccessToken, nil
		})
		// 注册额度刷新回调（9router 同款：GetUsageLimits → updateQuotaForAccount）
		p.AccountPool.SetQuotaFunc(NewQuotaRefreshFunc())
		if err := p.AccountPool.Load(); err != nil {
			atomic.StoreInt32(&p.running, 0)
			return fmt.Errorf("load accounts: %w", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", p.handleChat)
	mux.HandleFunc("/v1/models", p.handleModels)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/stats", p.handleStats)

	addr := fmt.Sprintf("127.0.0.1:%d", p.Port)
	p.srv = &http.Server{Addr: addr, Handler: mux}

	go func() {
		if err := p.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			atomic.StoreInt32(&p.running, 0)
		}
	}()

	go p.autoRefreshLoop()
	go p.autoQuotaRefreshLoop()

	return nil
}

// autoQuotaRefreshLoop 后台自动刷新额度：每 5min 调 GetUsageLimits 更新所有账号的 CreditUsed/CreditLimit。
// 9router 同款：getKiroUsage 定时任务，但 9router 只在仪表盘按需调用；此处主动刷新，
// 确保 Acquire 能根据最新额度做无感轮询，避免等到上游 429 才切换。
func (p *ProxyServer) autoQuotaRefreshLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if atomic.LoadInt32(&p.running) == 0 {
			return
		}
		if p.AccountPool != nil {
			p.AccountPool.RefreshQuota()
		}
	}
}

// autoRefreshLoop 后台自动刷新：每 60s 预热临近过期（<5min）的 accessToken。
// 9router 本无此定时器（请求路径惰性刷新，见 handleChat），此循环仅为避免
// 冷却恢复后的首次请求撞上过期刷新延迟，不触 chat 配额，对外行为不可见。
func (p *ProxyServer) autoRefreshLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if atomic.LoadInt32(&p.running) == 0 {
			return
		}
		now := time.Now().UnixNano()
		for _, acc := range p.AccountPool.Accounts() {
			exp := acc.TokenExpiresAt()
			if exp == 0 || now < exp-refreshLeadSec*1e9 {
				continue
			}
			// 三分支刷新（external_idp / IDC / social，10s 并发去重）
			cred := KiroCredentialFromAccount(acc)
			if err := KiroEnsureAccessToken(cred, p.egressProxy(acc)); err != nil {
				continue // 失败静默，下次请求路径再试（与 9router 惰性语义一致）
			}
			acc.SetAccessToken(cred.AccessToken)
			if cred.ExpiresAtMs > 0 {
				acc.SetTokenExpiresAt(cred.ExpiresAtMs * 1e6)
			}
		}
	}
}

func (p *ProxyServer) Stop() error {
	if !atomic.CompareAndSwapInt32(&p.running, 1, 0) {
		return nil
	}
	if p.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.srv.Shutdown(ctx)
}

func (p *ProxyServer) IsRunning() bool {
	return atomic.LoadInt32(&p.running) == 1
}

func (p *ProxyServer) ReloadAccounts() error {
	if p.AccountPool == nil {
		return fmt.Errorf("pool not initialized")
	}
	return p.AccountPool.Load()
}

func (p *ProxyServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Bearer API key check so third-party OpenAI-compatible clients can be
	// pointed at the gateway. Empty server key means auth disabled.
	if p.APIKey != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != p.APIKey {
			http.Error(w, `{"error":"invalid api key"}`, 401)
			return
		}
	}

	// 熔断保护：新链路连续失败熔断期直接短路 503（避免爆刷上游）
	if KiroCircuitTripped() {
		http.Error(w, `{"error":"kiro provider circuit open, retry later"}`, 503)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), 400)
		return
	}

	if p.AccountPool.Count() == 0 {
		http.Error(w, `{"error":"no accounts available"}`, 503)
		return
	}

	model := SanitizeModel(req.Model)

	start := time.Now()
	var success bool
	var tokensIn, tokensOut int

	// 账号轮换：上游失败经 ERROR_RULES 分类上模型锁（429 指数退避 / 401-404
	// 固定 2min / 文本规则优先），换下一账号——9router accountFallback 同款。
	// 全程走新版链路（kiro_provider.go）：三分支刷新 → 1:1 翻译 → 原地重试发送。
	maxTry := p.AccountPool.Count()
	var acc *Account
	var resp *fhttp.Response
	var lastErr error
	lastStatus := 0
	for try := 0; try < maxTry; try++ {
		if acc != nil {
			p.AccountPool.Release(acc)
			acc = nil
		}
		acc = p.AccountPool.Acquire(model)
		if acc == nil {
			break // 全锁/全忙
		}

		// 凭证桥接（AuthMethod 由 Provider 标签推导，注册机产物默认 builder-id）
		cred := KiroCredentialFromAccount(acc)

		// 1. 令牌确保：缺失或临近过期（<5min 缓冲）→ 三分支刷新（external_idp /
		// IDC / social，10s 并发去重）。成功回写账号缓存。
		if err := KiroEnsureAccessToken(cred, p.egressProxy(acc)); err != nil {
			acc.cooldownUntil = time.Now().Add(2 * time.Minute).UnixNano() // 账号级 2min 锁
			KiroApplyGatewayError(acc, model, 401, "token refresh failed: "+err.Error(), time.Now())
			KiroProviderFailure()
			lastErr = err
			lastStatus = 401
			continue
		}
		acc.SetAccessToken(cred.AccessToken)
		if cred.ExpiresAtMs > 0 {
			acc.SetTokenExpiresAt(cred.ExpiresAtMs * 1e6)
		}

		// 2. 翻译（OpenAI → Kiro 1:1：session replay、<instructions>、工具结构化、
		// 遗留 Bug ①②③ 原样复刻；api_key/idc/external_idp 绝不注入默认 ARN）
		tr, err := KiroTranslateChatRequest(&req, model, cred, headerMap(r.Header))
		if err != nil {
			lastErr = fmt.Errorf("translate: %w", err)
			lastStatus = 400
			continue
		}

		// 3. 发送（per-URL 原地重试 502/503/504 + 429 主机切换 + tokentype 头 +
		// api_key/external_idp/idc 主机重排）
		resp, lastErr = KiroSendChat(tr, cred, p.egressProxy(acc))
		if lastErr != nil {
			log.Printf("[handleChat] KiroSendChat error: %v (account=%s)", lastErr, acc.Email)
			KiroProviderFailure()
			KiroApplyGatewayError(acc, model, 0, lastErr.Error(), time.Now())
			lastStatus = 0
			continue
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("[handleChat] upstream non-200: HTTP %d body=%s (account=%s)", resp.StatusCode, truncate(string(body), 200), acc.Email)
			errText := fmt.Sprintf("upstream HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
			KiroApplyGatewayError(acc, model, resp.StatusCode, errText, time.Now())
			KiroProviderFailure()
			lastErr = fmt.Errorf("%s", errText)
			lastStatus = resp.StatusCode
			continue
		}

		KiroProviderSuccess()
		KiroApplyGatewaySuccess(acc, model)
		break
	}

	defer func() {
		if acc != nil {
			p.AccountPool.Release(acc)
		}
		p.Stats.RecordRequest(time.Since(start), success, tokensIn, tokensOut)
	}()

	if lastErr != nil || acc == nil || resp == nil {
		// 9router unavailableResponse：全锁时回 503 + Retry-After（最早锁到期）；
		// 最近失败为 429 则透传 429，客户端可见明确限流状态。
		code := 503
		if lastStatus != 0 {
			code = lastStatus
		}
		if retryAfter := p.AccountPool.EarliestModelLock(model); retryAfter > 0 {
			secs := (retryAfter - time.Now().UnixNano() + 999999999) / 1e9
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
			http.Error(w, fmt.Sprintf(`{"error":"all accounts rate limited (retry after %ds)"}`, secs), code)
			return
		}
		if lastErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":"api call failed: %v"}`, lastErr), code)
			return
		}
		http.Error(w, `{"error":"all accounts busy"}`, 503)
		return
	}
	defer resp.Body.Close()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(200)

		flusher, _ := w.(http.Flusher)

		roleSent := false
		chunks := make(chan SSEEvent, 64)
		go parseEventStream(resp.Body, chunks, model)

		// Keepalive ticker: send comment every 15s to prevent proxy timeout
		keepalive := time.NewTicker(15 * time.Second)
		defer keepalive.Stop()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for chunk := range chunks {
				if !roleSent && len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
					chunk.Choices[0].Delta.Role = "assistant"
					roleSent = true
				}
				keepalive.Stop() // stop keepalive once real data flows
				writeSSE(w, chunk)
				if flusher != nil {
					flusher.Flush()
				}

				if chunk.Usage != nil {
					tokensIn = chunk.Usage.PromptTokens
					tokensOut = chunk.Usage.CompletionTokens
				}
			}
		}()

		// Keepalive loop
		for {
			select {
			case <-done:
				goto streamDone
			case <-keepalive.C:
				fmt.Fprintf(w, ": ka\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}

	streamDone:
		writeSSEDone(w)
		if flusher != nil {
			flusher.Flush()
		}
	} else {
		content := ""
		chunks := make(chan SSEEvent, 64)
		go parseEventStream(resp.Body, chunks, model)

		for chunk := range chunks {
			if len(chunk.Choices) > 0 {
				content += chunk.Choices[0].Delta.Content
			}
			if chunk.Usage != nil {
				tokensIn = chunk.Usage.PromptTokens
				tokensOut = chunk.Usage.CompletionTokens
			}
		}

		resp := map[string]interface{}{
			"id":      newSSEID(),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{{
				"index":         0,
				"message":       map[string]interface{}{"role": "assistant", "content": content},
				"finish_reason": "stop",
			}},
			"usage": map[string]interface{}{
				"prompt_tokens":     tokensIn,
				"completion_tokens": tokensOut,
				"total_tokens":      tokensIn + tokensOut,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	// 跟踪 token 用量，累加到 CreditUsed（9router 同款：metricsEvent 累加）
	if tokensIn+tokensOut > 0 && acc != nil {
		acc.CreditUsed += float64(tokensIn + tokensOut)
		if acc.CreditLimit > 0 && acc.CreditUsed >= acc.CreditLimit {
			p.AccountPool.MarkQuotaExhausted(acc, "")
		}
	}

	success = true
}

func (p *ProxyServer) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Collect models from pool accounts (populated by health check)
	modelSet := make(map[string]bool)
	p.AccountPool.ForEach(func(acc *Account) {
		for _, m := range acc.AvailableModels {
			modelSet[m] = true
		}
	})

	// Build model list
	var data []map[string]interface{}
	if len(modelSet) > 0 {
		for id := range modelSet {
			data = append(data, map[string]interface{}{
				"id":       id,
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "kiro",
			})
		}
	} else {
		// Fallback to static list
		data = []map[string]interface{}{
			{"id": "claude-opus-4.8", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "claude-opus-4.7", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "claude-opus-4.5", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "claude-sonnet-5", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "claude-sonnet-4.5", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "claude-haiku-4.5", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "deepseek-3.2", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "qwen3-coder-next", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "glm-5", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
			{"id": "MiniMax-M2.5", "object": "model", "created": time.Now().Unix(), "owned_by": "kiro"},
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

func (p *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"running":  p.IsRunning(),
		"accounts": p.AccountPool.Count(),
		"active":   p.AccountPool.ActiveCount(),
	})
}

func (p *ProxyServer) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p.Stats.Snapshot())
}
