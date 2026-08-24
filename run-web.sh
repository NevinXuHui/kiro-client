#!/bin/bash

# Kiro Client Web 版本运行脚本
# 支持自动编译和参数配置

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 默认参数
HOST="localhost"
PORT="8080"
FORCE_BUILD=false
SKIP_BUILD=false

# 显示帮助信息
show_help() {
    cat << HELP
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🚀 Kiro Client Web 版本运行脚本
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

用法: $0 [选项]

选项:
  -h HOST        指定监听地址 (默认: localhost)
  -p PORT        指定监听端口 (默认: 8080)
  -b, --build    强制重新编译
  -s, --skip     跳过编译，直接运行
  --help         显示此帮助信息

示例:
  $0                          # 使用默认配置运行
  $0 -h 0.0.0.0 -p 8080       # 监听所有网卡，端口 8080
  $0 -b                       # 强制重新编译后运行
  $0 -s -h 0.0.0.0            # 跳过编译，使用现有二进制文件

说明:
  - 默认会检查二进制文件是否存在，不存在则自动编译
  - 检查源文件是否有更新，有更新则自动重新编译
  - 使用 -b 选项可以强制重新编译
  - 使用 -s 选项可以跳过编译检查

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
HELP
}

# 解析命令行参数
while [[ $# -gt 0 ]]; do
    case $1 in
        -h)
            HOST="$2"
            shift 2
            ;;
        -p)
            PORT="$2"
            shift 2
            ;;
        -b|--build)
            FORCE_BUILD=true
            shift
            ;;
        -s|--skip)
            SKIP_BUILD=true
            shift
            ;;
        --help)
            show_help
            exit 0
            ;;
        *)
            echo -e "${RED}❌ 未知选项: $1${NC}"
            show_help
            exit 1
            ;;
    esac
done

# 检查是否在项目根目录
if [ ! -f "go.mod" ]; then
    echo -e "${RED}❌ 错误: 请在项目根目录运行此脚本${NC}"
    exit 1
fi

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}🌐 Kiro Client Web 版本${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# 编译逻辑
BINARY="kiro-web"
NEED_BUILD=false

if [ "$SKIP_BUILD" = true ]; then
    echo -e "${YELLOW}⏭️  跳过编译检查${NC}"
    if [ ! -f "$BINARY" ]; then
        echo -e "${RED}❌ 错误: 二进制文件 $BINARY 不存在${NC}"
        echo -e "${YELLOW}提示: 请先运行 ./build.sh 或移除 -s 选项${NC}"
        exit 1
    fi
elif [ "$FORCE_BUILD" = true ]; then
    echo -e "${YELLOW}🔧 强制重新编译...${NC}"
    NEED_BUILD=true
elif [ ! -f "$BINARY" ]; then
    echo -e "${YELLOW}📦 二进制文件不存在，开始编译...${NC}"
    NEED_BUILD=true
else
    # 检查源文件是否有更新
    if find ./cmd/web ./internal -name "*.go" -newer "$BINARY" 2>/dev/null | grep -q .; then
        echo -e "${YELLOW}🔄 检测到源文件更新，重新编译...${NC}"
        NEED_BUILD=true
    else
        echo -e "${GREEN}✅ 使用现有二进制文件${NC}"
    fi
fi

# 执行编译
if [ "$NEED_BUILD" = true ]; then
    echo -e "${BLUE}📝 编译配置:${NC}"
    echo -e "   目标: Web 版本"
    echo -e "   输出: $BINARY"
    echo ""

    # 清理旧文件
    if [ -f "$BINARY" ]; then
        rm -f "$BINARY"
        echo -e "${GREEN}🗑️  清理旧文件${NC}"
    fi

    # 编译
    echo -e "${BLUE}⚙️  开始编译...${NC}"
    if go build -o "$BINARY" ./cmd/web/; then
        echo -e "${GREEN}✅ 编译成功${NC}"

        # 显示文件信息
        if command -v ls &> /dev/null; then
            FILE_SIZE=$(ls -lh "$BINARY" 2>/dev/null | awk '{print $5}')
            if [ -n "$FILE_SIZE" ]; then
                echo -e "${BLUE}📊 文件大小: ${FILE_SIZE}${NC}"
            fi
        fi
    else
        echo -e "${RED}❌ 编译失败${NC}"
        exit 1
    fi
    echo ""
fi

# 显示运行配置
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}🎯 运行配置${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "   监听地址: ${GREEN}$HOST${NC}"
echo -e "   监听端口: ${GREEN}$PORT${NC}"
echo -e "   访问地址: ${GREEN}http://$HOST:$PORT${NC}"

# 获取局域网 IP
if [ "$HOST" = "0.0.0.0" ]; then
    if command -v ifconfig &> /dev/null; then
        LOCAL_IP=$(ifconfig | grep "inet " | grep -v 127.0.0.1 | awk '{print $2}' | head -1)
        if [ -n "$LOCAL_IP" ]; then
            echo -e "   局域网访问: ${GREEN}http://$LOCAL_IP:$PORT${NC}"
        fi
    fi
fi
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# 显示使用提示
echo -e "${YELLOW}💡 使用提示:${NC}"
echo -e "   1. 浏览器访问: ${GREEN}http://$HOST:$PORT${NC}"
echo -e "   2. 按 ${YELLOW}Ctrl+C${NC} 停止服务器"
echo -e "   3. 查看日志可以了解运行状态"
echo ""

# 启动服务器
echo -e "${GREEN}🚀 启动服务器...${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# 设置信号处理
trap 'echo ""; echo -e "${YELLOW}🛑 正在停止服务器...${NC}"; kill $SERVER_PID 2>/dev/null; wait $SERVER_PID 2>/dev/null; echo -e "${GREEN}✅ 服务器已停止${NC}"; exit 0' INT TERM

# 运行服务器
./"$BINARY" -host "$HOST" -port "$PORT" &
SERVER_PID=$!

# 等待服务器启动
sleep 2

# 检查服务器是否运行
if ps -p $SERVER_PID > /dev/null 2>&1; then
    echo -e "${GREEN}✅ 服务器运行中 (PID: $SERVER_PID)${NC}"
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${YELLOW}📊 服务器日志:${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""

    # 等待服务器进程
    wait $SERVER_PID
else
    echo -e "${RED}❌ 服务器启动失败${NC}"
    echo -e "${YELLOW}提示: 请检查端口 $PORT 是否被占用${NC}"
    exit 1
fi
