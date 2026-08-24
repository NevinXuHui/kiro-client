# 📧 Outlook 账号添加使用指南

## 🎯 快速开始

### Web 版本使用步骤

1. **启动 Web 服务器**
   ```bash
   cd /Volumes/mine/Code/ai-tools/kiro-client
   go run ./cmd/web/main.go
   # 或使用编译后的版本
   ./kiro-web
   ```

2. **打开浏览器**
   ```
   访问：http://localhost:8080
   ```

3. **进入账号管理**
   - 点击左侧菜单 "账号管理"

4. **添加账号**
   - 点击 "添加账号" 按钮
   - 粘贴账号数据
   - 点击 "添加"

---

## 📋 账号格式说明

### 标准格式

```
邮箱----密码----ClientID----RefreshToken
```

### 字段说明

| 字段 | 说明 | 示例 |
|------|------|------|
| 邮箱 | Outlook 邮箱地址 | test@outlook.com |
| 密码 | 邮箱密码 | password123 |
| ClientID | AWS Builder ID ClientID | ncGq-IBFli-08ta0fuRcM3VzLWVhc3QtMQ |
| RefreshToken | AWS Builder ID RefreshToken | aorAAAAAGsC6aEcPRleBeej-mZweLFc |

### 分隔符要求

- **必须使用**: `----` (4个短横线)
- **不能使用**: 空格、逗号、制表符、单个短横线等

---

## ✨ 支持的格式

### 1. 单个账号

```
test@outlook.com----password123----clientid----refreshtoken
```

### 2. 多个账号（批量）

```
user1@outlook.com----pass1----cid1----rt1
user2@outlook.com----pass2----cid2----rt2
user3@outlook.com----pass3----cid3----rt3
```

### 3. ClientID/RefreshToken 顺序互换

系统会自动识别 ClientID 和 RefreshToken：

```
# 标准顺序
test@outlook.com----pass----clientid----refreshtoken

# 互换顺序（自动识别）
test@outlook.com----pass----refreshtoken----clientid
```

**识别规则**:
- ClientID 通常包含 `nVzLWVhc3QtMQ`
- RefreshToken 通常以 `aorAAAAAG` 开头

### 4. 带注释

```
# 这是测试账号组
test1@outlook.com----pass1----cid1----rt1
test2@outlook.com----pass2----cid2----rt2

// 这是生产账号组
prod1@outlook.com----pass1----cid1----rt1
prod2@outlook.com----pass2----cid2----rt2
```

---

## 🔍 格式验证

### ✅ 正确格式

```
✓ test@outlook.com----password123----ncGq-IBFli-nVzLWVhc3QtMQ----aorAAAAAGsC6aEcPR
✓ user@outlook.com----pass----aorAAAAAGxxx----ncGq-IBFli-nVzLWVhc3QtMQ
✓ # 注释
✓ // 注释
```

### ❌ 错误格式

```
✗ test@outlook.com password123 clientid refreshtoken  (使用空格)
✗ test@outlook.com--pass--cid--rt  (使用2个短横线)
✗ test@outlook.com,pass,cid,rt  (使用逗号)
✗ test@outlook.com----password123----clientid  (字段不足)
✗ test----password123----clientid----refreshtoken  (邮箱无@)
```

---

## 💡 使用技巧

### 1. 批量导入

**从文件导入**:
1. 准备账号文件 `accounts.txt`
2. 复制所有内容
3. 粘贴到添加账号弹窗
4. 点击添加

**示例文件内容**:
```
# 账号文件示例
# accounts.txt

# 测试账号
test1@outlook.com----pass1----cid1----rt1
test2@outlook.com----pass2----cid2----rt2

# 生产账号
prod1@outlook.com----pass1----cid1----rt1
prod2@outlook.com----pass2----cid2----rt2
```

### 2. 使用注释组织

```
# ========== 个人账号 ==========
personal@outlook.com----pass----cid----rt

# ========== 工作账号 ==========
work@outlook.com----pass----cid----rt

# ========== 测试账号 ==========
test@outlook.com----pass----cid----rt
```

### 3. 去重保护

系统会自动跳过已存在的账号，不会重复添加：

```
# 第一次添加
test@outlook.com----pass----cid----rt  ✓ 添加成功

# 第二次添加相同账号
test@outlook.com----pass----cid----rt  ⊘ 自动跳过（已存在）
```

### 4. 格式容错

即使 ClientID 和 RefreshToken 顺序颠倒，系统也能自动识别：

```
# 两种顺序都可以
email@outlook.com----pass----clientid----refreshtoken  ✓
email@outlook.com----pass----refreshtoken----clientid  ✓
```

---

## 🧪 测试示例

### 测试账号格式

使用以下测试数据验证功能：

```
# 测试账号1 - 标准格式
test1@outlook.com----TestPass123----ncGq-IBFli-08ta0fuRcM3VzLWVhc3QtMQ----aorAAAAAGsC6aEcPRleBeej-mZweLFc

# 测试账号2 - 顺序互换
test2@outlook.com----TestPass456----aorAAAAAGsC6cYRw8f3IuQyW32GgQpgr----1CXpMrL4iJQH2oaiRm488nVzLWVhc3QtMQ

# 测试账号3 - 带注释
# 这是第三个测试账号
test3@outlook.com----TestPass789----gQENIylx9gR8GUwwa_XBRnVzLWVhc3QtMQ----aorAAAAAGsC6cogp3G-cXSOdzNpZdGzR
```

---

## 📊 添加结果

### 成功添加

```
✅ 成功添加 3 个账号，当前共 3 个
```

### 部分成功

```
✅ 成功添加 2 个账号，当前共 5 个
⚠️  跳过 1 个重复账号
```

### 失败提示

```
❌ 未解析到有效账号或所有账号已存在
```

---

## ❓ 常见问题

### Q1: 为什么提示 "未解析到有效账号"？

**可能原因**:
1. 分隔符不是 `----` (4个短横线)
2. 字段数量不足（少于4个）
3. 邮箱格式错误（不包含 @）
4. 所有账号都已存在

**解决方法**:
1. 检查分隔符是否正确
2. 确认有 4 个字段
3. 检查邮箱格式
4. 尝试添加新账号

### Q2: 如何确认 ClientID 和 RefreshToken 的顺序？

**不用确认！** 系统会自动识别：
- ClientID 通常包含 `nVzLWVhc3QtMQ`
- RefreshToken 通常以 `aorAAAAAG` 开头

两种顺序都可以，系统会自动识别并正确分配。

### Q3: 添加后在哪里查看账号？

1. 在"账号管理"页面查看
2. 账号列表会显示：
   - 邮箱地址
   - 添加时间
   - 注册状态
   - 操作按钮（删除等）

### Q4: 如何删除账号？

1. 进入"账号管理"页面
2. 找到要删除的账号
3. 点击"删除"按钮
4. 确认删除

### Q5: Web 版本的数据存在哪里？

Web 版本使用浏览器的 `localStorage` 存储数据：
- 数据存储在浏览器本地
- 清除浏览器数据会删除账号
- 不同浏览器的数据不互通

### Q6: 如何备份账号数据？

**方法1: 导出到文件**
1. 进入"账号管理"页面
2. 点击"导出"按钮
3. 选择导出格式
4. 保存文件

**方法2: 手动备份**
1. F12 打开开发者工具
2. 进入 Console 控制台
3. 输入: `localStorage.getItem('kiro_outlook_accounts')`
4. 复制输出内容保存

---

## 🔧 故障排查

### 1. 功能不可用

**症状**: 点击"添加账号"按钮无反应

**解决步骤**:
1. 刷新页面 (Ctrl+F5 或 Cmd+Shift+R)
2. 清除浏览器缓存
3. 检查浏览器控制台错误 (F12)

### 2. 提示 "addoutlookaccouts is not a function"

**原因**: JavaScript 文件未正确加载

**解决方法**:
1. 硬刷新浏览器 (Ctrl+Shift+F5)
2. 检查网络请求 (F12 → Network)
3. 确认 `adapter.js` 正确加载

### 3. 添加后看不到账号

**可能原因**:
1. 账号已存在（被跳过）
2. 格式错误（未添加成功）
3. 页面未刷新

**解决方法**:
1. 检查添加结果提示
2. 刷新账号列表
3. 检查浏览器控制台

### 4. 账号格式总是错误

**检查清单**:
- [ ] 分隔符是 `----` (4个短横线)
- [ ] 有 4 个字段
- [ ] 邮箱包含 `@`
- [ ] ClientID 不为空
- [ ] RefreshToken 不为空

---

## 📚 相关文档

- [BUILD_AND_RUN.md](BUILD_AND_RUN.md) - 编译运行指南
- [WEB_VERSION_GUIDE.md](WEB_VERSION_GUIDE.md) - Web 版本完整指南
- [QUICKSTART.md](QUICKSTART.md) - 快速开始
- [PROXY_BATCH_ADD.md](PROXY_BATCH_ADD.md) - 代理池批量添加

---

## 💻 开发者信息

### 数据存储

```javascript
// 获取账号列表
const accounts = JSON.parse(localStorage.getItem('kiro_outlook_accounts') || '[]');

// 保存账号
localStorage.setItem('kiro_outlook_accounts', JSON.stringify(accounts));

// 清空账号
localStorage.removeItem('kiro_outlook_accounts');
```

### 账号数据结构

```javascript
{
  email: "test@outlook.com",
  password: "password123",
  clientId: "ncGq-IBFli-nVzLWVhc3QtMQ",
  refreshToken: "aorAAAAAGsC6aEcPR",
  registered: false,
  success: false,
  addedAt: "2026-08-24T22:30:00.000Z"
}
```

### API 调用示例

```javascript
// 添加账号
const result = await window.go.main.App.AddOutlookAccounts(
  "test@outlook.com----pass----cid----rt"
);

// 获取账号列表
const accounts = await window.go.main.App.GetOutlookAccounts();

// 删除账号
await window.go.main.App.DeleteOutlookAccount("test@outlook.com");

// 清空所有账号
await window.go.main.App.ClearOutlookAccounts();
```

---

## 🎉 总结

Outlook 账号添加功能已完全修复，支持：

✅ 单个/批量添加  
✅ 自动格式识别  
✅ 注释支持  
✅ 去重保护  
✅ 详细错误提示  

现在就开始使用吧！🚀

---

**更新日期**: 2026-08-24  
**版本**: v1.0  
**适用于**: Web 版本
