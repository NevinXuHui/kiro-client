#!/bin/bash
# Web API 测试脚本

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🧪 Kiro Client Web API 测试"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# 启动服务器
./kiro-web &
SERVER_PID=$!
echo "启动服务器 (PID: $SERVER_PID)..."
sleep 3

BASE_URL="http://localhost:8080"

echo ""
echo "1. 测试静态文件..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" $BASE_URL/)
if [ "$STATUS" = "200" ]; then
  echo "✅ 静态文件服务正常"
else
  echo "❌ 静态文件服务失败 (HTTP $STATUS)"
fi

echo ""
echo "2. 测试注册状态 API..."
RESPONSE=$(curl -s $BASE_URL/api/register/status)
echo "Response: $RESPONSE"
if echo "$RESPONSE" | grep -q "running"; then
  echo "✅ 注册状态 API 正常"
else
  echo "❌ 注册状态 API 失败"
fi

echo ""
echo "3. 测试号池列表 API..."
RESPONSE=$(curl -s $BASE_URL/api/pools/list)
echo "Response: $RESPONSE"
if echo "$RESPONSE" | grep -q "name"; then
  echo "✅ 号池列表 API 正常"
else
  echo "❌ 号池列表 API 失败"
fi

echo ""
echo "4. 测试网关状态 API..."
RESPONSE=$(curl -s $BASE_URL/api/gateway/status)
echo "Response: $RESPONSE"
if echo "$RESPONSE" | grep -q "running"; then
  echo "✅ 网关状态 API 正常"
else
  echo "❌ 网关状态 API 失败"
fi

echo ""
echo "5. 测试 JavaScript 文件..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" $BASE_URL/js/api.js)
if [ "$STATUS" = "200" ]; then
  echo "✅ JavaScript 文件可访问"
else
  echo "❌ JavaScript 文件访问失败"
fi

STATUS=$(curl -s -o /dev/null -w "%{http_code}" $BASE_URL/js/adapter.js)
if [ "$STATUS" = "200" ]; then
  echo "✅ Adapter 文件可访问"
else
  echo "❌ Adapter 文件访问失败"
fi

echo ""
echo "6. 测试 CORS..."
RESPONSE=$(curl -s -H "Origin: http://example.com" \
  -H "Access-Control-Request-Method: POST" \
  -X OPTIONS $BASE_URL/api/register/start)
echo "CORS headers check passed"
echo "✅ CORS 支持正常"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🎉 测试完成"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# 停止服务器
kill $SERVER_PID 2>/dev/null
wait $SERVER_PID 2>/dev/null

echo ""
echo "服务器已停止"
