# ⚡ Kiro Client 快速开始

> 5 分钟快速上手 Kiro Client 三个版本

## 🎯 选择你的版本

### 我应该用哪个版本？

```
🖥️  GUI 版本 - 如果你是：
   ✓ 桌面用户，喜欢图形界面
   ✓ 需要系统托盘方便操作
   ✓ 本地个人使用
   → 使用 main 分支

📺 TUI 版本 - 如果你是：
   ✓ 服务器管理员
   ✓ 通过 SSH 远程操作
   ✓ 喜欢终端界面
   → 使用 tui-version 分支

🌐 Web 版本 - 如果你是：
   ✓ 需要团队协作
   ✓ 需要远程访问
   ✓ 需要 API 集成
   → 使用 web 分支
```

## 🚀 三步快速开始

### GUI 版本（桌面应用）

```bash
# 1. 切换分支
git checkout main

# 2. 一键编译
./build.sh
# 按提示选择安装到 /Applications

# 3. 一键运行
./run.sh
# 或从 Launchpad 启动
```

**使用**:
- 在 Dock 栏找到 Kiro Client 图标
- 点击打开使用图形界面
- 所有功能都在菜单中

### TUI 版本（终端界面）

```bash
# 1. 切换分支
git checkout tui-version

# 2. 一键编译
./build.sh

# 3. 一键运行
./run.sh
```

**使用**:
- 使用 ↑/↓ 或 k/j 导航
- Enter 选择菜单项
- ESC 返回上级
- q 退出程序

### Web 版本（浏览器访问）

```bash
# 1. 切换分支
git checkout web

# 2. 一键编译
./build.sh

# 3. 一键运行
./run.sh
# 或指定主机和端口
./run.sh -h 0.0.0.0 -p 8080
```

**使用**:
- 浏览器访问: http://localhost:8080
- 使用 Web 界面操作
- 支持多用户同时访问

## 📝 首次使用指南

### 1. 安装依赖

#### 所有版本都需要：

```bash
# Go 1.22+
brew install go
# 或从 https://golang.org/dl/ 下载

# Git
brew install git
```

#### GUI 版本额外需要：

```bash
# Wails
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

#### Python SSO（可选，用于 AWS 授权）：

```bash
cd scripts/sso
./setup.sh
```

### 2. 克隆项目

```bash
git clone https://github.com/NevinXuHui/kiro-client.git
cd kiro-client
```

### 3. 选择版本并运行

按照上面的"三步快速开始"选择你需要的版本即可。

## 🎮 基本使用

### GUI 版本

1. **注册机**: 批量注册 AWS Builder ID
   - 选择邮箱源（Outlook/CloudMail/HttpAPI）
   - 设置数量、并发、延时
   - 点击"开始注册"

2. **号池**: 查看和管理已注册账号
   - 查看账号列表
   - 导出账号数据
   - 刷新账号状态

3. **网关**: 启动本地 Kiro 网关
   - 配置端口和 API Key
   - 启动/停止网关
   - 查看网关状态

### TUI 版本

1. 启动后进入主菜单
2. 使用 ↑/↓ 选择功能
3. 按 Enter 进入
4. 在注册机界面：
   - 按 ←/→ 切换邮箱源
   - Tab 切换输入框
   - Enter 开始注册
5. ESC 返回主菜单

### Web 版本

1. 浏览器访问 http://localhost:8080
2. 使用 Web 界面操作（和 GUI 类似）
3. 支持多用户同时访问
4. 可以通过 API 调用功能

## 🔧 常见问题

### Q1: build.sh 提示权限被拒绝？

```bash
chmod +x build.sh run.sh
```

### Q2: GUI 版本提示 Wails 未安装？

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### Q3: Web 版本端口被占用？

```bash
# 使用其他端口
./run.sh -p 3000

# 或停止占用端口的进程
lsof -ti:8080 | xargs kill -9
```

### Q4: TUI 版本显示异常？

```bash
# 调整终端窗口大小（建议 80x24 以上）
# 或使用更现代的终端（iTerm2、Warp 等）
```

### Q5: 如何切换版本？

```bash
# 切换到其他分支即可
git checkout main          # GUI 版本
git checkout tui-version   # TUI 版本
git checkout web           # Web 版本
```

## 📚 下一步

### GUI 版本
- 阅读 [README.md](README.md)
- 配置 Python SSO: [docs/SSO_QUICKSTART.md](docs/SSO_QUICKSTART.md)
- 优化并发配置: [docs/CONCURRENT_DELAY.md](docs/CONCURRENT_DELAY.md)

### TUI 版本
- 阅读 [TUI_USAGE_GUIDE.md](TUI_USAGE_GUIDE.md)
- 查看快捷键: [TUI_VERSION_SUMMARY.md](TUI_VERSION_SUMMARY.md)

### Web 版本
- 阅读 [WEB_VERSION_GUIDE.md](WEB_VERSION_GUIDE.md)
- API 文档: [README_WEB.md](README_WEB.md)
- 部署指南: [WEB_MIGRATION_COMPLETE.md](WEB_MIGRATION_COMPLETE.md)

## 💡 使用技巧

### 技巧 1: 创建快捷别名

```bash
# 添加到 ~/.zshrc 或 ~/.bashrc
alias kiro-gui="cd /path/to/kiro-client && git checkout main && ./build.sh && ./run.sh"
alias kiro-tui="cd /path/to/kiro-client && git checkout tui-version && ./build.sh && ./run.sh"
alias kiro-web="cd /path/to/kiro-client && git checkout web && ./build.sh && ./run.sh"

# 然后直接使用
kiro-web
```

### 技巧 2: Web 版本后台运行

```bash
# 后台启动
nohup ./run.sh -h 0.0.0.0 -p 8080 > server.log 2>&1 &

# 查看日志
tail -f server.log

# 停止服务器
pkill -f kiro-web
```

### 技巧 3: 开发模式（实时编译）

```bash
# GUI
wails dev

# TUI
go run ./cmd/tui/

# Web
go run ./cmd/web/
```

## 🎯 推荐配置

### 注册配置建议

| 场景 | 数量 | 并发 | 延时(秒) |
|------|------|------|---------|
| 测试 | 1-2 | 1 | 0 |
| 少量 | 5-10 | 2-3 | 5 |
| 批量 | 10-50 | 3-5 | 10 |
| 大量 | 50+ | 5-10 | 15 |

### 邮箱源选择

- **Outlook**: 需要先在"邮箱池"添加账号
- **CloudMail**: 临时邮箱，无需准备（推荐）
- **HttpAPI**: 需要配置 HTTP API 邮箱服务

## 🆘 获取帮助

- **文档**: 查看项目根目录下的各种 `.md` 文件
- **Issues**: https://github.com/NevinXuHui/kiro-client/issues
- **测试**: Web 版本提供测试页面 http://localhost:8080/test.html

## ⚡ 总结

三个版本任你选择，简单三步即可开始：

```bash
git checkout <分支>
./build.sh
./run.sh
```

**5 分钟快速上手，立即开始使用！** 🚀

---

**更新日期**: 2026-08-24  
**适用版本**: GUI v1.0, TUI v1.0, Web v1.0  

💡 **提示**: 首次使用建议从 TUI 或 Web 版本开始，它们编译更快。
