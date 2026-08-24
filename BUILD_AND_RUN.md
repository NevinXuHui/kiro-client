# 🚀 一键编译和运行指南

## 📋 概述

Kiro Client 提供了两个便捷脚本，让编译和运行变得简单：

- **build.sh** - 一键编译脚本
- **run.sh** - 一键运行脚本

这两个脚本会自动检测当前 Git 分支，并编译/运行对应版本。

## 🏗️ build.sh - 一键编译

### 功能特性

- ✅ 自动检测当前分支
- ✅ 根据分支选择编译目标
- ✅ 显示编译进度和结果
- ✅ 自动计算文件大小
- ✅ GUI 版本可选安装到 /Applications
- ✅ 自动添加执行权限

### 使用方式

```bash
# 1. 赋予执行权限（仅首次需要）
chmod +x build.sh

# 2. 运行编译
./build.sh
```

### 各分支编译

#### GUI 版本 (main/master 分支)

```bash
git checkout main
./build.sh
```

**编译输出**:
- 位置: `build/bin/kiro-client.app`
- 大小: ~19 MB
- 功能: 会询问是否安装到 /Applications

**依赖检查**:
- 自动检查 Wails 是否安装
- 如未安装会提示安装命令

#### TUI 版本 (tui-version 分支)

```bash
git checkout tui-version
./build.sh
```

**编译输出**:
- 位置: `./kiro-tui`
- 大小: ~19 MB
- 功能: 自动添加执行权限

#### Web 版本 (web 分支)

```bash
git checkout web
./build.sh
```

**编译输出**:
- 位置: `./kiro-web`
- 大小: ~19 MB
- 功能: 自动添加执行权限

### 输出示例

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔧 Kiro Client 一键编译脚本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 当前分支: web

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🌐 编译 Web 版本 (HTTP Server)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 开始编译...
✅ 编译成功
📍 输出: ./kiro-web
📍 大小: 19M
✅ 已添加执行权限

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✅ 编译完成
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

## 🚀 run.sh - 一键运行

### 功能特性

- ✅ 自动检测当前分支
- ✅ 根据分支选择运行目标
- ✅ 检查可执行文件是否存在
- ✅ 自动查找安装路径（GUI）
- ✅ 支持命令行参数（Web）
- ✅ 友好的错误提示

### 使用方式

```bash
# 1. 赋予执行权限（仅首次需要）
chmod +x run.sh

# 2. 运行应用
./run.sh
```

### 各分支运行

#### GUI 版本 (main/master 分支)

```bash
git checkout main
./run.sh
```

**运行逻辑**:
1. 优先查找 `/Applications/kiro-client.app`
2. 如未安装，查找 `build/bin/kiro-client.app`
3. 使用 `open` 命令启动应用

**提示**:
- 如应用未显示，请在 Dock 栏查找
- 建议先编译并安装到 /Applications

#### TUI 版本 (tui-version 分支)

```bash
git checkout tui-version
./run.sh
```

**运行逻辑**:
1. 检查 `./kiro-tui` 是否存在
2. 直接在终端中运行

**操作**:
- 运行后进入 TUI 界面
- 使用键盘操作
- 按 ESC 返回主菜单
- 按 q 退出

#### Web 版本 (web 分支)

```bash
git checkout web
./run.sh

# 或指定主机和端口
./run.sh -h 0.0.0.0 -p 8080
./run.sh --host 0.0.0.0 --port 3000
```

**运行逻辑**:
1. 检查 `./kiro-web` 是否存在
2. 解析命令行参数
3. 启动 Web 服务器

**参数**:
- `-h, --host`: 服务器地址（默认: localhost）
- `-p, --port`: 服务器端口（默认: 8080）

**访问**:
- 浏览器: `http://localhost:8080`
- 测试页面: `http://localhost:8080/test.html`
- 按 Ctrl+C 停止服务器

### 输出示例

#### GUI 版本
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🚀 Kiro Client 一键运行脚本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 当前分支: main

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🖥️  运行 GUI 版本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 启动应用...
✅ GUI 应用已启动
💡 如未显示窗口，请在 Dock 栏查找
```

#### TUI 版本
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🚀 Kiro Client 一键运行脚本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 当前分支: tui-version

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📺 运行 TUI 版本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 启动 TUI...

[TUI 界面显示]
```

#### Web 版本
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🚀 Kiro Client 一键运行脚本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 当前分支: web

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🌐 运行 Web 版本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📍 启动 Web 服务器...
📍 地址: http://localhost:8080
💡 按 Ctrl+C 停止服务器

2026/08/24 22:00:00 🌐 Kiro Client Web Server
2026/08/24 22:00:00 📍 Address: http://localhost:8080
2026/08/24 22:00:00 🚀 Server started successfully!
```

## 📝 完整工作流程

### 1. GUI 版本工作流

```bash
# 1. 切换到 GUI 分支
git checkout main

# 2. 编译
./build.sh
# 按提示选择是否安装到 /Applications

# 3. 运行
./run.sh
# 应用将在桌面显示
```

### 2. TUI 版本工作流

```bash
# 1. 切换到 TUI 分支
git checkout tui-version

# 2. 编译
./build.sh

# 3. 运行
./run.sh
# 进入 TUI 界面操作
```

### 3. Web 版本工作流

```bash
# 1. 切换到 Web 分支
git checkout web

# 2. 编译
./build.sh

# 3. 运行
./run.sh

# 4. 访问
# 浏览器打开: http://localhost:8080
```

## 🔧 故障排查

### 问题 1: 权限被拒绝

**错误**:
```
bash: ./build.sh: Permission denied
```

**解决**:
```bash
chmod +x build.sh run.sh
```

### 问题 2: Wails 未安装

**错误**:
```
❌ Wails 未安装
📍 请运行: go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

**解决**:
```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### 问题 3: 可执行文件未找到

**错误**:
```
❌ 可执行文件未找到
💡 请先运行: ./build.sh
```

**解决**:
```bash
./build.sh
```

### 问题 4: 端口被占用

**错误**:
```
listen tcp :8080: bind: address already in use
```

**解决**:
```bash
# 方案 1: 使用其他端口
./run.sh -p 3000

# 方案 2: 停止占用端口的进程
lsof -ti:8080 | xargs kill -9
```

## 💡 使用技巧

### 技巧 1: 快速切换版本

```bash
# 一条命令切换并编译
git checkout tui-version && ./build.sh && ./run.sh

# 或使用别名
alias kiro-gui="git checkout main && ./build.sh && ./run.sh"
alias kiro-tui="git checkout tui-version && ./build.sh && ./run.sh"
alias kiro-web="git checkout web && ./build.sh && ./run.sh"
```

### 技巧 2: 后台运行 Web 版本

```bash
# 后台运行
nohup ./run.sh > server.log 2>&1 &

# 查看日志
tail -f server.log

# 停止服务器
pkill -f kiro-web
```

### 技巧 3: 开发模式

```bash
# GUI: 使用 wails dev
git checkout main
wails dev

# TUI: 实时编译运行
git checkout tui-version
go run ./cmd/tui/

# Web: 实时编译运行
git checkout web
go run ./cmd/web/
```

### 技巧 4: 清理构建

```bash
# 清理所有构建产物
rm -rf build/ kiro-tui kiro-web

# 清理 Go 缓存
go clean -cache
```

## 📚 相关文档

- [GUI 版本 README](README.md)
- [TUI 版本指南](TUI_VERSION_SUMMARY.md)
- [Web 版本指南](WEB_VERSION_GUIDE.md)
- [项目完成总结](PROJECT_COMPLETION_SUMMARY.md)

## 🎯 快速参考

### 编译所有版本

```bash
# GUI
git checkout main && ./build.sh

# TUI
git checkout tui-version && ./build.sh

# Web
git checkout web && ./build.sh
```

### 运行所有版本

```bash
# GUI
git checkout main && ./run.sh

# TUI
git checkout tui-version && ./run.sh

# Web
git checkout web && ./run.sh -h 0.0.0.0 -p 8080
```

---

**提示**: 编译前确保已安装 Go 1.22+ 和相关依赖。

**帮助**: 遇到问题请查看各版本的详细文档或提交 Issue。

🚀 **享受使用 Kiro Client！**
