# Kiro 账号验活逻辑

## 概述

参考 KiroClaim 和 core.VerifyAlive 的封号判定逻辑，增强账号健康检查的准确性。

## 验活流程

### 1. Token 刷新（必须步骤）

```go
accessToken, err := c.RefreshAccessToken(account)
```

**判定规则：**
- ✅ **200** → Token 有效，继续检查
- ❌ **401/403** → `suspended`（账号吊销/封禁，判死）
- ⚠️ **其他错误** → `unhealthy`（临时错误，可重试）

### 2. 查询用量（GetUsageLimits）

```go
usage, err := c.GetUsage(account, accessToken)
```

**判定规则：**
- ✅ **200** → 解析额度信息
- ❌ **403** → `suspended`（Q 端点封号信号）
- ⚠️ **其他错误** → 不影响健康状态（可能是 API 限制）

### 3. 查询模型（ListAvailableModels）

```go
models, statusCode := c.GetAvailableModels(account, accessToken)
```

**判定规则：**
- ✅ **200** → 返回可用模型列表
- ❌ **403** → `suspended`（模型接口封号）
- ⚠️ **其他错误** → 返回默认模型列表

## 健康状态分类

### 1. healthy（健康）
- Token 刷新成功
- Q 端点无 403
- 429 限流视为健康（软失败，账号保留）

### 2. suspended（封禁）
触发条件（任一即判死）：
- Token 刷新 401/403
- GetUsageLimits 403
- ListAvailableModels 403

特征：
- `HealthStatus = "suspended"`
- `HealthCode = "AUTH"`
- **不再自动重试**（省请求，需手动全量刷新）

### 3. unhealthy（异常）
- 5xx 错误（502/503/504）
- 网络错误（NET）
- 其他 4xx 错误

特征：
- `HealthStatus = "unhealthy"`
- `HealthCode = "502"/"503"/"504"/"NET"/其他码`
- **立即重试**（瞬时错误尝试恢复）

## 增量刷新策略（9router 惰性思想）

```go
func NeedsRefresh(acc *Account, now time.Time, ttl time.Duration) bool
```

**刷新判定：**
1. **AUTH 死号** → ❌ 不自动重试
2. **429 软失败** → ⏱️ 60s 后重试（限流恢复快）
3. **其他异常** → ✅ 立即重试（NET/5xx/UNKNOWN）
4. **健康且检测 < TTL** → ⏭️ 跳过（默认 5min）

## 与 KiroClaim 对比

| 项目 | kiro-client | KiroClaim |
|------|-------------|-----------|
| Token 刷新 | IDC → Social 回退 | IDC → Social 回退 |
| 封号判定 | 401/403 + Q 端点 403 | 401/403 + Q 端点 403 + CreateSubscriptionToken |
| 并行探测 | 用量 + 模型 | 用量 + 模型 + 升级接口 |
| 状态码 | healthy/unhealthy/suspended | active/suspended |
| 重试策略 | 9router 惰性（AUTH 不重试） | 后台自动刷新 |

## API 接口

### 刷新单个账号
```bash
POST /api/pool/refresh/account
{
  "poolID": "pool-id",
  "email": "user@example.com"
}
```

### 刷新整个号池
```bash
POST /api/pools/refresh
{
  "poolID": "pool-id"
}
```

### 导入时验活
```bash
POST /api/pool/import
{
  "data": "[...]",
  "verify": true,           # 是否验活
  "concurrency": 3,         # 并发数
  "skipSuspended": true     # 跳过封禁账号
}
```

**返回示例：**
```json
{
  "id": "pool-id",
  "imported": 10,
  "skipped": 2,
  "verifyStats": {
    "total": 12,
    "active": 10,
    "suspended": 1,
    "unknown": 1
  }
}
```

## 最佳实践

### 1. 导入新账号
```bash
# 验活并过滤封禁账号
curl -X POST /api/pool/import \
  -d '{"data":"[...]","verify":true,"skipSuspended":true}'
```

### 2. 定期全量刷新
```bash
# 每 5 分钟刷新一次号池
curl -X POST /api/pools/refresh -d '{"poolID":"default"}'
```

### 3. 手动验活单个账号
```bash
curl -X POST /api/pool/refresh/account \
  -d '{"poolID":"default","email":"user@example.com"}'
```

## 注意事项

1. **429 限流不算死号**：保留账号，60s 后重试
2. **AUTH 死号不自动重试**：减少无效请求
3. **封号判定优先级**：Token 403 > Q 端点 403
4. **并发控制**：导入验活默认 3 并发，避免触发上游限流
