#!/bin/bash
# Kiro Client 一键编译脚本

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印函数
print_header() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_info() {
    echo -e "${YELLOW}📍 $1${NC}"
}

# 检测当前分支
CURRENT_BRANCH=$(git branch --show-current)
print_header "🔧 Kiro Client 一键编译脚本"
print_info "当前分支: $CURRENT_BRANCH"

# 根据分支选择编译目标
case "$CURRENT_BRANCH" in
    "main"|"master")
        print_header "🖥️  编译 GUI 版本 (Wails)"

        # 检查 Wails 是否安装
        if ! command -v wails &> /dev/null; then
            print_error "Wails 未安装"
            print_info "请运行: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
            exit 1
        fi

        # 编译
        print_info "开始编译..."
        wails build

        if [ $? -eq 0 ]; then
            print_success "编译成功"
            print_info "输出: build/bin/kiro-client.app"

            # 显示文件大小
            if [ -d "build/bin/kiro-client.app" ]; then
                SIZE=$(du -sh build/bin/kiro-client.app | cut -f1)
                print_info "大小: $SIZE"

                # 询问是否安装
                read -p "是否安装到 /Applications? (y/n) " -n 1 -r
                echo
                if [[ $REPLY =~ ^[Yy]$ ]]; then
                    cp -r build/bin/kiro-client.app /Applications/
                    print_success "已安装到 /Applications/kiro-client.app"
                fi
            fi
        else
            print_error "编译失败"
            exit 1
        fi
        ;;

    "tui-version")
        print_header "📺 编译 TUI 版本 (Bubble Tea)"

        # 编译
        print_info "开始编译..."
        go build -o kiro-tui ./cmd/tui/

        if [ $? -eq 0 ]; then
            print_success "编译成功"
            print_info "输出: ./kiro-tui"

            # 显示文件大小
            SIZE=$(du -sh kiro-tui | cut -f1)
            print_info "大小: $SIZE"

            # 赋予执行权限
            chmod +x kiro-tui
            print_success "已添加执行权限"
        else
            print_error "编译失败"
            exit 1
        fi
        ;;

    "web")
        print_header "🌐 编译 Web 版本 (HTTP Server)"

        # 编译
        print_info "开始编译..."
        go build -o kiro-web ./cmd/web/

        if [ $? -eq 0 ]; then
            print_success "编译成功"
            print_info "输出: ./kiro-web"

            # 显示文件大小
            SIZE=$(du -sh kiro-web | cut -f1)
            print_info "大小: $SIZE"

            # 赋予执行权限
            chmod +x kiro-web
            print_success "已添加执行权限"
        else
            print_error "编译失败"
            exit 1
        fi
        ;;

    *)
        print_error "未知分支: $CURRENT_BRANCH"
        print_info "支持的分支: main/master (GUI), tui-version (TUI), web (Web)"
        exit 1
        ;;
esac

print_header "✅ 编译完成"
