package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"reg_go/internal/web"
)

func main() {
	// 命令行参数
	port := flag.String("port", "8080", "HTTP server port")
	host := flag.String("host", "localhost", "HTTP server host")
	flag.Parse()

	// 创建 Web 服务器
	server := web.NewServer()

	// 设置路由
	mux := http.NewServeMux()

	// 静态文件服务
	fs := http.FileServer(http.Dir("frontend"))
	mux.Handle("/", fs)

	// API 路由
	mux.HandleFunc("/api/register/start", server.HandleRegisterStart)
	mux.HandleFunc("/api/register/stop", server.HandleRegisterStop)
	mux.HandleFunc("/api/register/status", server.HandleRegisterStatus)
	mux.HandleFunc("/api/pools/list", server.HandlePoolsList)
	mux.HandleFunc("/api/pools/export", server.HandlePoolsExport)
	mux.HandleFunc("/api/pools/refresh", server.HandlePoolsRefresh)
	mux.HandleFunc("/api/proxy/batch-add", server.HandleProxyBatchAdd)
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
		WriteTimeout: 15 * time.Second,
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
