package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Screen 表示当前显示的界面
type Screen int

const (
	ScreenMenu Screen = iota
	ScreenRegister
	ScreenPool
	ScreenGateway
	ScreenSettings
)

// App 是 TUI 应用的主模型
type App struct {
	screen   Screen
	width    int
	height   int
	quitting bool

	// 各个界面的状态
	register *RegisterView
	pool     *PoolModel
	gateway  *GatewayModel
	settings *SettingsModel

	// 菜单选择
	menuCursor int
}

// NewApp 创建新的 TUI 应用
func NewApp() App {
	return App{
		screen:     ScreenMenu,
		menuCursor: 0,
		register:   NewRegisterView(),
		pool:       NewPoolModel(),
		gateway:    NewGatewayModel(),
		settings:   NewSettingsModel(),
	}
}

// Init 初始化应用
func (a App) Init() tea.Cmd {
	return nil
}

// Update 处理消息并更新状态
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if a.screen == ScreenMenu {
				a.quitting = true
				return a, tea.Quit
			}
			// 其他界面按 q 返回主菜单
			a.screen = ScreenMenu
			return a, nil

		case "esc":
			// ESC 返回主菜单
			a.screen = ScreenMenu
			return a, nil
		}

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
	}

	// 根据当前界面分发消息
	var cmd tea.Cmd
	switch a.screen {
	case ScreenMenu:
		a, cmd = a.updateMenu(msg)
	case ScreenRegister:
		a.register, cmd = a.register.Update(msg)
	case ScreenPool:
		a.pool, cmd = a.pool.Update(msg)
	case ScreenGateway:
		a.gateway, cmd = a.gateway.Update(msg)
	case ScreenSettings:
		a.settings, cmd = a.settings.Update(msg)
	}

	return a, cmd
}

// updateMenu 处理主菜单的消息
func (a App) updateMenu(msg tea.Msg) (App, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if a.menuCursor > 0 {
				a.menuCursor--
			}
		case "down", "j":
			if a.menuCursor < 4 {
				a.menuCursor++
			}
		case "enter", " ":
			// 选择菜单项
			switch a.menuCursor {
			case 0:
				a.screen = ScreenRegister
			case 1:
				a.screen = ScreenPool
			case 2:
				a.screen = ScreenGateway
			case 3:
				a.screen = ScreenSettings
			case 4:
				a.quitting = true
				return a, tea.Quit
			}
		}
	}
	return a, nil
}

// View 渲染界面
func (a App) View() string {
	if a.quitting {
		return RenderSuccess("Bye!") + "\n"
	}

	switch a.screen {
	case ScreenMenu:
		return a.viewMenu()
	case ScreenRegister:
		return a.register.View()
	case ScreenPool:
		return a.pool.View()
	case ScreenGateway:
		return a.gateway.View()
	case ScreenSettings:
		return a.settings.View()
	default:
		return "Unknown screen"
	}
}

// viewMenu 渲染主菜单
func (a App) viewMenu() string {
	var s strings.Builder

	// 标题
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Padding(1, 0).
		Render("🔧 Kiro Client - TUI")

	s.WriteString(title + "\n\n")

	// 菜单项
	menuItems := []struct {
		icon string
		name string
		desc string
	}{
		{"📝", "注册机", "批量注册 AWS Builder ID 账号"},
		{"📊", "号池管理", "查看和管理已注册的账号"},
		{"🌐", "网关控制", "启动/停止 Kiro 本地网关"},
		{"⚙️", "设置", "配置代理池、域名池等"},
		{"🚪", "退出", "退出程序"},
	}

	for i, item := range menuItems {
		cursor := "  "
		style := lipgloss.NewStyle()

		if a.menuCursor == i {
			cursor = "▶ "
			style = style.
				Bold(true).
				Foreground(ColorPrimary).
				Background(lipgloss.Color("#e8f4ff")).
				Padding(0, 1)
		}

		line := fmt.Sprintf("%s %s", item.icon, item.name)
		desc := lipgloss.NewStyle().Foreground(ColorMuted).Render(" - " + item.desc)

		if a.menuCursor == i {
			line = style.Render(line + desc)
		} else {
			line = line + desc
		}

		s.WriteString(cursor + line + "\n")
	}

	s.WriteString("\n")

	// 帮助
	help := RenderHelp("↑/↓: 导航  Enter: 选择  Ctrl+C/q: 退出")
	s.WriteString(help)

	// 边框
	content := BorderStyle.Render(s.String())

	return content
}

// PoolModel 号池管理界面模型
type PoolModel struct{}

func NewPoolModel() *PoolModel {
	return &PoolModel{}
}

func (m *PoolModel) Update(msg tea.Msg) (*PoolModel, tea.Cmd) {
	return m, nil
}

func (m *PoolModel) View() string {
	var s strings.Builder
	s.WriteString(RenderTitle("号池管理") + "\n\n")
	s.WriteString("号池管理功能正在开发中...\n\n")
	s.WriteString(RenderHelp("ESC: 返回主菜单  q: 退出"))
	return BorderStyle.Render(s.String())
}

// GatewayModel 网关控制界面模型
type GatewayModel struct{}

func NewGatewayModel() *GatewayModel {
	return &GatewayModel{}
}

func (m *GatewayModel) Update(msg tea.Msg) (*GatewayModel, tea.Cmd) {
	return m, nil
}

func (m *GatewayModel) View() string {
	var s strings.Builder
	s.WriteString(RenderTitle("网关控制") + "\n\n")
	s.WriteString("网关控制功能正在开发中...\n\n")
	s.WriteString(RenderHelp("ESC: 返回主菜单  q: 退出"))
	return BorderStyle.Render(s.String())
}

// SettingsModel 设置界面模型
type SettingsModel struct{}

func NewSettingsModel() *SettingsModel {
	return &SettingsModel{}
}

func (m *SettingsModel) Update(msg tea.Msg) (*SettingsModel, tea.Cmd) {
	return m, nil
}

func (m *SettingsModel) View() string {
	var s strings.Builder
	s.WriteString(RenderTitle("设置") + "\n\n")
	s.WriteString("设置功能正在开发中...\n\n")
	s.WriteString(RenderHelp("ESC: 返回主菜单  q: 退出"))
	return BorderStyle.Render(s.String())
}
