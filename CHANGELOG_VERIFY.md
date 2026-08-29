# 验活功能增强 Changelog

## 2025-08-29

### ✨ 新增功能

#### 1. 导入时验活 (5bccbdd)
- **新增 API 参数**：
  - `verify`: 是否启用验活
  - `concurrency`: 并发数（默认 3）
  - `skipSuspended`: 是否跳过封禁账号
  
- **返回验活统计**：
  ```json
  {
    "verifyStats": {
      "total": 12,
      "active": 10,
      "suspended": 1,
      "unknown": 1
    }
  }
  ```

- **实现细节**：
  - `pool.VerifyAccount`: 单账号验活
  - `pool.BatchVerify`: 批量并发验活
  - 优先 IDC 端点，回退 Social 端点

#### 2. 增强更新验活 (989c9d0)
- **封号判定更严格**：
  - Token 刷新 401/403 → `suspended`
  - GetUsageLimits 403 → `suspended`
  - ListAvailableModels 403 → `suspended`

- **优化流程**：
  - 移除重复的 `CheckHealth` 调用
  - 直接刷新 Token 作为健康检查
  - 并行查询用量和模型

- **状态分类**：
  - `healthy`: 正常可用
  - `suspended`: 已封禁（AUTH 死号不重试）
  - `unhealthy`: 异常（5xx/NET 立即重试）

### 📝 文档完善

#### 验活逻辑文档 (65247e3)
- 完整验活流程说明
- 健康状态分类规则
- 与 KiroClaim 对比
- API 接口使用示例
- 最佳实践建议

详见：`docs/VERIFY_LOGIC.md`

### 🔧 技术改动

#### API 变更
```go
// 旧版本
func GetAvailableModels(account *Account, accessToken string) []string

// 新版本（返回状态码用于判定封号）
func GetAvailableModels(account *Account, accessToken string) ([]string, int)
```

#### 导入接口增强
```bash
# 旧版本：直接导入
POST /api/pool/import {"data": "[...]"}

# 新版本：支持验活和过滤
POST /api/pool/import {
  "data": "[...]",
  "verify": true,
  "concurrency": 3,
  "skipSuspended": true
}
```

### 🎯 参考实现

- **KiroClaim** `handler/health.go:255-359`
  - 刷新 Token → 并行探测 → 封号判定
  - 优先级：CreateSubscriptionToken > GetUsageLimits > ListAvailableModels
  
- **core.VerifyAlive** `internal/core/verify.go:17-89`
  - Token 401/403 判死
  - Q 端点 403 判死
  - Token 有效但端点不可达仍视为存活

### 📊 性能优化

- **并发验活**：默认 3 并发，可配置
- **增量刷新**：9router 惰性思想
  - AUTH 死号不重试（省请求）
  - 429 软失败 60s 后重试
  - 健康账号 < TTL 跳过
- **请求优化**：移除重复健康检查

### 🔐 封号判定优先级

1. **Token 刷新 403** → 账号吊销（最高优先级）
2. **Q 端点 403** → 账号封禁
3. **模型列表 403** → 模型权限封禁

### 📦 文件变更

```
docs/VERIFY_LOGIC.md              新增  163 行
internal/pool/verify.go           新增  356 行
internal/pool/kiro_client.go      修改  +62/-21
internal/web/server.go            修改  +112/-4
test_import_verify.sh             新增  测试脚本
```

### 🚀 使用示例

#### 导入时验活
```bash
curl -X POST http://localhost:9702/api/pool/import \
  -H "Content-Type: application/json" \
  -d '{
    "data": "[{...}]",
    "verify": true,
    "concurrency": 5,
    "skipSuspended": true
  }'
```

#### 手动刷新账号
```bash
curl -X POST http://localhost:9702/api/pool/refresh/account \
  -d '{"poolID":"default","email":"user@example.com"}'
```

### ⚠️ 注意事项

1. **429 不算死号**：限流视为健康，会保留账号并稍后重试
2. **AUTH 死号**：不再自动重试，需手动处理
3. **并发控制**：避免并发过高触发上游限流
4. **状态持久化**：健康状态实时更新到 `pools.json`

### 🔄 后续优化方向

- [ ] 支持 CreateSubscriptionToken 探测（KiroClaim 同款）
- [ ] 验活结果缓存（减少重复请求）
- [ ] 验活进度实时推送（SSE）
- [ ] 按健康状态分组查看
- [ ] 批量重新验活封禁账号
