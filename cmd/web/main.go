package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"reg_go/internal/web"
)

func main() {
	// 命令行参数
	port := flag.String("port", "9702", "HTTP server port")
	host := flag.String("host", "0.0.0.0", "HTTP server host")
	flag.Parse()

	// 创建 Web 服务器
	server := web.NewServer()

	// 设置路由
	mux := http.NewServeMux()

	// 静态文件服务（HTML/JS/CSS 禁用缓存，避免浏览器继续用旧脚本）
	fs := http.FileServer(http.Dir("frontend"))
	mux.Handle("/", noCacheStatic(fs))

	// API 路由
	mux.HandleFunc("/api/register/start", server.HandleRegisterStart)
	mux.HandleFunc("/api/register/stop", server.HandleRegisterStop)
	mux.HandleFunc("/api/register/status", server.HandleRegisterStatus)
	mux.HandleFunc("/api/logs", server.HandleLogs)
	mux.HandleFunc("/api/pools/list", server.HandlePoolsList)
	mux.HandleFunc("/api/pools/get", server.HandlePoolGet)
	mux.HandleFunc("/api/pools/export", server.HandlePoolsExport)
	mux.HandleFunc("/api/pools/refresh", server.HandlePoolsRefresh)
	mux.HandleFunc("/api/pools/refresh-account", server.HandlePoolRefreshAccount)
	mux.HandleFunc("/api/pools/export-accounts", server.HandlePoolExportAccounts)
	mux.HandleFunc("/api/pools/export-account", server.HandlePoolExportAccount)
	mux.HandleFunc("/api/pools/update", server.HandlePoolUpdate)
	mux.HandleFunc("/api/pools/delete", server.HandlePoolDelete)
	mux.HandleFunc("/api/pools/import", server.HandlePoolImport)
	mux.HandleFunc("/api/proxy/list", server.HandleProxyList)
	mux.HandleFunc("/api/proxy/batch-add", server.HandleProxyBatchAdd)
	mux.HandleFunc("/api/proxy/test", server.HandleProxyTest)
	mux.HandleFunc("/api/proxy/add", server.HandleProxyAdd)
	mux.HandleFunc("/api/proxy/update", server.HandleProxyUpdate)
	mux.HandleFunc("/api/proxy/delete", server.HandleProxyDelete)
	mux.HandleFunc("/api/proxy/batch-delete", server.HandleProxyBatchDelete)
	mux.HandleFunc("/api/proxy/batch-weight", server.HandleProxyBatchWeight)
	mux.HandleFunc("/api/outlook/list", server.HandleOutlookList)
	mux.HandleFunc("/api/outlook/add", server.HandleOutlookAdd)
	mux.HandleFunc("/api/outlook/delete", server.HandleOutlookDelete)
	mux.HandleFunc("/api/outlook/clear", server.HandleOutlookClear)
	mux.HandleFunc("/api/outlook/clear-registered", server.HandleOutlookClearRegistered)
	mux.HandleFunc("/api/httpapi/list", server.HandleHttpAPIList)
	mux.HandleFunc("/api/httpapi/add", server.HandleHttpAPIAdd)
	mux.HandleFunc("/api/httpapi/delete", server.HandleHttpAPIDelete)
	mux.HandleFunc("/api/httpapi/clear", server.HandleHttpAPIClear)
	mux.HandleFunc("/api/httpapi/clear-registered", server.HandleHttpAPIClearRegistered)
	mux.HandleFunc("/api/gateway/start", server.HandleGatewayStart)
	mux.HandleFunc("/api/gateway/stop", server.HandleGatewayStop)
	mux.HandleFunc("/api/gateway/status", server.HandleGatewayStatus)
	mux.HandleFunc("/api/ws", server.HandleWebSocket)

	// CORS 中间件
	handler := corsMiddleware(mux)

	// HTTP 服务器配置
	addr := fmt.Sprintf("%s:%s", *host, *port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	// 启动服务器
	go func() {
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Printf("🌐 Kiro Client Web Server")
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Printf("📍 Address: http://%s", addr)
		log.Printf("🚀 Server started successfully!")
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down server...")

	// 停止服务器
	if err := httpServer.Close(); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("✅ Server exited")
}

// corsMiddleware 添加 CORS 支持
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// noCacheStatic 对 HTML/JS/CSS 禁用缓存，确保前端改动能立刻生效。
func noCacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		next.ServeHTTP(w, r)
	})
}
