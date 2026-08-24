package tui

import "github.com/charmbracelet/lipgloss"

// 颜色定义
var (
	ColorPrimary   = lipgloss.Color("#2f80f5")
	ColorSuccess   = lipgloss.Color("#00b894")
	ColorWarning   = lipgloss.Color("#fdcb6e")
	ColorDanger    = lipgloss.Color("#d63031")
	ColorSecondary = lipgloss.Color("#636e72")
	ColorBorder    = lipgloss.Color("#dfe6e9")
	ColorText      = lipgloss.Color("#2d3436")
	ColorMuted     = lipgloss.Color("#b2bec3")
)

// 样式定义
var (
	// 标题样式
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			MarginBottom(1)

	// 子标题样式
	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorSecondary).
			MarginBottom(1)

	// 边框样式
	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(1, 2)

	// 激活项样式
	ActiveStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	// 成功样式
	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true)

	// 警告样式
	WarningStyle = lipgloss.NewStyle().
			Foreground(ColorWarning).
			Bold(true)

	// 错误样式
	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorDanger).
			Bold(true)

	// 禁用样式
	DisabledStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// 帮助样式
	HelpStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			MarginTop(1)

	// 状态栏样式
	StatusBarStyle = lipgloss.NewStyle().
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#ffffff")).
			Padding(0, 1)

	// 进度条样式
	ProgressBarStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary)

	// 输入框样式
	InputStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	// 输入框焦点样式
	InputFocusStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1)

	// 按钮样式
	ButtonStyle = lipgloss.NewStyle().
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#ffffff")).
			Padding(0, 2).
			MarginRight(1)

	// 按钮禁用样式
	ButtonDisabledStyle = lipgloss.NewStyle().
				Background(ColorMuted).
				Foreground(lipgloss.Color("#ffffff")).
				Padding(0, 2).
				MarginRight(1)
)

// RenderTitle 渲染标题
func RenderTitle(title string) string {
	return TitleStyle.Render("🔧 " + title)
}

// RenderSubtitle 渲染子标题
func RenderSubtitle(subtitle string) string {
	return SubtitleStyle.Render(subtitle)
}

// RenderSuccess 渲染成功消息
func RenderSuccess(msg string) string {
	return SuccessStyle.Render("✓ " + msg)
}

// RenderWarning 渲染警告消息
func RenderWarning(msg string) string {
	return WarningStyle.Render("⚠ " + msg)
}

// RenderError 渲染错误消息
func RenderError(msg string) string {
	return ErrorStyle.Render("✗ " + msg)
}

// RenderHelp 渲染帮助文本
func RenderHelp(help string) string {
	return HelpStyle.Render(help)
}

// RenderButton 渲染按钮
func RenderButton(label string, disabled bool) string {
	if disabled {
		return ButtonDisabledStyle.Render(label)
	}
	return ButtonStyle.Render(label)
}

// RenderProgressBar 渲染进度条
func RenderProgressBar(current, total int, width int) string {
	if total == 0 {
		return ""
	}

	percent := float64(current) / float64(total)
	filled := int(float64(width) * percent)
	empty := width - filled

	bar := ""
	for i := 0; i < filled; i++ {
		bar += "█"
	}
	for i := 0; i < empty; i++ {
		bar += "░"
	}

	percentStr := lipgloss.NewStyle().
		Foreground(ColorPrimary).
		Render(lipgloss.NewStyle().Width(5).Align(lipgloss.Right).Render(
			lipgloss.NewStyle().Bold(true).Render(
				lipgloss.NewStyle().Render(
					lipgloss.NewStyle().Render(
						lipgloss.NewStyle().Render(
							lipgloss.NewStyle().Render(
								lipgloss.NewStyle().Render(
									lipgloss.NewStyle().Render(
										lipgloss.NewStyle().Render(
											lipgloss.NewStyle().Render(
												lipgloss.NewStyle().Render(
													lipgloss.NewStyle().Render(
														lipgloss.NewStyle().Render(
															lipgloss.NewStyle().Render(
																lipgloss.NewStyle().Render(
																	lipgloss.NewStyle().Render(
																		lipgloss.NewStyle().Render(
																			lipgloss.NewStyle().Render(
																				lipgloss.NewStyle().Render(
																					lipgloss.NewStyle().Render(
																						lipgloss.NewStyle().Render("")))))))))))))))))))))

	return ProgressBarStyle.Render(bar) + " " + percentStr
}
