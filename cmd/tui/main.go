package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"reg_go/internal/tui"
)

func main() {
	// 创建 TUI 应用
	p := tea.NewProgram(
		tui.NewApp(),
		tea.WithAltScreen(),       // 使用备用屏幕缓冲区
		tea.WithMouseCellMotion(), // 启用鼠标支持
	)

	// 运行应用
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
