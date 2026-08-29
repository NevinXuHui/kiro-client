#!/bin/bash
# 测试导入验活功能

echo "=== 测试导入验活 API ==="

# 测试数据（无效的 token，用于测试验活逻辑）
TEST_DATA='[{
  "clientId": "test-client",
  "clientSecret": "test-secret",
  "refreshToken": "invalid-token-12345",
  "email": "test@example.com",
  "provider": "idc",
  "region": "us-east-1",
  "subscription": "Free",
  "creditLimit": 0,
  "creditUsed": 0
}]'

echo ""
echo "1. 测试不验活导入（原有功能）"
curl -sS -X POST http://127.0.0.1:9702/api/pool/import \
  -H "Content-Type: application/json" \
  -d "{\"data\": \"$TEST_DATA\", \"verify\": false}" | jq .

echo ""
echo "2. 测试验活导入（不过滤封禁账号）"
curl -sS -X POST http://127.0.0.1:9702/api/pool/import \
  -H "Content-Type: application/json" \
  -d "{\"data\": \"$TEST_DATA\", \"verify\": true, \"concurrency\": 1, \"skipSuspended\": false}" | jq .

echo ""
echo "3. 测试验活导入（过滤封禁账号）"
curl -sS -X POST http://127.0.0.1:9702/api/pool/import \
  -H "Content-Type: application/json" \
  -d "{\"data\": \"$TEST_DATA\", \"verify\": true, \"concurrency\": 1, \"skipSuspended\": true}" | jq .

echo ""
echo "=== 测试完成 ==="
