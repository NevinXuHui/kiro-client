# Kiro Client Web 版本指南

## 🌐 项目概述

**Kiro Client Web** 是 Kiro Client 的纯 Web 应用版本，基于前后端分离架构，无需依赖 Wails 或任何桌面环境。

- **分支**: web
- **仓库**: https://github.com/NevinXuHui/kiro-client/tree/web
- **类型**: 前后端分离 Web 应用
- **技术栈**: Go HTTP Server + RESTful API + WebSocket

## 🎯 核心特性

### 1. 纯 Web 架构
- 无需 WebView 或 Electron
- 标准 HTTP/WebSocket 协议
- 可独立部署到任意服务器
- 支持远程访问

### 2. RESTful API
- 完整的 REST 接口
- JSON 数据格式
- CORS 支持
- 标准 HTTP 状态码

### 3. 实时通信
- WebSocket 双向通信
- 实时日志推送
- 任务进度更新
- 事件广播

### 4. 前后端分离
- 前端：原生 HTML/CSS/JS
- 后端：Go HTTP Server
- 静态文件服务
- API 接口隔离

## 🏗️ 架构设计

### 整体架构

```
┌─────────────────────────────────────────────┐
│              浏览器客户端                    │
│  ┌─────────────────────────────────────┐    │
│  │      前端 (HTML/CSS/JS)             │    │
│  │  - 静态页面                          │    │
│  │  - API 客户端 (api.js)              │    │
│  │  - WebSocket 客户端                 │    │
│  └─────────────────────────────────────┘    │
└─────────────────┬───────────────────────────┘
                  │
                  │ HTTP/WebSocket
                  ↓
┌─────────────────────────────────────────────┐
│           Go HTTP Server                    │
│  ┌─────────────────────────────────────┐    │
│  │  静态文件服务 (/)                   │    │
│  │  - frontend/*                       │    │
│  └─────────────────────────────────────┘    │
│  ┌─────────────────────────────────────┐    │
│  │  RESTful API (/api/*)               │    │
│  │  - 注册管理                          │    │
│  │  - 号池操作                          │    │
│  │  - 网关控制                          │    │
│  └─────────────────────────────────────┘    │
│  ┌─────────────────────────────────────┐    │
│  │  WebSocket (/api/ws)                │    │
│  │  - 实时日志                          │    │
│  │  - 进度推送                          │    │
│  │  - 事件广播                          │    │
│  └─────────────────────────────────────┘    │
└─────────────────┬───────────────────────────┘
                  │
                  ↓
┌─────────────────────────────────────────────┐
│             业务逻辑层                       │
│  - internal/core    注册核心                │
│  - internal/task    任务协调                │
│  - internal/pool    号池管理                │
│  - internal/email   邮箱源                  │
│  - internal/proxy   代理池                  │
└─────────────────────────────────────────────┘
```

### 通信流程

```
用户操作 → API 请求 → HTTP Server → 业务逻辑 → 返回结果
                                      ↓
                              WebSocket 推送
                                      ↓
                              前端实时更新
```

## 📁 项目结构

```
kiro-client/
├── cmd/
│   └── web/
│       └── main.go              # Web 服务器入口
├── internal/
│   ├── web/
│   │   └── server.go            # HTTP 服务器实现
│   ├── core/                    # 注册核心逻辑
│   ├── task/                    # 任务协调
│   ├── pool/                    # 号池管理
│   ├── email/                   # 邮箱源
│   ├── proxy/                   # 代理池
│   └── reverseproxy/            # 网关代理
├── frontend/                    # 前端文件
│   ├── index.html
│   ├── js/
│   │   ├── api.js              # API 客户端（新增）
│   │   ├── app.js              # 应用逻辑
│   │   └── ...
│   └── css/
├── kiro-web                     # 编译后的可执行文件
└── go.mod
```

## 🚀 快速开始

### 编译

```bash
# 克隆仓库
git clone https://github.com/NevinXuHui/kiro-client.git
cd kiro-client

# 切换到 web 分支
git checkout web

# 安装依赖
go mod tidy

# 编译
go build -o kiro-web ./cmd/web/
```

### 运行

```bash
# 默认启动 (localhost:8080)
./kiro-web

# 指定端口
./kiro-web -port 3000

# 指定主机和端口
./kiro-web -host 0.0.0.0 -port 8080

# 后台运行
nohup ./kiro-web > server.log 2>&1 &
```

### 访问

```bash
# 浏览器访问
http://localhost:8080

# API 测试
curl http://localhost:8080/api/register/status
```

## 🔌 API 接口文档

### 基础信息

- **Base URL**: `http://localhost:8080/api`
- **Content-Type**: `application/json`
- **CORS**: 已启用

### 注册相关 API

#### 1. 开始注册

```http
POST /api/register/start
Content-Type: application/json

{
  "count": 10,
  "concurrency": 3,
  "delay": 5,
  "emailProvider": "cloudmail",
  "cloudmailDomains": ["example.com"],
  "cloudmailConfigs": {...},
  "outputPath": "accounts.json"
}
```

**响应**:
```json
{
  "success": true,
  "message": "任务已启动",
  "taskId": "task_123"
}
```

#### 2. 停止注册

```http
POST /api/register/stop
Content-Type: application/json

{}
```

**响应**:
```json
{
  "success": true,
  "message": "Task stopped"
}
```

#### 3. 获取注册状态

```http
GET /api/register/status
```

**响应**:
```json
{
  "running": true,
  "completed": 5,
  "failed": 1,
  "total": 10
}
```

### 号池相关 API

#### 1. 获取号池列表

```http
GET /api/pools/list
```

**响应**:
```json
[
  {
    "name": "default",
    "count": 50,
    "healthy": 45,
    "unhealthy": 5
  }
]
```

#### 2. 导出号池

```http
POST /api/pools/export
Content-Type: application/json

{
  "poolName": "default",
  "format": "json"
}
```

**响应**:
```json
{
  "success": true,
  "data": "...",
  "filename": "pool_default_20260824.json"
}
```

#### 3. 刷新号池

```http
POST /api/pools/refresh
Content-Type: application/json

{
  "poolName": "default"
}
```

**响应**:
```json
{
  "success": true,
  "refreshed": 10,
  "failed": 2
}
```

### 网关相关 API

#### 1. 启动网关

```http
POST /api/gateway/start
Content-Type: application/json

{
  "port": 8081
}
```

**响应**:
```json
{
  "success": true,
  "port": 8081,
  "message": "Gateway started"
}
```

#### 2. 停止网关

```http
POST /api/gateway/stop
Content-Type: application/json

{}
```

**响应**:
```json
{
  "success": true,
  "message": "Gateway stopped"
}
```

#### 3. 获取网关状态

```http
GET /api/gateway/status
```

**响应**:
```json
{
  "running": true,
  "port": 8081,
  "requests": 1234
}
```

### WebSocket 接口

#### 连接

```javascript
const ws = new WebSocket('ws://localhost:8080/api/ws');

ws.onopen = () => {
  console.log('Connected');
};

ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log('Message:', data);
};
```

#### 消息格式

**服务器推送**:
```json
{
  "type": "task_started",
  "data": {
    "taskId": "task_123",
    "count": 10
  }
}
```

**日志推送**:
```json
{
  "type": "log",
  "data": {
    "level": "info",
    "message": "注册成功",
    "email": "user@example.com",
    "timestamp": "2026-08-24T21:30:00Z"
  }
}
```

**进度推送**:
```json
{
  "type": "progress",
  "data": {
    "completed": 5,
    "failed": 1,
    "total": 10,
    "percentage": 50.0
  }
}
```

## 💻 前端集成

### API 客户端使用

前端已包含 `api.js`，提供了完整的 API 封装：

```javascript
// 自动初始化
const api = window.kiroAPI;

// 自动连接 WebSocket
api.connectWebSocket((message) => {
  console.log('WebSocket:', message);
});

// 注册 WebSocket 事件回调
api.on('task_started', (data) => {
  console.log('Task started:', data);
});

api.on('log', (data) => {
  console.log('Log:', data);
});

api.on('progress', (data) => {
  updateProgressBar(data.percentage);
});

// 调用 API
async function startRegister() {
  try {
    const result = await api.startRegister({
      count: 10,
      concurrency: 3,
      delay: 5,
      emailProvider: 'cloudmail'
    });
    console.log('Success:', result);
  } catch (error) {
    console.error('Error:', error);
  }
}

// 获取状态
async function checkStatus() {
  const status = await api.getRegisterStatus();
  console.log('Status:', status);
}
```

### 替换 Wails 调用

原有的 Wails 调用需要替换为 API 调用：

```javascript
// 之前 (Wails)
window.go.main.App.StartRegister(config).then(result => {
  console.log(result);
});

// 现在 (Web)
window.kiroAPI.startRegister(config).then(result => {
  console.log(result);
});
```

## 🔧 配置和部署

### 环境变量

```bash
# 服务器配置
export KIRO_HOST=0.0.0.0
export KIRO_PORT=8080

# 数据目录
export KIRO_DATA_DIR=/var/lib/kiro

# 日志级别
export KIRO_LOG_LEVEL=info
```

### Systemd 服务

```ini
[Unit]
Description=Kiro Client Web Server
After=network.target

[Service]
Type=simple
User=kiro
WorkingDirectory=/opt/kiro-client
ExecStart=/opt/kiro-client/kiro-web -host 0.0.0.0 -port 8080
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

```bash
# 启动服务
sudo systemctl start kiro-web
sudo systemctl enable kiro-web

# 查看状态
sudo systemctl status kiro-web

# 查看日志
sudo journalctl -u kiro-web -f
```

### Nginx 反向代理

```nginx
server {
    listen 80;
    server_name kiro.example.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /api/ws {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
    }
}
```

### Docker 部署

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o kiro-web ./cmd/web/

FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/

COPY --from=builder /app/kiro-web .
COPY --from=builder /app/frontend ./frontend

EXPOSE 8080

CMD ["./kiro-web", "-host", "0.0.0.0", "-port", "8080"]
```

```bash
# 构建镜像
docker build -t kiro-web .

# 运行容器
docker run -d \
  -p 8080:8080 \
  -v /var/lib/kiro:/root/data \
  --name kiro-web \
  kiro-web

# 查看日志
docker logs -f kiro-web
```

## 🎯 版本对比

| 特性 | GUI (Wails) | TUI | Web |
|------|-------------|-----|-----|
| **界面** | 桌面 GUI | 终端 TUI | 浏览器 Web |
| **依赖** | WebView | 终端 | 浏览器 |
| **部署** | 本地桌面 | SSH/服务器 | 远程服务器 |
| **访问** | 本地 | 本地/SSH | 远程/多用户 |
| **资源** | 高 | 低 | 中 |
| **并发** | 单用户 | 单用户 | 多用户 |
| **适用** | 桌面用户 | 运维/脚本 | 团队/远程 |

## 📊 性能建议

### 服务器配置

**最低配置**:
- CPU: 1 核心
- 内存: 512 MB
- 磁盘: 100 MB

**推荐配置**:
- CPU: 2+ 核心
- 内存: 2+ GB
- 磁盘: 1+ GB

### 并发处理

- 默认支持 100+ 并发连接
- WebSocket 自动负载均衡
- 合理配置并发注册数

### 安全建议

1. **使用 HTTPS**: 配置 SSL 证书
2. **添加认证**: JWT 或 Basic Auth
3. **限流保护**: 防止 DDoS
4. **CORS 策略**: 限制跨域访问
5. **日志审计**: 记录操作日志

## 🔍 故障排查

### 常见问题

**无法访问**:
```bash
# 检查服务状态
ps aux | grep kiro-web

# 检查端口占用
netstat -tuln | grep 8080

# 查看日志
tail -f server.log
```

**API 错误**:
```bash
# 测试 API
curl -v http://localhost:8080/api/register/status

# 检查 CORS
curl -H "Origin: http://example.com" \
     -H "Access-Control-Request-Method: POST" \
     -X OPTIONS http://localhost:8080/api/register/start
```

**WebSocket 断开**:
- 检查网络连接
- 查看浏览器控制台
- 确认服务器正常运行
- 检查防火墙规则

## 📚 相关文档

- [项目总结](PROJECT_FINAL_SUMMARY.md)
- [TUI 版本](TUI_VERSION_SUMMARY.md)
- [并发延时配置](docs/CONCURRENT_DELAY.md)
- [SSO 快速开始](docs/SSO_QUICKSTART.md)

## 🎉 总结

Web 版本提供了：

✅ 完整的 RESTful API  
✅ 实时 WebSocket 通信  
✅ 前后端分离架构  
✅ 远程访问能力  
✅ 多用户支持  
✅ 易于部署和扩展  

适用于：
- 团队协作
- 远程管理
- 服务器部署
- CI/CD 集成

---

**更新日期**: 2026-08-24  
**版本**: Web v1.0  
**分支**: web  

🌐 **享受使用 Kiro Client Web！**
