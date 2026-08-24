# Kiro Client - Web Version

🌐 **纯 Web 应用版本** - 无需桌面环境，浏览器即可使用

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Branch](https://img.shields.io/badge/Branch-web-blue)](https://github.com/NevinXuHui/kiro-client/tree/web)

## 🎯 项目简介

Kiro Client Web 版本是基于 **前后端分离** 架构的纯 Web 应用，提供完整的 RESTful API 和 WebSocket 实时通信。

### 核心特性

- ✅ **前后端分离**: 标准的 REST API 设计
- ✅ **实时通信**: WebSocket 双向通信
- ✅ **远程访问**: 支持部署到服务器
- ✅ **多用户**: 支持多人同时使用
- ✅ **易部署**: 单一可执行文件
- ✅ **跨平台**: Linux/macOS/Windows

## 🚀 快速开始

### 1. 编译

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

### 2. 运行

```bash
# 默认启动 (localhost:8080)
./kiro-web

# 指定端口
./kiro-web -port 3000

# 绑定所有网卡（允许远程访问）
./kiro-web -host 0.0.0.0 -port 8080
```

### 3. 访问

```bash
# 浏览器访问
http://localhost:8080

# API 测试
curl http://localhost:8080/api/register/status
```

## 📺 界面预览

### 主界面
```
┌─────────────────────────────────────────┐
│  🔧 Kiro Client                         │
├─────────────────────────────────────────┤
│  📝 注册机      批量注册 AWS Builder ID │
│  📊 号池管理    查看和管理已注册账号    │
│  🌐 网关控制    启动/停止本地网关       │
│  ⚙️ 设置       配置代理池、域名池等     │
└─────────────────────────────────────────┘
```

### 注册界面
```
┌─────────────────────────────────────────┐
│  邮箱源: [Outlook] [CloudMail] [HttpAPI] │
│                                         │
│  数量:    [10]                          │
│  并发:    [3]                           │
│  延时:    [5]                           │
│                                         │
│  [开始注册]                             │
│                                         │
│  进度: ████████░░░░░░░░ 50%             │
│  成功: 5  失败: 0  总数: 10             │
└─────────────────────────────────────────┘
```

## 🔌 API 文档

### 注册相关

#### 开始注册
```http
POST /api/register/start
Content-Type: application/json

{
  "count": 10,
  "concurrency": 3,
  "delay": 5,
  "emailProvider": "cloudmail"
}
```

#### 停止注册
```http
POST /api/register/stop
```

#### 获取状态
```http
GET /api/register/status
```

响应:
```json
{
  "running": true,
  "completed": 5,
  "failed": 1,
  "total": 10
}
```

### 号池相关

#### 获取列表
```http
GET /api/pools/list
```

#### 导出号池
```http
POST /api/pools/export
Content-Type: application/json

{
  "poolName": "default",
  "format": "json"
}
```

### 网关相关

#### 启动网关
```http
POST /api/gateway/start
Content-Type: application/json

{
  "port": 8081
}
```

#### 停止网关
```http
POST /api/gateway/stop
```

### WebSocket

```javascript
// 连接
const ws = new WebSocket('ws://localhost:8080/api/ws');

// 接收消息
ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log(data);
  // { type: "task_started", data: {...} }
  // { type: "log", data: {...} }
  // { type: "progress", data: {...} }
};
```

## 💻 前端集成

### 使用 API 客户端

前端已包含完整的 API 封装 (`frontend/js/api.js`)：

```javascript
// API 客户端已自动初始化
const api = window.kiroAPI;

// 开始注册
await api.startRegister({
  count: 10,
  concurrency: 3,
  delay: 5,
  emailProvider: 'cloudmail'
});

// 获取状态
const status = await api.getRegisterStatus();

// WebSocket 事件监听
api.on('task_started', (data) => {
  console.log('Task started:', data);
});

api.on('progress', (data) => {
  updateProgressBar(data.percentage);
});
```

### 替换 Wails 调用

从 Wails 迁移到 Web 版本：

```javascript
// 之前 (Wails)
window.go.main.App.StartRegister(config);

// 现在 (Web)
window.kiroAPI.startRegister(config);
```

## 🏗️ 架构设计

```
┌───────────────────────────────┐
│      浏览器客户端              │
│  ┌─────────────────────────┐  │
│  │  HTML/CSS/JS            │  │
│  │  - api.js (API 客户端)  │  │
│  │  - WebSocket 客户端     │  │
│  └─────────────────────────┘  │
└───────────┬───────────────────┘
            │ HTTP/WebSocket
            ↓
┌───────────────────────────────┐
│     Go HTTP Server            │
│  ┌─────────────────────────┐  │
│  │  静态文件服务 (/)       │  │
│  └─────────────────────────┘  │
│  ┌─────────────────────────┐  │
│  │  RESTful API (/api/*)   │  │
│  └─────────────────────────┘  │
│  ┌─────────────────────────┐  │
│  │  WebSocket (/api/ws)    │  │
│  └─────────────────────────┘  │
└───────────┬───────────────────┘
            ↓
┌───────────────────────────────┐
│      业务逻辑层                │
│  - 注册核心                   │
│  - 任务协调                   │
│  - 号池管理                   │
│  - 邮箱源                     │
└───────────────────────────────┘
```

## 🔧 部署指南

### 后台运行

```bash
# nohup 方式
nohup ./kiro-web > server.log 2>&1 &

# screen 方式
screen -S kiro
./kiro-web
# Ctrl+A, D 分离会话
```

### Systemd 服务

创建 `/etc/systemd/system/kiro-web.service`:

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

[Install]
WantedBy=multi-user.target
```

```bash
# 启动服务
sudo systemctl start kiro-web
sudo systemctl enable kiro-web

# 查看状态
sudo systemctl status kiro-web
```

### Nginx 反向代理

```nginx
server {
    listen 80;
    server_name kiro.example.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }

    location /api/ws {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

### Docker 部署

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o kiro-web ./cmd/web/

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/kiro-web .
COPY --from=builder /app/frontend ./frontend
EXPOSE 8080
CMD ["./kiro-web", "-host", "0.0.0.0", "-port", "8080"]
```

```bash
# 构建
docker build -t kiro-web .

# 运行
docker run -d -p 8080:8080 --name kiro-web kiro-web
```

## 📊 版本对比

| 特性 | GUI (Wails) | TUI | **Web** |
|------|-------------|-----|---------|
| **界面** | 桌面窗口 | 终端 | **浏览器** |
| **依赖** | WebView | 终端 | **无** |
| **部署** | 本地 | SSH | **远程** |
| **并发** | 单用户 | 单用户 | **多用户** |
| **访问** | 本地 | 本地/SSH | **随处** |
| **资源** | 高 | 低 | **中** |

## 🎯 适用场景

### ✅ 最适合

- 🌐 **团队协作**: 多人共享使用
- 🚀 **远程管理**: 服务器部署
- 📊 **集中管理**: 统一号池管理
- 🔧 **CI/CD**: 集成到自动化流程
- 📱 **移动访问**: 手机/平板浏览器

### ⚠️ 不适合

- 桌面端单用户（推荐使用 GUI 版本）
- 无网络环境（推荐使用 TUI 版本）
- 需要系统托盘（推荐使用 GUI 版本）

## 📚 完整文档

- 📖 [Web 版本完整指南](WEB_VERSION_GUIDE.md) - API 文档、部署指南
- 📝 [项目总结](PROJECT_FINAL_SUMMARY.md) - 项目架构、功能说明
- 🖥️ [TUI 版本](TUI_VERSION_SUMMARY.md) - 终端界面版本
- ⚙️ [并发延时配置](docs/CONCURRENT_DELAY.md) - 性能优化
- 🚀 [SSO 快速开始](docs/SSO_QUICKSTART.md) - AWS SSO 配置

## 🔍 故障排查

### 无法访问

```bash
# 检查服务状态
ps aux | grep kiro-web

# 检查端口
netstat -tuln | grep 8080

# 查看日志
tail -f server.log
```

### API 错误

```bash
# 测试 API
curl -v http://localhost:8080/api/register/status

# 检查 CORS
curl -H "Origin: http://example.com" \
     -X OPTIONS http://localhost:8080/api/register/start
```

### WebSocket 断开

- 检查网络连接
- 查看浏览器控制台
- 确认服务器正常运行
- 检查防火墙规则

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 许可证

MIT License

## 🔗 相关链接

- **GitHub**: https://github.com/NevinXuHui/kiro-client
- **Web 分支**: https://github.com/NevinXuHui/kiro-client/tree/web
- **TUI 分支**: https://github.com/NevinXuHui/kiro-client/tree/tui-version
- **主分支**: https://github.com/NevinXuHui/kiro-client/tree/master

---

**更新日期**: 2026-08-24  
**版本**: Web v1.0  
**分支**: web  

🌐 **享受使用 Kiro Client Web！**
