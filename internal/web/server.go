package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"reg_go/internal/email"
	"reg_go/internal/pool"
	"reg_go/internal/proxy"
	"reg_go/internal/reverseproxy"
	"reg_go/internal/storage"
	"reg_go/internal/task"
)

// Server Web 服务器
type Server struct {
	// 代理服务器
	proxyServer *reverseproxy.ProxyServer
	proxyMu     sync.Mutex
	poolMu      sync.Mutex

	// WebSocket 连接池
	wsClients   map[*websocket.Conn]bool
	wsClientsMu sync.RWMutex
	wsBroadcast chan []byte

	// WebSocket 升级器
	upgrader websocket.Upgrader
}

// NewServer 创建新的 Web 服务器
func NewServer() *Server {
	s := &Server{
		wsClients:   make(map[*websocket.Conn]bool),
		wsBroadcast: make(chan []byte, 256),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // 允许所有来源
			},
		},
	}

	dataDir := storage.GetDataDir()
	proxy.InitPool(dataDir)
	email.InitDomainPool(dataDir)

	log.SetOutput(&logWriter{})
	log.SetFlags(log.Ltime)

	// 启动广播协程
	go s.broadcastLoop()

	return s
}

type logWriter struct{}

func (w *logWriter) Write(p []byte) (int, error) {
	task.Manager.AppendLog(string(p))
	return os.Stderr.Write(p)
}

// broadcastLoop 广播循环
func (s *Server) broadcastLoop() {
	for {
		message := <-s.wsBroadcast
		s.wsClientsMu.RLock()
		for client := range s.wsClients {
			err := client.WriteMessage(websocket.TextMessage, message)
			if err != nil {
				log.Printf("WebSocket write error: %v", err)
				client.Close()
				delete(s.wsClients, client)
			}
		}
		s.wsClientsMu.RUnlock()
	}
}

// Broadcast 广播消息到所有客户端
func (s *Server) Broadcast(message []byte) {
	select {
	case s.wsBroadcast <- message:
	default:
		log.Println("Broadcast channel full, message dropped")
	}
}

// HandleWebSocket WebSocket 连接处理
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	s.wsClientsMu.Lock()
	s.wsClients[conn] = true
	s.wsClientsMu.Unlock()

	defer func() {
		s.wsClientsMu.Lock()
		delete(s.wsClients, conn)
		s.wsClientsMu.Unlock()
		conn.Close()
	}()

	// 发送欢迎消息
	welcome := map[string]string{
		"type":    "connected",
		"message": "Connected to Kiro Client",
	}
	if data, err := json.Marshal(welcome); err == nil {
		conn.WriteMessage(websocket.TextMessage, data)
	}

	// 保持连接
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// HandleRegisterStart 开始注册
func (s *Server) HandleRegisterStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req task.StartTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 启动任务
	result := task.StartTask(req)

	s.respondJSON(w, result)

	// 广播任务开始
	s.broadcastEvent("task_started", result)
}

// HandleRegisterStop 停止注册
func (s *Server) HandleRegisterStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	success := task.StopTask(true)
	result := map[string]interface{}{
		"success": success,
		"message": "Task stopped",
	}

	s.respondJSON(w, result)

	// 广播任务停止
	s.broadcastEvent("task_stopped", result)
}

// HandleRegisterStatus 获取注册状态
func (s *Server) HandleRegisterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.respondJSON(w, task.Manager.GetStatus())
}

// HandleLogs 获取任务日志
func (s *Server) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, task.Manager.GetLogs())
}

// HandlePoolsList 获取号池列表
func (s *Server) HandlePoolsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mgr := pool.GetManager(storage.GetDataDir())
	pools := mgr.List()
	res := make([]map[string]interface{}, len(pools))
	for i, p := range pools {
		res[i] = poolToMap(p)
	}
	s.respondJSON(w, res)
}

// HandlePoolsExport 导出号池
func (s *Server) HandlePoolsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PoolName string `json:"poolName"`
		Format   string `json:"format"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// TODO: 实现导出
	result := map[string]interface{}{
		"success": true,
		"message": "Export not implemented yet",
	}
	s.respondJSON(w, result)
}

// HandlePoolsRefresh 刷新号池
func (s *Server) HandlePoolsRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PoolID   string `json:"poolID"`
		PoolName string `json:"poolName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}

	mgr := pool.GetManager(storage.GetDataDir())
	p, err := resolvePool(mgr, req.PoolID, req.PoolName)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	s.poolMu.Lock()
	defer s.poolMu.Unlock()

	client := pool.NewKiroClient()
	successCount, failedCount := 0, 0
	for _, acc := range p.Accounts {
		if err := client.UpdateAccountInfo(acc); err != nil {
			failedCount++
		} else {
			successCount++
		}
	}

	if _, err = mgr.Update(p.ID, p.Name, p.Strategy, p.Accounts); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Failed to save: " + err.Error()})
		return
	}

	p, _ = mgr.Get(p.ID)
	m := poolToMap(p)
	m["refreshed"] = successCount
	m["failed"] = failedCount
	s.respondJSON(w, m)
}

func (s *Server) HandlePoolGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		s.respondJSON(w, map[string]interface{}{"error": "missing pool id"})
		return
	}
	p, err := pool.GetManager(storage.GetDataDir()).Get(id)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, poolToMap(p))
}

func (s *Server) HandlePoolRefreshAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PoolID string `json:"poolID"`
		Email  string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}
	if req.PoolID == "" || req.Email == "" {
		s.respondJSON(w, map[string]interface{}{"error": "poolID and email are required"})
		return
	}

	mgr := pool.GetManager(storage.GetDataDir())
	p, err := mgr.Get(req.PoolID)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	var target *pool.Account
	for _, acc := range p.Accounts {
		if acc.Email == req.Email {
			target = acc
			break
		}
	}
	if target == nil {
		s.respondJSON(w, map[string]interface{}{"error": "Account not found"})
		return
	}

	s.poolMu.Lock()
	defer s.poolMu.Unlock()

	client := pool.NewKiroClient()
	if err := client.UpdateAccountInfo(target); err != nil {
		s.respondJSON(w, map[string]interface{}{
			"error":  err.Error(),
			"status": target.HealthStatus,
		})
		return
	}
	if _, err = mgr.Update(p.ID, p.Name, p.Strategy, p.Accounts); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Failed to save: " + err.Error()})
		return
	}
	s.respondJSON(w, accountToMap(target))
}


func (s *Server) HandlePoolExportAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PoolID string `json:"poolID"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}
	mgr := pool.GetManager(storage.GetDataDir())
	p, err := mgr.Get(req.PoolID)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	if len(p.Accounts) == 0 {
		s.respondJSON(w, map[string]interface{}{"error": "暂无账号可导出"})
		return
	}
	data, err := pool.ExportAccountsJSON(p.Accounts)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, map[string]interface{}{
		"success":  true,
		"filename": "kiro-accounts-" + time.Now().Format("2006-01-02") + ".json",
		"content":  string(data),
		"count":    len(p.Accounts),
	})
}

func (s *Server) HandlePoolExportAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PoolID string `json:"poolID"`
		Email  string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}
	mgr := pool.GetManager(storage.GetDataDir())
	p, err := mgr.Get(req.PoolID)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	var acc *pool.Account
	for _, item := range p.Accounts {
		if item.Email == req.Email {
			acc = item
			break
		}
	}
	if acc == nil {
		s.respondJSON(w, map[string]interface{}{"error": "账号不存在"})
		return
	}
	data, err := pool.ExportAccountsJSON([]*pool.Account{acc})
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, map[string]interface{}{
		"success":  true,
		"filename": pool.SanitizeFilename(acc.Email) + ".json",
		"content":  string(data),
		"count":    1,
	})
}

func (s *Server) HandlePoolUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Strategy     string `json:"strategy"`
		AccountsJSON string `json:"accountsJSON"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}
	if req.ID == "" {
		s.respondJSON(w, map[string]interface{}{"error": "id is required"})
		return
	}
	var accounts []*pool.Account
	if req.AccountsJSON != "" {
		if err := json.Unmarshal([]byte(req.AccountsJSON), &accounts); err != nil {
			s.respondJSON(w, map[string]interface{}{"error": "Invalid accounts JSON: " + err.Error()})
			return
		}
	}
	p, err := pool.GetManager(storage.GetDataDir()).Update(req.ID, req.Name, pool.Strategy(req.Strategy), accounts)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, poolToMap(p))
}

func (s *Server) HandlePoolDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}
	if req.ID == "" {
		s.respondJSON(w, map[string]interface{}{"error": "id is required"})
		return
	}
	if err := pool.GetManager(storage.GetDataDir()).Delete(req.ID); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, map[string]interface{}{"success": true})
}

func (s *Server) HandlePoolImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": "Invalid request body"})
		return
	}
	accounts, err := pool.ParseAccountJSON([]byte(req.Data))
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	if len(accounts) == 0 {
		s.respondJSON(w, map[string]interface{}{"error": "未找到有效账号"})
		return
	}

	mgr := pool.GetManager(storage.GetDataDir())
	var defaultPool *pool.Pool
	for _, p := range mgr.List() {
		if p.Name == "默认号池" {
			defaultPool = p
			break
		}
	}
	if defaultPool == nil {
		created, err := mgr.Create("默认号池", pool.StrategySequential, accounts)
		if err != nil {
			s.respondJSON(w, map[string]interface{}{"error": err.Error()})
			return
		}
		s.respondJSON(w, poolToMap(created))
		return
	}
	added := 0
	for _, acc := range accounts {
		if err := mgr.AddAccountToPool(defaultPool.ID, acc); err == nil {
			added++
		}
	}
	if added == 0 {
		s.respondJSON(w, map[string]interface{}{"error": "所有账号已存在或验证失败"})
		return
	}
	updated, err := mgr.Get(defaultPool.ID)
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, poolToMap(updated))
}

func resolvePool(mgr *pool.Manager, id, name string) (*pool.Pool, error) {
	if id != "" {
		return mgr.Get(id)
	}
	if name != "" {
		for _, p := range mgr.List() {
			if p.Name == name {
				return p, nil
			}
		}
		return nil, fmt.Errorf("pool not found: %s", name)
	}
	return nil, fmt.Errorf("poolID or poolName is required")
}

func poolToMap(p *pool.Pool) map[string]interface{} {
	if p == nil {
		return map[string]interface{}{}
	}
	b, _ := json.Marshal(p)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

func accountToMap(a *pool.Account) map[string]interface{} {
	if a == nil {
		return map[string]interface{}{}
	}
	b, _ := json.Marshal(a)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

// HandleProxyList 列出代理池
func (s *Server) HandleProxyList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, proxy.List())
}

// HandleProxyBatchAdd 批量添加代理
func (s *Server) HandleProxyBatchAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URLs   []string `json:"urls"`
		Weight int      `json:"weight"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	result := proxy.BatchAdd(req.URLs, req.Weight)
	s.respondJSON(w, result)
	s.broadcastEvent("proxy_batch_added", result)
}

// HandleProxyTest 测试单条代理连通性
func (s *Server) HandleProxyTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	s.respondJSON(w, proxy.Detect(req.URL))
}

// HandleProxyAdd 新增一条代理
func (s *Server) HandleProxyAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Weight int    `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	e, err := proxy.Add(proxy.PoolEntry{Name: req.Name, URL: req.URL, Weight: req.Weight, Enabled: true})
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, e)
}

// HandleProxyUpdate 更新一条代理
func (s *Server) HandleProxyUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		URL     string `json:"url"`
		Weight  int    `json:"weight"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	e, err := proxy.Update(req.ID, proxy.PoolEntry{Name: req.Name, URL: req.URL, Weight: req.Weight, Enabled: req.Enabled})
	if err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, e)
}

// HandleProxyDelete 删除一条代理
func (s *Server) HandleProxyDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := proxy.Delete(req.ID); err != nil {
		s.respondJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	s.respondJSON(w, map[string]interface{}{"success": true})
}

// HandleOutlookList 列出 Outlook 账号
func (s *Server) HandleOutlookList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, email.GetOutlookAccounts())
}

// HandleOutlookAdd 添加 Outlook 账号（卡密文本）
func (s *Server) HandleOutlookAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	s.respondJSON(w, email.AddOutlookAccounts(req.Data))
}

// HandleOutlookDelete 删除单个 Outlook 账号
func (s *Server) HandleOutlookDelete(w http.ResponseWriter, r *http.Request) {
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
	s.respondJSON(w, email.DeleteOutlookAccount(req.Email))
}

// HandleOutlookClear 清空 Outlook 账号
func (s *Server) HandleOutlookClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, email.ClearOutlookAccounts())
}

// HandleOutlookClearRegistered 清除已注册 Outlook 账号
func (s *Server) HandleOutlookClearRegistered(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, email.ClearRegisteredOutlookAccounts())
}

// HandleHttpAPIList 列出 HTTP 邮箱
func (s *Server) HandleHttpAPIList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, email.GetHttpAPIAccounts())
}

// HandleHttpAPIAdd 添加 HTTP 邮箱（卡密文本）
func (s *Server) HandleHttpAPIAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	s.respondJSON(w, email.AddHttpAPIAccounts(req.Data))
}

// HandleHttpAPIDelete 删除单个 HTTP 邮箱
func (s *Server) HandleHttpAPIDelete(w http.ResponseWriter, r *http.Request) {
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
	s.respondJSON(w, email.DeleteHttpAPIAccount(req.Email))
}

// HandleHttpAPIClear 清空 HTTP 邮箱
func (s *Server) HandleHttpAPIClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, email.ClearHttpAPIAccounts())
}

// HandleHttpAPIClearRegistered 清除已注册 HTTP 邮箱
func (s *Server) HandleHttpAPIClearRegistered(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.respondJSON(w, email.ClearRegisteredHttpAPIAccounts())
}

// HandleGatewayStart 启动网关
func (s *Server) HandleGatewayStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Port int `json:"port"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()

	if s.proxyServer != nil {
		s.respondError(w, "Gateway already running", http.StatusBadRequest)
		return
	}

	// TODO: 创建正确的配置
	config := reverseproxy.Config{
		Port: req.Port,
	}
	proxyServer := reverseproxy.NewProxyServer(config, "")
	if err := proxyServer.Start(); err != nil {
		s.respondError(w, fmt.Sprintf("Failed to start gateway: %v", err), http.StatusInternalServerError)
		return
	}

	s.proxyServer = proxyServer

	result := map[string]interface{}{
		"success": true,
		"port":    req.Port,
		"message": "Gateway started",
	}

	s.respondJSON(w, result)
	s.broadcastEvent("gateway_started", result)
}

// HandleGatewayStop 停止网关
func (s *Server) HandleGatewayStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()

	if s.proxyServer == nil {
		s.respondError(w, "Gateway not running", http.StatusBadRequest)
		return
	}

	s.proxyServer.Stop()
	s.proxyServer = nil

	result := map[string]interface{}{
		"success": true,
		"message": "Gateway stopped",
	}

	s.respondJSON(w, result)
	s.broadcastEvent("gateway_stopped", result)
}

// HandleGatewayStatus 获取网关状态
func (s *Server) HandleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.proxyMu.Lock()
	running := s.proxyServer != nil
	s.proxyMu.Unlock()

	result := map[string]interface{}{
		"running": running,
	}

	s.respondJSON(w, result)
}

// HandleProxyBatchDelete 批量删除代理
func (s *Server) HandleProxyBatchDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	result := proxy.DeleteMany(req.IDs)
	s.respondJSON(w, result)
	s.broadcastEvent("proxy_batch_deleted", result)
}

// HandleProxyBatchWeight 批量设置权重
func (s *Server) HandleProxyBatchWeight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		IDs    []string `json:"ids"`
		Weight int      `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	result := proxy.SetWeightMany(req.IDs, req.Weight)
	s.respondJSON(w, result)
	s.broadcastEvent("proxy_batch_weight", result)
}

// respondJSON 返回 JSON 响应
func (s *Server) respondJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// respondError 返回错误响应
func (s *Server) respondError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

// broadcastEvent 广播事件
func (s *Server) broadcastEvent(eventType string, data interface{}) {
	message := map[string]interface{}{
		"type": eventType,
		"data": data,
	}

	if jsonData, err := json.Marshal(message); err == nil {
		s.Broadcast(jsonData)
	}
}
