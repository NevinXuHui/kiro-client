package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"reg_go/internal/task"
)

// RegisterView 注册机界面
type RegisterView struct {
	// 表单输入
	inputs      []textinput.Model
	focusIndex  int
	emailSource int // 0=Outlook, 1=CloudMail, 2=HttpAPI

	// 任务状态
	running      bool
	taskProgress TaskProgress
	logs         []LogEntry

	// 窗口尺寸
	width  int
	height int
}

// TaskProgress 任务进度
type TaskProgress struct {
	Total     int
	Completed int
	Success   int
	Failed    int
	Current   string
}

// LogEntry 日志条目
type LogEntry struct {
	Time    time.Time
	Level   string // success, error, warning, info
	Message string
}

// NewRegisterView 创建注册机界面
func NewRegisterView() *RegisterView {
	// 创建输入框
	inputs := make([]textinput.Model, 3)

	// 数量输入
	inputs[0] = textinput.New()
	inputs[0].Placeholder = "10"
	inputs[0].Focus()
	inputs[0].CharLimit = 3
	inputs[0].Width = 20
	inputs[0].Prompt = ""

	// 并发数输入
	inputs[1] = textinput.New()
	inputs[1].Placeholder = "3"
	inputs[1].CharLimit = 2
	inputs[1].Width = 20
	inputs[1].Prompt = ""

	// 延时输入
	inputs[2] = textinput.New()
	inputs[2].Placeholder = "5"
	inputs[2].CharLimit = 4
	inputs[2].Width = 20
	inputs[2].Prompt = ""

	return &RegisterView{
		inputs:      inputs,
		focusIndex:  0,
		emailSource: 1, // 默认 CloudMail
		logs:        make([]LogEntry, 0),
	}
}

// Update 更新注册机界面
func (v *RegisterView) Update(msg tea.Msg) (*RegisterView, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab", "up", "down":
			// 切换焦点
			if msg.String() == "up" || msg.String() == "shift+tab" {
				v.focusIndex--
			} else {
				v.focusIndex++
			}

			if v.focusIndex > len(v.inputs) {
				v.focusIndex = 0
			} else if v.focusIndex < 0 {
				v.focusIndex = len(v.inputs)
			}

			// 更新输入框焦点
			for i := 0; i < len(v.inputs); i++ {
				if i == v.focusIndex {
					cmds = append(cmds, v.inputs[i].Focus())
				} else {
					v.inputs[i].Blur()
				}
			}

			return v, tea.Batch(cmds...)

		case "enter":
			if v.focusIndex == len(v.inputs) {
				// 开始/停止注册按钮
				if !v.running {
					return v, v.startTask()
				} else {
					// 停止任务
					v.stopTask()
				}
			}

		case "1", "2", "3":
			// 切换邮箱源（仅在不聚焦输入框时）
			if v.focusIndex >= len(v.inputs) {
				if msg.String() == "1" {
					v.emailSource = 0
				} else if msg.String() == "2" {
					v.emailSource = 1
				} else if msg.String() == "3" {
					v.emailSource = 2
				}
			}

		case "left", "right", "h", "l":
			// 左右键切换邮箱源
			if msg.String() == "left" || msg.String() == "h" {
				v.emailSource--
				if v.emailSource < 0 {
					v.emailSource = 2
				}
			} else {
				v.emailSource++
				if v.emailSource > 2 {
					v.emailSource = 0
				}
			}
		}

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height

	case TaskProgressMsg:
		v.taskProgress = TaskProgress(msg)

	case LogMsg:
		v.addLog(LogEntry(msg))
	}

	// 更新当前焦点的输入框
	if v.focusIndex < len(v.inputs) {
		var cmd tea.Cmd
		v.inputs[v.focusIndex], cmd = v.inputs[v.focusIndex].Update(msg)
		cmds = append(cmds, cmd)
	}

	return v, tea.Batch(cmds...)
}

// View 渲染注册机界面
func (v *RegisterView) View() string {
	var s strings.Builder

	// 标题
	s.WriteString(RenderTitle("注册机") + "\n\n")

	// 配置表单
	s.WriteString(v.renderForm() + "\n\n")

	// 进度显示
	if v.running || v.taskProgress.Total > 0 {
		s.WriteString(v.renderProgress() + "\n\n")
	}

	// 日志
	if len(v.logs) > 0 {
		s.WriteString(v.renderLogs() + "\n\n")
	}

	// 帮助
	help := "Tab: 切换  ←/→: 切换邮箱源  Enter: 开始/停止  ESC: 返回  q: 退出"
	s.WriteString(RenderHelp(help))

	return s.String()
}

// renderForm 渲染配置表单
func (v *RegisterView) renderForm() string {
	var s strings.Builder

	// 邮箱源选择（带高亮边框）
	emailSourceBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		MarginBottom(1)

	var sourcesStr strings.Builder
	sourcesStr.WriteString(lipgloss.NewStyle().Bold(true).Render("邮箱源 (←/→ 切换):") + "\n\n")
	sources := []string{"Outlook", "CloudMail", "HttpAPI"}
	for i, src := range sources {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == v.emailSource {
			prefix = "▶ "
			style = style.Foreground(ColorPrimary).Bold(true)
		} else {
			prefix = "  "
			style = style.Foreground(ColorMuted)
		}
		s.WriteString(prefix + style.Render(fmt.Sprintf("[%d] %s", i+1, src)) + "\n")
	}
	s.WriteString(emailSourceBox.Render(sourcesStr.String()) + "\n")
	s.WriteString("\n")

	// 配置输入
	labels := []string{"数量", "并发", "延时(秒)"}
	for i, label := range labels {
		focused := v.focusIndex == i
		style := InputStyle
		if focused {
			style = InputFocusStyle
			label = "▶ " + label
		} else {
			label = "  " + label
		}

		s.WriteString(lipgloss.NewStyle().Width(12).Render(label) + " ")
		s.WriteString(style.Render(v.inputs[i].View()) + "\n")
	}

	// 按钮
	s.WriteString("\n")
	buttonLabel := "开始注册"
	if v.running {
		buttonLabel = "停止任务"
	}

	buttonFocused := v.focusIndex == len(v.inputs)
	if buttonFocused {
		s.WriteString("  ▶ " + ButtonStyle.Render(buttonLabel) + "\n")
	} else {
		s.WriteString("    " + lipgloss.NewStyle().
			Foreground(ColorMuted).
			Render(buttonLabel) + "\n")
	}

	return BorderStyle.Render(s.String())
}

// renderProgress 渲染进度
func (v *RegisterView) renderProgress() string {
	var s strings.Builder

	p := v.taskProgress

	// 标题
	s.WriteString(lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("进度 (%d/%d)", p.Completed, p.Total)) + "\n\n")

	// 进度条
	if p.Total > 0 {
		barWidth := 40
		percent := float64(p.Completed) / float64(p.Total)
		filled := int(float64(barWidth) * percent)
		empty := barWidth - filled

		bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
		percentText := fmt.Sprintf("%.1f%%", percent*100)

		s.WriteString(ProgressBarStyle.Render(bar) + " " + percentText + "\n\n")
	}

	// 统计
	s.WriteString(fmt.Sprintf("✓ 成功: %s  ", SuccessStyle.Render(fmt.Sprintf("%d", p.Success))))
	s.WriteString(fmt.Sprintf("✗ 失败: %s  ", ErrorStyle.Render(fmt.Sprintf("%d", p.Failed))))
	s.WriteString(fmt.Sprintf("↻ 进行中: %d", p.Total-p.Completed))
	s.WriteString("\n")

	// 当前任务
	if p.Current != "" {
		s.WriteString("\n")
		s.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Render(
			"当前: " + p.Current))
	}

	return BorderStyle.Render(s.String())
}

// renderLogs 渲染日志
func (v *RegisterView) renderLogs() string {
	var s strings.Builder

	s.WriteString(lipgloss.NewStyle().Bold(true).Render("日志") + "\n\n")

	// 显示最近 10 条日志
	start := 0
	if len(v.logs) > 10 {
		start = len(v.logs) - 10
	}

	for i := start; i < len(v.logs); i++ {
		log := v.logs[i]
		timeStr := log.Time.Format("15:04:05")
		timeStyle := lipgloss.NewStyle().Foreground(ColorMuted)

		var icon, msgStyle string
		switch log.Level {
		case "success":
			icon = "✓"
			msgStyle = SuccessStyle.Render(log.Message)
		case "error":
			icon = "✗"
			msgStyle = ErrorStyle.Render(log.Message)
		case "warning":
			icon = "⚠"
			msgStyle = WarningStyle.Render(log.Message)
		default:
			icon = "•"
			msgStyle = log.Message
		}

		s.WriteString(fmt.Sprintf("%s %s %s\n",
			timeStyle.Render(timeStr), icon, msgStyle))
	}

	return BorderStyle.Render(s.String())
}

// startTask 开始注册任务
func (v *RegisterView) startTask() tea.Cmd {
	v.running = true
	v.taskProgress = TaskProgress{}
	v.logs = make([]LogEntry, 0)

	// 解析配置
	count, _ := strconv.Atoi(v.inputs[0].Value())
	if count == 0 {
		count = 10
	}

	concurrency, _ := strconv.Atoi(v.inputs[1].Value())
	if concurrency == 0 {
		concurrency = 3
	}

	delay, _ := strconv.Atoi(v.inputs[2].Value())
	if delay == 0 {
		delay = 5
	}

	// 邮箱提供商
	emailProvider := "cloudmail"
	switch v.emailSource {
	case 0:
		emailProvider = "outlook"
	case 2:
		emailProvider = "httpapi"
	}

	v.addLog(LogEntry{
		Time:    time.Now(),
		Level:   "info",
		Message: fmt.Sprintf("启动任务: %d个账号, 并发%d, 延时%d秒, 邮箱源: %s", count, concurrency, delay, emailProvider),
	})

	// 启动任务（异步）
	return func() tea.Msg {
		req := task.StartTaskRequest{
			Count:         count,
			Concurrency:   concurrency,
			Delay:         delay,
			EmailProvider: emailProvider,
		}

		// 启动任务
		go func() {
			result := task.StartTask(req)
			_ = result
		}()

		return TaskProgressMsg{
			Total:     count,
			Completed: 0,
			Success:   0,
			Failed:    0,
			Current:   "正在启动...",
		}
	}
}

// stopTask 停止任务
func (v *RegisterView) stopTask() {
	v.running = false
	task.StopTask(true) // 传递 wait=true 参数
	v.addLog(LogEntry{
		Time:    time.Now(),
		Level:   "warning",
		Message: "任务已停止",
	})
}

// addLog 添加日志
func (v *RegisterView) addLog(log LogEntry) {
	v.logs = append(v.logs, log)
	// 限制日志数量
	if len(v.logs) > 100 {
		v.logs = v.logs[len(v.logs)-100:]
	}
}

// TaskProgressMsg 任务进度消息
type TaskProgressMsg TaskProgress

// LogMsg 日志消息
type LogMsg LogEntry
