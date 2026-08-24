# Kiro Client 架构文档

> 本文档描述 Kiro Client 的整体架构与核心流水线。项目为 Wails v2 桌面应用：Go 后端 + 原生 JS 前端（无框架）。

## 1. 总体结构

```
┌─────────────────────────────────────────────────────────┐
│                    前端（frontend/）                      │
│  原生 JS + CSS · Wails 绑定调用 Go API · i18n 固定中文    │
└──────────────────────────┬──────────────────────────────┘
                           │ Wails runtime (window.go)
┌──────────────────────────▼──────────────────────────────┐
│                    app.go（绑定层）                       │
│  注册任务 / 手动注册 / 号池 / 邮箱池 / 代理池 / 域名池      │
│  网关控制 / 设置 / 系统托盘                               │
└───────┬───────────────────┬──────────────────┬──────────┘
        │                   │                  │
┌───────▼───────┐  ┌────────▼────────┐  ┌─────▼──────────┐
│ internal/core │  │ internal/pool   │  │ reverseproxy   │
│ 注册流水线     │  │ 号池/健康/额度   │  │ Kiro 网关      │
└───────┬───────┘  └────────┬────────┘  └─────┬──────────┘
        │                   │                 │
┌───────▼───────────────────▼─────────────────▼──────────┐
│  支撑层：email（邮箱源/域名池）· proxy（代理池）           │
│          waf（TLS/浏览器）· browser（指纹）· storage      │
│          crypto · http · subscription                   │
└─────────────────────────────────────────────────────────┘
```

## 2. 核心流水线

### 2.1 注册流水线（internal/core）

AWS Builder ID 注册共 15 步，由 `Registrar.Run()` 驱动，任一步失败进入 `task` 层重试策略：

| 步骤 | 说明 |
|---|---|
| Step1 OIDC | OIDC Client 注册（`oidc.us-east-1.amazonaws.com`） |
| Step2 Device | 设备授权（device_authorization） |
| Step3 Email | 邮箱地址生成 |
| Step4 Portal | portal.sso 会话建立 |
| Step5 WorkflowInit | signin.aws 工作流初始化 |
| Step6 SubmitEmail | 提交邮箱 |
| Step7 Signup / Step7.5 SignupInit | 注册表单 + signup 初始化 |
| Step7.8 ProfileInit / Step8 ProfileStart | profile.aws.amazon.com 资料初始化与补齐 |
| Step9 SendOTP | send-otp 邮箱验证码发送（**TES 风控最可能拦截点**） |
| Step10 GetOTP | 邮箱收码 |
| Step11 CreateIdentity | 创建身份 |
| Step12 SSO | SSO 工作流（注册完成） |
| Step13 VerifyAlive | 验活 |
| Step14 KiroAuthorize | Kiro OAuth 授权（authorization_code + PKCE） |
| Step15 | 结果入库（`data.SaveKiroSuccess` → 号池） |

关键点：
- **WAF 绕过**：`internal/waf` 用 chromedp 启动真实浏览器内核，在页面上下文内执行 fetch，规避 TLS 指纹（JA3/JA4）检测；cookie 与 tls-client 会话双向同步。**专业适配 AWS 8.1 最新风控，核心请求依赖系统 Edge 浏览器内核（Chromium）执行**。
- **TLS 指纹**：`tls-client` Chrome_144 profile 模拟真实浏览器握手。
- **send-otp 兜底**：接口直连被 TES 拦截（BLOCKED/WAF challenge）时，回退到浏览器会话内执行 `/api/start` + `/api/send-otp`。

### 2.1.1 注册机专项架构

```
┌──────────────────── 注册机（任务层） ────────────────────┐
│                     task.Manager                        │
│           并发调度 · 延迟 · 停止信号 · 统计               │
└─────────────────────────┬───────────────────────────────┘
                          │ 每账号 goroutine
┌─────────────────────────▼───────────────────────────────┐
│                 coordinator（协调器）                     │
│  ┌─────────────┐  ┌────────────────────┐                │
│  │ 代理池抽签    │  │ 邮箱源领取          │                │
│  │ proxy.Pick  │  │ Outlook池/域名轮换   │                │
│  │ 被封剔除     │  │ CloudMail建号       │                │
│  └──────┬──────┘  └─────────┬──────────┘                │
│         └────────┬──────────┘                           │
│           熔断决策：bannedProxies / bannedDomains        │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│             core.Registrar（15 步流水线）                │
│  ┌─────────────────────────────────────────────────┐    │
│  │ 传输层：tls-client（Chrome_144 指纹）              │    │
│  │ 应用层：waf/chromedp → Edge 内核（Chromium）       │    │
│  │        真实页面上下文执行关键请求（WAF 绕过）        │    │
│  │ 收码层：email 包（Outlook / HTTP API / CloudMail）    │    │
│  └─────────────────────────────────────────────────┘    │
└─────────────────────────┬───────────────────────────────┘
                          │
            ┌─────────────┴─────────────┐
            ▼                           ▼
       data.SaveKiroSuccess       失败 → 熔断分支
            │                    （换域名/换代理/重试）
            ▼
     号池（pool）→ 健康检测/额度 → 网关可用
```

### 2.1.2 注册机完整流程图

```
注册任务启动
    │
    ▼
校验配置（数量/并发/延迟/邮箱源）
    │  Outlook：邮箱池非空？
    │  HTTP API：邮箱池非空？
    │  CloudMail：域名池非空？配置非空？
    ├── 否 ──► 终止任务
    ▼
预加载域名池状态（拉黑/禁用 → 运行时跳过）
    │
    ▼
并发调度（每账号独立 goroutine + 延迟）
    │
    ▼
┌── 每账号循环 ──────────────────────────────────────────┐
│                                                       │
│  ① 抽签代理（权重） ──► ② 领取邮箱/域名轮换 ──► 创建邮箱 │
│                                                       │
│  ③ 注册流水线（core.Registrar）：                      │
│     OIDC → Device → Email → Portal → WorkflowInit      │
│     → SubmitEmail → Signup → SignupInit                │
│     → ProfileInit → ProfileStart → SendOTP             │
│     → GetOTP → CreateIdentity → SSO → VerifyAlive      │
│     → KiroAuthorize                                    │
│     （关键步骤经 Edge 内核浏览器执行，规避 TLS/WAF 风控） │
│                                                       │
│  ④ 成功 ──► 验活 ──► 入库号池 ──► 结果输出 ──► 下一账号 │
│                                                       │
│  ⑤ 失败（按错误分类熔断）：                            │
│     ├─ send-otp 域名被拉黑 ──► 标记域名(持久化)          │
│     │      ──► 换域名重建邮箱 ──► 重置重试预算 ──► ③    │
│     ├─ 风控拦截 BLOCKED ──► 标记代理                   │
│     │      ──► 重新抽签换代理 ──► ③                   │
│     └─ 其他错误 ──► 预算内重试 ──► 耗尽记失败           │
│                                                       │
│  终止条件：任务完成 / 全部代理被封 / 全部域名拉黑 /      │
│            用户停止                                   │
└───────────────────────────────────────────────────────┘
```
### 2.2 任务协调器（internal/task）

并发控制 + 三级熔断，全部**逐目标隔离**（不灭门）：

```
并发 goroutine（配置数量）
  │
  ├─ 逐代理熔断：错误命中风控 → 标记该代理（bannedProxies）
  │     → 下一轮从代理池重新抽签（上限 20 次候选）
  │
  ├─ 域名级熔断：send-otp "域名已被拉黑" → 标记域名
  │     → 持久化到域名池（domain_pool.json）+ 换域名重建邮箱重试
  │
  └─ 换号重试：Outlook 账号失败 → 领取下一个账号（retryLoop 预算控制）
```

- 单代理被封不终止任务；所有代理被封才停止。
- 域名全部拉黑则放弃该任务；拉黑状态持久化，重启后跳过。

### 2.3 网关流水线（internal/reverseproxy）

```
客户端 → /v1/chat/completions（Bearer API Key 校验）
  → KiroCircuitTripped（429 冷却/熔断检查）
  → KiroEnsureAccessToken（accessToken 缺失或临近过期 → 自动刷新）
  → KiroTranslateChatRequest（OpenAI 格式 → Kiro 上游格式）
  → KiroSendChat（tls-client 发送，e2e 代理可选）
  → ParseEventStream（SSE 事件流解析 → OpenAI 格式回传）
```

- `429` 触发账号级冷却（cooldownUntil），冷却期内自动轮换其他账号。
- 账号池按策略（顺序/轮询/随机）挑选，健康/异常状态参与决策。

### 2.4 号池健康检测（internal/pool）

```
UpdateAccountInfo：
  1. CheckHealth        → RefreshAccessToken（OIDC token 端点）
       └─ classifyHealthError（状态细分语义）：
            401/403 → unhealthy + AUTH（令牌吊销，真死）
            429     → healthy  + 429（软失败，限流恢复快）
            502/503/504 → unhealthy + 对应码（上游不可用）
            其他 HTTP ≥400 → unhealthy + 数字码
            网络错误 → unhealthy + NET
  2. GetUsage           → CodeWhisperer/Q usage 端点（额度/重置时间）
  3. GetAvailableModels → 模型列表
```

**自动增量刷新**（`app.go`）：后台 goroutine 每 60s 巡检全部号池：
- 跳过：健康且检测时间 < 5min；AUTH 死号（省请求）
- 刷新：超 TTL、429（60s 后）、NET/5xx/未知
- 每轮最多 3 个账号，与手动刷新互斥（`healthMu`）

## 3. 数据存储

见 README「数据目录」表。要点：
- 全部 JSON/文本文件，位于 `%APPDATA%\kiro-client`（可自定义）。
- 域名池/代理池/号池均为独立文件，写入采用「临时文件 + rename」原子操作。
- 账号运行时字段（健康/额度）不持久化，实时查询；登录凭据（RefreshToken）持久化。

## 4. 反风控设计汇总

| 层级 | 机制 | 失败隔离 |
|---|---|---|
| 传输 | tls-client Chrome_144 指纹 | — |
| 应用 | chromedp 浏览器内请求（WAF 绕过） | — |
| IP | 代理池权重抽签 | 逐代理熔断（不灭门） |
| 域名 | 域名池轮换 | 域名级熔断 + 持久化 |
| 频率 | 并发/延迟配置 + 增量刷新限速 | 429 冷却 |

## 5. 前端（frontend/）

- 原生 JS 模块（无框架）：`app.js`（导航/配置/弹窗）、`ui.js`（注册页/域名选择）、`task.js`、`accounts.js`、`cloudmail.js`、`pools.js`、`gateway.js`、`proxy_pool.js`、`domain_pool.js`、`subscription.js`、`models.js` 等。
- `index.html` 单页 + 模态框；`wailsjs/` 为生成绑定。
- i18n：仅中文（DICT 单语言，语言切换已移除）。

## 6. 构建与发布

```bash
wails build        # 产物 build/bin/kiro-client.exe
```

- 前端构建脚本 `frontend/build.js`：复制 HTML/JS/CSS 到 `frontend/dist` 并注入 Wails runtime。
- 支持 Windows（主）/ macOS / Linux（AppImage，`wails.json` 已声明）。

## 7. 已知限制

- 仅支持 AWS `us-east-1` 区域 OIDC/Start URL（`d-9067642ac7` 目录）。
- 网关为单机本地服务，无多用户/鉴权加密。
- AWS 检测策略更新可能导致流程失效（详见 README 免责声明）。
