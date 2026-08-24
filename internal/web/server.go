package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"reg_go/internal/reverseproxy"
	"reg_go/internal/task"
)

// Server Web 服务器
type Server struct {
	// 代理服务器
	proxyServer *reverseproxy.ProxyServer
	proxyMu     sync.Mutex

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

	// 启动广播协程
	go s.broadcastLoop()

	return s
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

	// 简单状态响应
	status := map[string]interface{}{
		"running":   false,
		"completed": 0,
		"failed":    0,
		"total":     0,
	}

	s.respondJSON(w, status)
}

// HandlePoolsList 获取号池列表
func (s *Server) HandlePoolsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: 实现号池列表
	pools := []map[string]interface{}{
		{"name": "default", "count": 0},
	}
	s.respondJSON(w, pools)
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
		PoolName string `json:"poolName"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// TODO: 实现刷新
	result := map[string]interface{}{
		"success": true,
		"message": "Refresh not implemented yet",
	}
	s.respondJSON(w, result)
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

