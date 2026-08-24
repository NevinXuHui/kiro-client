# 🚀 代理池批量添加功能文档

## 📋 功能说明

代理池批量添加功能允许一次性添加多个代理地址，大大提高配置效率。

## ✨ 核心特性

- ✅ 批量添加多个代理
- ✅ 自动跳过空行和注释
- ✅ 智能去重（跳过重复）
- ✅ 统一权重设置
- ✅ 详细结果报告

## 🎮 使用方式

### GUI 版本

1. 进入"设置"页面
2. 找到"代理池"部分
3. 点击"批量添加"按钮
4. 输入代理地址（每行一个）
5. 设置权重（1-100，推荐 50）
6. 点击"添加"

**示例输入**:
\`\`\`
http://proxy1.example.com:8080
http://user:pass@proxy2.example.com:8080
socks5://proxy3.example.com:1080

# 注释行会被跳过
http://proxy4.example.com:3128
\`\`\`

### Web 版本

与 GUI 版本相同，通过浏览器访问。

### API 调用

\`\`\`javascript
// Wails API
const result = await window.go.main.App.BatchAddProxyEntries([
  'http://proxy1.example.com:8080',
  'http://proxy2.example.com:8080'
], 50);
\`\`\`

\`\`\`bash
# REST API
curl -X POST http://localhost:8080/api/proxy/batch-add \
  -H "Content-Type: application/json" \
  -d '{"urls":["http://p1.com:8080"],"weight":50}'
\`\`\`

## 📊 返回结果

\`\`\`json
{
  "success": 3,
  "failed": 0,
  "skipped": 1,
  "total": 4,
  "added": ["http://proxy1..."],
  "errors": []
}
\`\`\`

## 💡 使用技巧

1. **从文件导入**: 复制代理列表文件内容
2. **使用注释分组**: 用 # 或 // 添加注释
3. **混合格式**: 支持 http/https/socks5
4. **权重建议**: 主要 80-100, 一般 40-60, 备用 10-30

## 🧪 测试场景

**场景 1: 正常添加**
- 输入 3 个不同代理
- 预期: 成功 3, 失败 0, 跳过 0

**场景 2: 包含重复**
- 输入包含 1 个重复代理
- 预期: 成功 2, 失败 0, 跳过 1

**场景 3: 包含注释**
- 输入包含注释行
- 预期: 注释被跳过，不计入总数

---

**更新日期**: 2026-08-24  
**版本**: v1.0
