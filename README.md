<p align="center">
  <img src="kiro-color.png" width="130" alt="Kiro Client 图标">
</p>

<h1 align="center"><b>Kiro Client</b></h1>

<p align="center">基于 Wails v2 的 AWS Builder ID 批量注册管理 + Kiro 模型本地网关</p>

<p align="center">
  <a href="../../releases"><img src="https://img.shields.io/badge/下载-Releases-2f80f5?style=flat-square" alt="下载"></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square" alt="Go 1.26">
  <img src="https://img.shields.io/badge/Wails-v2-DF4A63?style=flat-square" alt="Wails v2">
  <img src="https://img.shields.io/badge/平台-Windows-0078D6?style=flat-square" alt="平台 Windows">
  <img src="https://img.shields.io/badge/License-MIT-yellow?style=flat-square" alt="License MIT">
</p>

<p align="center">
  <a href="#功能特性">功能特性</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="#使用指南">使用指南</a> ·
  <a href="#配置说明">配置说明</a> ·
  <a href="#常见问题">常见问题</a> ·
  <a href="#免责声明">免责声明</a> ·
  <a href="#开源协议">开源协议</a>
</p>

基于 [Wails v2](https://wails.io) 构建的桌面工具，集 **AWS Builder ID 批量注册管理** 与 **Kiro 模型本地网关** 于一体。支持 Outlook 邮箱池、HTTP 邮箱、自部署 Cloud-Mail 多邮箱源，内置浏览器指纹模拟、逐代理熔断、域名级熔断、代理池与域名池，用于规避批量注册场景下的账号风控；注册成功的账号自动入库号池，经健康检测与额度校验后，通过本地 OpenAI 兼容网关供第三方客户端（Cursor、Windsurf 等）接入。

> ⚠️ 本工具仅用于个人学习与研究。请勿高并发注册、严禁商用；AWS 检测策略持续更新，本项目功能可能随时失效。
>
> 当前为初始版本，手动授权暂不可用。本项目适合有一定技术能力者使用，涉及专业知识较多。

## 功能特性

### 批量注册（注册机）
- **Outlook 邮箱池**：批量导入 Outlook 账号（支持应用专用密码），并发注册、失败自动换号重试。
- **HTTP 邮箱**：批量导入 `邮箱----API地址`，通过 HTTP GET 拉信提取验证码（如 ourmail.top）。
- **Cloud-Mail 自部署邮箱**：支持多服务器配置，域名自动发现聚合，注册按域名轮换。
- **手动浏览器注册（兜底）**：弹出真实浏览器窗口，由真人完成注册与授权（可绕过行为风控与域名拉黑），程序自动轮询令牌并入库。
- **专业适配 AWS 8.1 最新风控**：核心请求依赖系统 **Edge 浏览器内核**（Chromium）在真实页面上下文中执行，配合 TLS 指纹模拟，从传输层到行为层全面规避当前最新风控检测。
- **反风控体系**：
  - TLS 指纹模拟（Chrome 144 profile，`tls-client`）
  - chromedp 浏览器内请求（绕过 WAF 指纹检测）
  - **逐代理熔断**：单代理被封仅标记该代理，不终止整批任务
  - **域名级熔断**：`send-otp` 被 TES 拦截（域名拉黑）时自动换域名重试，拉黑状态持久化
  - **代理池**：多代理按权重抽签，被封代理自动退出抽签
  - **域名池**：Cloud-Mail 域名集中管理，可手动禁用 / 解除拉黑，注册时自动跳过

### 号池管理
- 注册成功账号自动入库（含邮箱、ClientID、RefreshToken 等完整凭据）
- **健康检测**：状态细分——`健康` / `异常`（细分码 `AUTH`=令牌失效、`429`=限流、`502/503/504`=上游不可用、`NET`=网络错误）/ `未知`
- **额度查询**：CodeWhisperer / Amazon Q usage API，展示剩余额度与重置时间，支持无感轮询决策
- **自动增量刷新**：后台每 60s 巡检，仅刷新超时（5min）或异常可恢复账号，每轮最多 3 个（避免触发上游限流）
- 账号卡片实时状态徽章 + 额度进度条 + 可用模型列表

### Kiro 本地网关
- OpenAI 兼容：`/v1/chat/completions`、`/v1/models`、`/health`、`/stats`
- Bearer API Key 鉴权（默认 `123`，可改），默认端口 `20130`（可改）
- 完整反向代理流水线：熔断 → 令牌确保（临近过期自动刷新）→ 请求翻译 → 上游发送 → 事件流解析
- 默认**关闭**，需在设置页手动开启

### 其他
- 系统托盘（关闭窗口隐藏到托盘）
- 注册结果明文 JSON 输出到指定目录
- 语言：中文（固定）

## 快速开始

### 直接运行
1. 从 [Releases](../../releases) 下载最新 `kiro-client.exe`。
2. 双击运行，首次使用建议先配置（见下）。

### 从源码构建
```bash
# 依赖：Go 1.26+、Node.js、Wails CLI v2
go install github.com/wailsapp/wails/v2/cmd/wails@latest

wails build
# 产物：build/bin/kiro-client.exe
```

## 使用指南

### 一、注册账号（注册机页）

**方式 A：Outlook**
1. 邮箱池页导入 Outlook 账号（每行 `邮箱----密码----ClientID----RefreshToken`，后两段可对调）。
2. 注册机页选择 Outlook，填写数量/并发/延迟，点击开始。

**方式 B：HTTP 邮箱**
1. 邮箱池页 → HTTP 邮箱标签 → 添加账号（每行 `邮箱----API地址`，支持 `邮箱：` 前缀）。
2. 注册机页选择 HTTP 邮箱，填写数量/并发/延迟，点击开始。

**方式 C：Cloud-Mail**
1. 邮箱池页 → Cloud-Mail 标签 → 添加自部署服务器（名称/地址/管理员邮箱/密码），点击测试（自动拉取服务器域名）。
2. 注册机页选择 Cloud-Mail，勾选域名（或随机/轮询模式），开始注册。

**方式 D：手动浏览器注册**
1. 注册机页点击「开始手动注册」，弹出真实浏览器窗口。
2. 在浏览器中完成 AWS Builder ID 注册与 Kiro 授权。
3. 程序自动轮询设备授权令牌，成功后自动入库。

> 💡 建议在设置页配置**代理池**与**域名池**；并发 1、注册数 < 2、避免短时间多次重试，否则易触发风控导致 IP 与域名被拉黑。

### 二、注册机执行流程（自动注册）

```
任务启动（数量 / 并发 / 延迟）
  │
  ├─ 校验邮箱源：
  │     Outlook   → 读取邮箱池（共享领取，并发安全）
  │     HTTP API  → 读取 HTTP 邮箱池（GET 接口收码）
  │     CloudMail → 域名池轮换（跳过禁用/已拉黑域名）→ 选服务器 → 创建邮箱
  │
  ├─ 每账号抽签代理（代理池按权重；被封代理自动剔除）
  │
  ├─ 注册流水线（15 步，核心依赖 Edge 内核浏览器执行关键请求）：
  │     OIDC 注册 → 设备授权 → 邮箱提交 → signin 工作流 → 资料补齐
  │     → send-otp（风控拦截高发点，失败自动回退浏览器执行）
  │     → 收码验证 → 创建身份 → SSO 完成 → 验活 → Kiro OAuth 授权
  │
  ├─ 成功 → 自动入库号池（完整凭据 + 额度/模型信息）
  │
  └─ 失败（分级熔断，互不牵连）：
        ├─ 域名被 TES 拉黑 → 标记并持久化 → 换域名重建邮箱重试
        ├─ 代理被风控拦截 → 标记该代理 → 换代理重试
        └─ 其他错误 → 预算内重试，耗尽记失败
```

> 完整流程图与架构图见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)「2.1 注册流水线」。

### 三、管理号池（号池页）
- 查看账号健康状态、额度、模型；支持单账号/批量/增量刷新。
- 健康细分码含义见上「号池管理」。

### 四、接入网关（网关页）
1. 开启网关（默认端口 `20130`）。
2. 第三方客户端配置：
   - Base URL：`http://127.0.0.1:20130`
   - API Key：默认 `123`（网关页可改）
3. 验证：
```bash
curl http://127.0.0.1:20130/v1/models \
  -H "Authorization: Bearer 123"
```

## 配置说明

### 数据目录
默认 `%APPDATA%\kiro-client`（设置页可改，自动迁移已有数据）：

| 文件 | 内容 |
|---|---|
| `accounts.json` | Outlook 邮箱池 |
| `httpapi.json` | HTTP 邮箱池（邮箱 + 收信 API） |
| `cloudmail.dat` | Cloud-Mail 服务器配置 |
| `domain_pool.json` | 域名池（启用/禁用/拉黑状态） |
| `proxy_pool.json` | 代理池 |
| `pools.json` | 号池（账号 + 健康/额度运行时数据） |
| `identities.dat` | 浏览器指纹身份库 |
| `gateway_accounts.json` | 网关账号同步 |
| `storage.conf` | 应用配置（数据目录/输出目录/代理/网关密钥） |

### 代理池（设置页）
- 每行一个代理，`URL + 权重（1-100）`，按权重概率抽签。
- 支持 `http` / `https` / `socks5` 完整 URL 及简写（`host:port`、`user:pass@host:port` 等）。
- 注册任务运行时被封代理自动剔除，仅剩可用代理轮换。

### 域名池（设置页）
- 自动同步所有 Cloud-Mail 服务器的域名，按服务器数聚合展示。
- 手动禁用某域名（注册时跳过）或解除拉黑（恢复使用）。
- 被 TES 拉黑的域名自动标记并持久化，重启后依然跳过；注册页对应域名置灰。

### 环境变量
| 变量 | 说明 |
|---|---|
| `KIRO_BROWSER_PATH` | 手动注册/浏览器自动化时指定的 Chrome/Edge 可执行文件路径（不设则自动探测） |

## 项目结构

```
├── app.go                  # Wails 绑定层（注册/号池/网关/设置等全部 API）
├── main.go                 # 应用入口
├── internal/
│   ├── core/               # 注册流水线（15 步：OIDC → 设备授权 → 验活 → Kiro OAuth）
│   ├── task/               # 任务协调器（并发/逐代理熔断/域名熔断/换号重试）
│   ├── pool/               # 号池（健康检测细分/额度查询/自动增量刷新）
│   ├── reverseproxy/       # 网关（OpenAI 兼容 + Kiro 上游转发流水线）
│   ├── email/              # 邮箱源（Outlook/HTTP API/Cloud-Mail/域名池）
│   ├── proxy/              # 代理池（权重抽签/归一化/检测）
│   ├── waf/                # TLS 指纹 + chromedp 浏览器自动化
│   ├── browser/            # 浏览器指纹身份生成
│   ├── storage/            # 数据目录与配置
│   ├── crypto/             # 加解密工具
│   └── http/               # HTTP 辅助
├── frontend/               # Web 前端（原生 JS + CSS，无框架）
└── 9router-0.5.35/         # 上游参考源码（未参与构建，勿提交）
```

> 架构细节见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 常见问题

**Q：注册总是被拦截（send-otp BLOCKED）？**
A：通常是邮箱域名已被 AWS TES 拉黑（与 IP 无关）。改用 Outlook 邮箱，或在域名池中查看/解除拉黑状态，或使用手动浏览器注册。

**Q：账号显示 `429`？**
A：限流（软失败），账号本身可用，稍后自动恢复；避免短时间频繁注册或检测。

**Q：账号显示 `AUTH`？**
A：令牌失效/吊销（RefreshToken 被撤销），需重新注册。

**Q：网关连不上？**
A：网关默认关闭，需在网关页手动开启；确认端口未被占用、API Key 正确。

**Q：如何更换数据目录？**
A：设置页 → 存储目录 → 选择新路径（自动迁移 `accounts.json` 等数据）。

## 免责声明

1. 本项目仅供学习与个人合法用途，**严禁商用**与**高并发批量注册**。
2. 注册/使用 AWS 服务须遵守 [AWS 服务条款](https://aws.amazon.com/service-terms/)；批量注册可能违反 AWS 使用政策，后果自负。
3. AWS 检测策略持续更新，本项目功能**可能随时失效**，不适合作为生产主力工具。
4. 使用时请配置代理池与域名池、控制并发与频率，避免触发风控导致 IP / 域名 / 账号被封禁。
5. 作者不对因使用本项目产生的任何直接或间接损失负责。

## 开源协议

[MIT](LICENSE)

Copyright (c) 2026 Kiro Client Contributors

本项目与 AWS、Amazon、Kiro 等品牌无任何关联，所有商标归其各自所有者所有。
