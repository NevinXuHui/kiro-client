package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/energye/systray"

	"reg_go/internal/core"
	"reg_go/internal/data"
	"reg_go/internal/email"
	"reg_go/internal/pool"
	"reg_go/internal/proxy"
	"reg_go/internal/reverseproxy"
	"reg_go/internal/storage"
	"reg_go/internal/subscription"
	"reg_go/internal/task"
)

type App struct {
	ctx         context.Context
	proxyServer *reverseproxy.ProxyServer

	// 手动注册模式：串行执行，防止并发弹出多个浏览器窗口
	manualMu  sync.Mutex
	manualRun bool

	// 号池自动健康刷新：healthMu 与手动刷新互斥（防 race/风暴），healthStop 停止信号
	healthMu   sync.Mutex
	healthStop chan struct{}
}

func NewApp() *App { return &App{} }

// startup — Wails 生命周期：窗口创建后调用
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.Init()
	// 窗口与 ZCode 主窗口同位置（83,0），方便对照
	wailsRuntime.WindowSetPosition(ctx, 83, 0)
	// 懒初始化本地网关（不启动，默认关闭；需用时由设置页"开启网关"按钮启动）
	a.ensureProxyServer()
	// 从号池同步账号到网关（去重）
	_ = a.syncGatewayAccounts()
	// 号池自动健康刷新（增量，后台常驻）
	a.healthStop = make(chan struct{})
	go a.autoHealthRefreshLoop()
	// 系统托盘：关窗口隐藏到托盘，托盘菜单可恢复窗口或退出
	go a.initTray()
}

// initTray 启动系统托盘（energye/systray，Wails v2 无内置托盘 API）
func (a *App) initTray() {
	systray.Run(func() {
		systray.SetIcon(appIconBytes)
		systray.SetTitle("Kiro Client")
		systray.SetTooltip("Kiro Client")
		show := systray.AddMenuItem("显示窗口", "显示主窗口")
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出", "退出 Kiro Client")
		show.Click(func() { a.ShowWindow() })
		quit.Click(func() {
			systray.Quit()
			wailsRuntime.Quit(a.ctx)
		})
	}, func() {})
}

// shutdown — Wails 生命周期：窗口关闭前调用
func (a *App) shutdown(ctx context.Context) {
	if a.healthStop != nil {
		close(a.healthStop)
	}
	if a.proxyServer != nil && a.proxyServer.IsRunning() {
		a.proxyServer.Stop()
	}
}

func (a *App) Init() {
	log.SetOutput(&logWriter{})
	log.SetFlags(log.Ltime)
	proxy.InitPool(storage.GetDataDir())
	email.InitDomainPool(storage.GetDataDir())
}

type logWriter struct{}

func (w *logWriter) Write(p []byte) (int, error) {
	task.Manager.AppendLog(string(p))
	return os.Stderr.Write(p)
}

// ===== System =====

// ShowWindow 从托盘恢复窗口（前端双击/点击事件调用）
func (a *App) ShowWindow() {
	if a.ctx != nil {
		wailsRuntime.WindowShow(a.ctx)
		wailsRuntime.WindowUnminimise(a.ctx)
	}
}

// IsHidden 是否处于隐藏状态（最小化到托盘）
func (a *App) IsHidden() bool {
	return false // Wails 没有直接 API，留作占位
}

func (a *App) OpenURL(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

// SelectDirectory — Wails 原生目录选择对话框
func (a *App) SelectDirectory() string {
	path, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择目录",
	})
	if err != nil || path == "" {
		return ""
	}
	return path
}

// SelectOutlookFile — Wails 原生文件选择对话框
func (a *App) SelectOutlookFile() string {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 Outlook 账号文件",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "文本文件", Pattern: "*.txt;*.csv;*.json"},
			{Pattern: "*"},
		},
	})
	if err != nil || path == "" {
		return ""
	}
	return path
}

// ===== Status =====

func (a *App) GetStatus() map[string]interface{} { return task.Manager.GetStatus() }
func (a *App) GetLogs() []string                 { return task.Manager.GetLogs() }
func (a *App) GetTaskStatus() map[string]interface{} {
	return map[string]interface{}{"kiro": task.Manager.GetStatus()}
}

func (a *App) GetOverview() map[string]interface{} {
	outlookTotal, outlookRegistered, outlookSuccess, outlookPending := countOutlookAccounts()
	ts := task.Manager.GetStatus()

	// 号池统计
	poolTotal, poolHealthy, poolUnhealthy, poolModels := countPoolAccounts()

	return map[string]interface{}{
		"version": "v0.0.0",
		"kiro":    ts,
		"outlook": map[string]interface{}{
			"total": outlookTotal, "registered": outlookRegistered,
			"success": outlookSuccess, "pending": outlookPending,
		},
		"pool": map[string]interface{}{
			"total":     poolTotal,
			"healthy":   poolHealthy,
			"unhealthy": poolUnhealthy,
			"models":    poolModels,
		},
	}
}

func countPoolAccounts() (total, healthy, unhealthy, models int) {
	mgr := pool.GetManager(storage.GetDataDir())
	pools := mgr.List()
	modelSet := map[string]bool{}
	seen := map[string]bool{}
	for _, p := range pools {
		for _, acc := range p.Accounts {
			// 同一账号出现在多个号池时只统计一次
			if seen[acc.Email] {
				continue
			}
			seen[acc.Email] = true
			total++
			if acc.HealthStatus == "healthy" {
				healthy++
			} else if acc.HealthStatus == "unhealthy" {
				unhealthy++
			}
			for _, m := range acc.AvailableModels {
				modelSet[m] = true
			}
		}
	}
	models = len(modelSet)
	return
}

func countOutlookAccounts() (total, registered, success, pending int) {
	accounts := storage.GetAccountsCached()
	if len(accounts) == 0 {
		return
	}
	total = len(accounts)
	for _, acc := range accounts {
		reg, _ := acc["registered"].(bool)
		suc, _ := acc["success"].(bool)
		if reg {
			registered++
			if suc {
				success++
			}
		} else {
			pending++
		}
	}
	return
}

// ===== Task =====

func (a *App) StartTask(raw string) map[string]interface{} {
	var req task.StartTaskRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return task.StartTask(req)
}

func (a *App) StopTask() map[string]interface{}              { return task.StopTask(false) }
func (a *App) ResetFingerprintCache() map[string]interface{} { return map[string]interface{}{} }

// ===== Manual Register（手动注册：设备授权 + 可见浏览器）=====

// StartManualRegister 启动手动注册。返回 {ok:true} 表示已开始；
// 结果通过日志流可见，成功后自动写盘 + 入库号池。
func (a *App) StartManualRegister() map[string]interface{} {
	a.manualMu.Lock()
	if a.manualRun {
		a.manualMu.Unlock()
		return map[string]interface{}{"error": "手动注册已在运行中"}
	}
	a.manualRun = true
	a.manualMu.Unlock()

	go func() {
		defer func() {
			a.manualMu.Lock()
			a.manualRun = false
			a.manualMu.Unlock()
		}()

		log.Println("[Kiro][手动] === 手动注册开始 ===")
		cfg := core.NewConfig()
		cfg.Proxy = storage.GetProxy()
		if cfg.Proxy != "" {
			log.Printf("[Kiro][手动] 使用代理: %s", cfg.Proxy)
		}

		reg := core.NewRegistrar(cfg)
		result := reg.ManualRegister()

		if result["status"] == "success" {
			// 写盘（accounts.json）+ 自动入库默认号池
			if err := data.SaveKiroSuccess(result, storage.GetResultOutputDir()); err != nil {
				log.Printf("[Kiro][手动] 保存结果失败: %v", err)
			}
			manualAddToPool(result)
			log.Printf("[Kiro][手动] === 手动注册成功，已入库 ===")
		} else {
			errMsg, _ := result["error"].(string)
			log.Printf("[Kiro][手动] === 手动注册失败: %s ===", errMsg)
		}
	}()
	return map[string]interface{}{"ok": true}
}

// GetManualRegisterStatus 手动注册运行状态。
func (a *App) GetManualRegisterStatus() map[string]interface{} {
	a.manualMu.Lock()
	defer a.manualMu.Unlock()
	return map[string]interface{}{"running": a.manualRun}
}

// manualAddToPool 手动注册成功后加入默认号池（与 task 包 autoAddToPool 同构）。
func manualAddToPool(result map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Kiro][手动] 入库异常: %v", r)
		}
	}()

	at, _ := result["aws_token"].(map[string]interface{})
	if at == nil {
		return
	}
	refreshToken, _ := at["refreshToken"].(string)
	if refreshToken == "" {
		return
	}
	clientID, _ := result["client_id"].(string)
	clientSecret, _ := result["client_secret"].(string)
	emailAddr, _ := result["email"].(string)
	if emailAddr == "" || clientID == "" || clientSecret == "" {
		return
	}

	acc := &pool.Account{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RefreshToken: refreshToken,
		Email:        emailAddr,
		Provider:     "BuilderId",
		Region:       "us-east-1",
		Time:         time.Now().Format("2006-01-02 15:04:05"),
	}
	if verify, ok := result["verify"].(map[string]interface{}); ok {
		if v, ok := verify["credit_used"].(float64); ok {
			acc.CreditUsed = int(v)
		}
		if v, ok := verify["credit_limit"].(float64); ok {
			acc.CreditLimit = int(v)
		}
		if v, ok := verify["subscription"].(string); ok {
			acc.Subscription = v
		}
	}

	mgr := pool.GetManager(storage.GetDataDir())
	pools := mgr.List()
	var defaultPool *pool.Pool
	for i := range pools {
		if pools[i].Name == "默认号池" {
			defaultPool = pools[i]
			break
		}
	}
	if defaultPool == nil {
		_, err := mgr.Create("默认号池", pool.StrategyRoundRobin, []*pool.Account{acc})
		if err != nil {
			log.Printf("[Kiro][手动] 创建默认号池失败: %v", err)
			return
		}
		log.Printf("[Kiro][手动] 入库: 创建默认号池并添加 %s", emailAddr)
		return
	}
	if err := mgr.AddAccountToPool(defaultPool.ID, acc); err != nil {
		log.Printf("[Kiro][手动] 入库失败: %v", err)
		return
	}
	log.Printf("[Kiro][手动] 入库: %s → 默认号池", emailAddr)
}

// ===== Config =====

func (a *App) GetDataDir() string { return storage.GetDataDir() }

func (a *App) SetDataDir(path string) map[string]interface{} {
	p, err := storage.SetDataDirPath(path)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"path": p}
}

func (a *App) ResetDataDir() map[string]interface{} {
	return map[string]interface{}{"path": storage.ResetDataDirPath()}
}

func (a *App) GetResultOutputDir() string { return storage.GetResultOutputDir() }

func (a *App) SetResultOutputDir(path string) map[string]interface{} {
	p, err := storage.SetResultOutputDir(path)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"path": p}
}

func (a *App) ResetResultOutputDir() map[string]interface{} {
	return map[string]interface{}{"path": storage.ResetResultOutputDir()}
}

func (a *App) GetProxy() string { return storage.GetProxy() }

func (a *App) SetProxy(proxyStr string) map[string]interface{} {
	p, err := storage.SetProxy(proxyStr)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	// 网关出口代理跟随全局设置（kiro.dev 按出口 IP 限流）
	if a.proxyServer != nil {
		a.proxyServer.Proxy = p
	}
	return map[string]interface{}{"proxy": p}
}

// ResetProxy — 添加返回值以兼容 Wails 绑定
func (a *App) ResetProxy() map[string]interface{} {
	storage.ResetProxy()
	return map[string]interface{}{"ok": true}
}

// DetectProxy — 接受 proxyStr 参数，兼容 Wails 绑定签名
func (a *App) DetectProxy(proxyStr string) map[string]interface{} {
	info := proxy.Detect(proxyStr)
	b, _ := json.Marshal(info)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

// ===== Outlook =====

func (a *App) GetOutlookAccounts() []map[string]interface{} { return storage.GetAccountsCached() }
func (a *App) AddOutlookAccounts(data string) map[string]interface{} {
	return email.AddOutlookAccounts(data)
}
func (a *App) DeleteOutlookAccount(addr string) map[string]interface{} {
	return email.DeleteOutlookAccount(addr)
}
func (a *App) ClearOutlookAccounts() map[string]interface{} { return email.ClearOutlookAccounts() }
func (a *App) ClearRegisteredOutlookAccounts() map[string]interface{} {
	return email.ClearRegisteredOutlookAccounts()
}
func (a *App) ImportOutlookFile(path string) map[string]interface{} {
	return email.ImportOutlookFile(path)
}

// ===== CloudMail =====

func (a *App) GetCloudMailConfigs() []map[string]interface{} {
	cfgs := email.GetCloudMailConfigs()
	res := make([]map[string]interface{}, len(cfgs))
	for i, c := range cfgs {
		b, _ := json.Marshal(c)
		json.Unmarshal(b, &res[i])
	}
	return res
}

func (a *App) SaveCloudMailConfigs(jsonStr string) map[string]interface{} {
	return email.SaveCloudMailConfigs(jsonStr)
}
func (a *App) TestCloudMailConnection(jsonStr string) map[string]interface{} {
	return email.TestCloudMailConnection(jsonStr)
}

// ===== Domain Pool =====

func (a *App) ListDomainPool() []map[string]interface{} {
	entries := email.ListDomainPool()
	res := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		b, _ := json.Marshal(e)
		json.Unmarshal(b, &res[i])
	}
	return res
}

func (a *App) SetDomainEnabled(domain string, enabled bool) map[string]interface{} {
	email.SetDomainEnabled(domain, enabled)
	return map[string]interface{}{"success": true}
}

func (a *App) UnbanDomain(domain string) map[string]interface{} {
	email.UnbanDomain(domain)
	return map[string]interface{}{"success": true}
}

// ===== Proxy Pool =====

func (a *App) ListProxyPool() []map[string]interface{} {
	entries := proxy.List()
	res := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		b, _ := json.Marshal(e)
		json.Unmarshal(b, &res[i])
	}
	return res
}

func (a *App) AddProxyEntry(name, url string, weight int) map[string]interface{} {
	e, err := proxy.Add(proxy.PoolEntry{Name: name, URL: url, Weight: weight, Enabled: true})
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	b, _ := json.Marshal(e)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

func (a *App) UpdateProxyEntry(id, name, url string, weight int, enabled bool) map[string]interface{} {
	e, err := proxy.Update(id, proxy.PoolEntry{Name: name, URL: url, Weight: weight, Enabled: enabled})
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	b, _ := json.Marshal(e)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

func (a *App) DeleteProxyEntry(id string) map[string]interface{} {
	if err := proxy.Delete(id); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"success": true}
}

func (a *App) TestProxyEntry(url string) map[string]interface{} {
	info := proxy.Detect(url)
	b, _ := json.Marshal(info)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

// ===== Subscription =====

func (a *App) LoadOutputAccounts() map[string]interface{} {
	accounts, err := data.LoadAccounts(storage.GetResultOutputDir())
	if err != nil {
		return map[string]interface{}{"accounts": []interface{}{}, "error": err.Error()}
	}
	return map[string]interface{}{"accounts": accounts, "outputDir": storage.GetResultOutputDir()}
}

func (a *App) GetSubscriptionPlans(addr string) ([]subscription.Plan, error) {
	account := a.findAccount(addr)
	if account == nil {
		return nil, nil
	}
	accessToken, err := subscription.RefreshAccessToken(*account)
	if err != nil {
		return nil, err
	}
	return subscription.ListPlans(*account, accessToken)
}

func (a *App) GetSubscriptionLink(addr, planType string) (string, error) {
	account := a.findAccount(addr)
	if account == nil {
		return "", nil
	}
	accessToken, err := subscription.RefreshAccessToken(*account)
	if err != nil {
		return "", err
	}
	return subscription.CreateSubscriptionLink(*account, accessToken, planType)
}

func (a *App) findAccount(addr string) *subscription.Account {
	accounts, _ := data.LoadAccounts(storage.GetResultOutputDir())
	for _, acc := range accounts {
		if acc["email"] == addr {
			b, _ := json.Marshal(acc)
			var a subscription.Account
			json.Unmarshal(b, &a)
			return &a
		}
	}
	return nil
}

// ===== License =====

// VerifyLicense — 接受 license 参数，兼容 Wails 绑定签名
func (a *App) VerifyLicense(license string) map[string]interface{} {
	return map[string]interface{}{"success": true}
}

func (a *App) CheckLicense() map[string]interface{}   { return map[string]interface{}{"success": true} }
func (a *App) GetLicenseInfo() map[string]interface{} { return map[string]interface{}{} }
func (a *App) LogoutLicense() map[string]interface{}  { return map[string]interface{}{"success": true} }

// ===== Reverse Proxy =====

// gatewayAccountsPath 网关账号文件（从号池同步导出，供 reverseproxy.AccountPool 加载）
func gatewayAccountsPath() string {
	return filepath.Join(storage.GetDataDir(), "gateway_accounts.json")
}

// syncGatewayAccounts 把号池（所有池、按邮箱去重）导出为网关账号文件，
// 让网关加载真实账号而不是旧的 resultOutputDir/accounts.json。
func (a *App) syncGatewayAccounts() error {
	mgr := pool.GetManager(storage.GetDataDir())
	seen := map[string]bool{}
	var list []reverseproxy.Account
	for _, p := range mgr.List() {
		for _, acc := range p.Accounts {
			if seen[acc.Email] {
				continue
			}
			seen[acc.Email] = true
			list = append(list, reverseproxy.Account{
				ClientID:        acc.ClientID,
				ClientSecret:    acc.ClientSecret,
				RefreshToken:    acc.RefreshToken,
				Region:          acc.Region,
				Email:           acc.Email,
				Subscription:    acc.Subscription,
				CreditLimit:     float64(acc.CreditLimit),
				CreditUsed:      float64(acc.CreditUsed),
				Provider:        acc.Provider,
				Time:            acc.Time,
				AvailableModels: acc.AvailableModels,
			})
		}
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return os.WriteFile(gatewayAccountsPath(), data, 0600)
}

// ensureProxyServer 懒初始化反向代理服务器
func (a *App) ensureProxyServer() {
	if a.proxyServer == nil {
		a.proxyServer = reverseproxy.NewProxyServer(
			reverseproxy.Config{
				Port:             20130,
				MaxAccountConc:   2,
				TotalConcurrency: 10,
				APIKey:           storage.GetGatewayAPIKey(),
				Proxy:            storage.GetProxy(),
			},
			gatewayAccountsPath(),
		)
	}
}

func (a *App) ProxyStart() map[string]interface{} {
	a.ensureProxyServer()
	// 出口代理可能在网关启动前刚被修改，启动时同步一次
	a.proxyServer.Proxy = storage.GetProxy()
	// 先把号池账号同步给网关（去重）
	if err := a.syncGatewayAccounts(); err != nil {
		return map[string]interface{}{"success": false, "error": "同步号池失败: " + err.Error()}
	}
	// 池已初始化则热加载新账号；未初始化由 Start 内部加载
	_ = a.proxyServer.ReloadAccounts()
	if a.proxyServer.IsRunning() {
		return map[string]interface{}{"success": true}
	}
	if err := a.proxyServer.Start(); err != nil {
		return map[string]interface{}{"success": false, "error": err.Error()}
	}
	return map[string]interface{}{"success": true}
}

func (a *App) ProxyStop() map[string]interface{} {
	a.ensureProxyServer()
	if err := a.proxyServer.Stop(); err != nil {
		return map[string]interface{}{"success": false, "error": err.Error()}
	}
	return map[string]interface{}{"success": true}
}

func (a *App) ProxyStatus() map[string]interface{} {
	a.ensureProxyServer()
	pool := a.proxyServer.AccountPool
	ac, tc := 0, 0
	if pool != nil {
		ac = pool.ActiveCount()
		tc = pool.Count()
	}
	stats := a.proxyServer.Stats
	return map[string]interface{}{
		"running":        a.proxyServer.IsRunning(),
		"port":           a.proxyServer.Port,
		"apiKey":         a.proxyServer.APIKey,
		"activeAccounts": ac,
		"totalAccounts":  tc,
		"stats": map[string]interface{}{
			"totalRequests":   stats.TotalRequests,
			"successRequests": stats.SuccessRequests,
			"failedRequests":  stats.FailedRequests,
			"totalTokensIn":   stats.TotalTokensIn,
			"totalTokensOut":  stats.TotalTokensOut,
			"avgLatencyMs":    stats.AvgLatencyMs,
			"todayRequests":   stats.TodayRequests,
		},
	}
}

func (a *App) ProxyConfig(cfg map[string]interface{}) map[string]interface{} {
	a.ensureProxyServer()
	if cfg == nil {
		return map[string]interface{}{
			"port":             a.proxyServer.Port,
			"maxAccountConc":   a.proxyServer.MaxAccountConc,
			"totalConcurrency": a.proxyServer.TotalConcurrency,
		}
	}
	restart := false
	if v, ok := cfg["port"]; ok {
		if p, ok := v.(float64); ok && p > 0 && int(p) != a.proxyServer.Port {
			a.proxyServer.Port = int(p)
			restart = true
		}
	}
	if v, ok := cfg["maxAccountConc"]; ok {
		if p, ok := v.(float64); ok && p > 0 {
			a.proxyServer.MaxAccountConc = int(p)
		}
	}
	if v, ok := cfg["totalConcurrency"]; ok {
		if p, ok := v.(float64); ok && p > 0 {
			a.proxyServer.TotalConcurrency = int(p)
		}
	}
	if v, ok := cfg["apiKey"]; ok {
		if s, ok := v.(string); ok {
			if err := storage.SetGatewayAPIKey(s); err == nil {
				a.proxyServer.APIKey = s
			}
		}
	}
	// 端口变化时热重启网关，让新端口立即生效（避免"改了不生效"）
	if restart && a.proxyServer.IsRunning() {
		if err := a.proxyServer.Stop(); err != nil {
			return map[string]interface{}{"success": false, "error": "重启网关失败: " + err.Error()}
		}
		if err := a.proxyServer.Start(); err != nil {
			return map[string]interface{}{"success": false, "error": "重启网关失败: " + err.Error()}
		}
	}
	return map[string]interface{}{"success": true}
}

func (a *App) ProxyAccounts() []map[string]interface{} {
	a.ensureProxyServer()
	pool := a.proxyServer.AccountPool
	if pool == nil {
		return []map[string]interface{}{}
	}
	accounts := pool.List()
	res := make([]map[string]interface{}, len(accounts))
	for i, acc := range accounts {
		b, _ := json.Marshal(acc)
		json.Unmarshal(b, &res[i])
	}
	return res
}

// errTextCode 失败状态码的中文简名（供测试结果展示）
func errTextCode(status int) string {
	switch status {
	case 429:
		return "限流"
	case 403:
		return "拒绝"
	case 401:
		return "未授权"
	}
	return "错误"
}

// TestModelConnection 真实发送一次上游请求测试模型连通性（9router 烧杯同款）。
// 与真实对话请求同罚：429 会冷却账号。全程走新版链路（三分支刷新 → 1:1 翻译 → 发送）。
func (a *App) TestModelConnection(model string) map[string]interface{} {
	start := time.Now()
	a.ensureProxyServer()
	p := a.proxyServer

	if p.AccountPool == nil || p.AccountPool.Count() == 0 {
		return map[string]interface{}{"ok": false, "error": "无可用账号，请先添加号池"}
	}
	model = reverseproxy.SanitizeModel(model)
	acc := p.AccountPool.Acquire(model)
	if acc == nil {
		return map[string]interface{}{"ok": false, "error": "所有账号忙或冷却中"}
	}
	defer p.AccountPool.Release(acc)

	// 出口代理：账号自带优先，否则全局设置（kiro.dev 按出口 IP 限流）
	egress := acc.Proxy
	if egress == "" {
		egress = storage.GetProxy()
	}

	// 1. 令牌确保：缺失或临近过期（5min 缓冲）→ 三分支刷新（并发去重）
	cred := reverseproxy.KiroCredentialFromAccount(acc)
	if err := reverseproxy.KiroEnsureAccessToken(cred, egress); err != nil {
		p.AccountPool.Suspend(acc)
		return map[string]interface{}{"ok": false, "error": "token 刷新失败: " + err.Error(), "account": acc.Email}
	}
	acc.SetAccessToken(cred.AccessToken)
	if cred.ExpiresAtMs > 0 {
		acc.SetTokenExpiresAt(cred.ExpiresAtMs * 1e6)
	}

	// 2. 翻译（"hi" 最小连通性测试；maxTokens 16 按 9router ping 语义传，
	// 翻译器按遗留 Bug① 硬编码 32000，与真实请求行为一致）
	req := &reverseproxy.ChatRequest{
		Model:     model,
		MaxTokens: 16,
		Messages:  []reverseproxy.ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	tr, err := reverseproxy.KiroTranslateChatRequest(req, model, cred, nil)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": "翻译失败: " + err.Error(), "account": acc.Email}
	}

	// 3. 发送（原地重试 + 主机轮换）
	resp, err := reverseproxy.KiroSendChat(tr, cred, egress)
	if err != nil {
		status := 0
		if strings.Contains(err.Error(), "429") {
			status = 429
		}
		// 与真实请求同罚：ERROR_RULES 分类上模型锁（429 指数退避，其他瞬态 30s）
		reverseproxy.KiroApplyGatewayError(acc, model, status, err.Error(), time.Now())
		reverseproxy.KiroProviderFailure()
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("上游错误(%s)：%v，账号已冷却", errTextCode(status), err), "account": acc.Email}
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		b := string(body)
		if len(b) > 200 {
			b = b[:200]
		}
		errText := fmt.Sprintf("upstream HTTP %d: %s", resp.StatusCode, b)
		reverseproxy.KiroApplyGatewayError(acc, model, resp.StatusCode, errText, time.Now())
		reverseproxy.KiroProviderFailure()
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("上游 HTTP %d，账号已冷却", resp.StatusCode), "account": acc.Email}
	}

	reverseproxy.KiroProviderSuccess()
	reverseproxy.KiroApplyGatewaySuccess(acc, model)

	// 读内容：有非空 content 才算通（对齐 9router：HTTP ok + choices 非空）
	chunks := make(chan reverseproxy.SSEEvent, 64)
	go reverseproxy.ParseEventStream(resp.Body, chunks, model)
	content := ""
	for ch := range chunks {
		if len(ch.Choices) > 0 {
			content += ch.Choices[0].Delta.Content
		}
	}
	if strings.TrimSpace(content) == "" {
		return map[string]interface{}{"ok": false, "error": "上游 200 但返回空内容", "account": acc.Email}
	}
	return map[string]interface{}{
		"ok":        true,
		"latencyMs": time.Since(start).Milliseconds(),
		"account":   acc.Email,
		"preview":   content,
	}
}

// ===== Account Pool =====

func (a *App) ListPools() []map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())
	pools := mgr.List()
	res := make([]map[string]interface{}, len(pools))
	for i, p := range pools {
		b, _ := json.Marshal(p)
		json.Unmarshal(b, &res[i])
	}
	return res
}

func (a *App) CreatePool(name, strategy string, accountsJSON string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())

	// 解析 JSON 为 Account 数组
	var accounts []*pool.Account
	if err := json.Unmarshal([]byte(accountsJSON), &accounts); err != nil {
		return map[string]interface{}{"error": "Invalid accounts JSON: " + err.Error()}
	}

	p, err := mgr.Create(name, pool.Strategy(strategy), accounts)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	b, _ := json.Marshal(p)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

func (a *App) UpdatePool(id, name, strategy string, accountsJSON string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())

	// 解析 JSON 为 Account 数组
	var accounts []*pool.Account
	if err := json.Unmarshal([]byte(accountsJSON), &accounts); err != nil {
		return map[string]interface{}{"error": "Invalid accounts JSON: " + err.Error()}
	}

	p, err := mgr.Update(id, name, pool.Strategy(strategy), accounts)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	b, _ := json.Marshal(p)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

func (a *App) DeletePool(id string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())
	if err := mgr.Delete(id); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"success": true}
}

func (a *App) GetPool(id string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())
	p, err := mgr.Get(id)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	b, _ := json.Marshal(p)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

// ImportPoolAccounts 从 JSON 文件导入账号到号池
func (a *App) ImportPoolAccounts(filePath string) map[string]interface{} {
	accounts, err := pool.ImportAccountsFromFile(filePath)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	b, _ := json.Marshal(accounts)
	var result []map[string]interface{}
	json.Unmarshal(b, &result)

	return map[string]interface{}{
		"accounts": result,
		"count":    len(accounts),
	}
}

// ImportToDefaultPool 从 JSON 文件导入账号到默认号池（无需弹窗确认）
func (a *App) ImportToDefaultPool(filePath string) map[string]interface{} {
	accounts, err := pool.ImportAccountsFromFile(filePath)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if len(accounts) == 0 {
		return map[string]interface{}{"error": "未找到有效账号"}
	}

	mgr := pool.GetManager(storage.GetDataDir())
	pools := mgr.List()

	// 查找或创建默认号池
	var defaultPool *pool.Pool
	for _, p := range pools {
		if p.Name == "默认号池" {
			defaultPool = p
			break
		}
	}

	if defaultPool == nil {
		// 创建默认号池
		p, err := mgr.Create("默认号池", pool.StrategySequential, accounts)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		defaultPool = p
	} else {
		// 追加账号到已有号池（去重）
		added := 0
		for _, acc := range accounts {
			if err := mgr.AddAccountToPool(defaultPool.ID, acc); err == nil {
				added++
			}
		}
		if added == 0 {
			return map[string]interface{}{"error": "所有账号已存在或验证失败"}
		}
		// 重新加载获取更新后的池
		for _, p := range mgr.List() {
			if p.ID == defaultPool.ID {
				defaultPool = p
				break
			}
		}
	}

	b, _ := json.Marshal(defaultPool)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

// AddAccountToPool 添加单个账号到号池（注册后调用）
func (a *App) AddAccountToPool(poolID string, accountJSON string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())

	var account pool.Account
	if err := json.Unmarshal([]byte(accountJSON), &account); err != nil {
		return map[string]interface{}{"error": "Invalid account JSON: " + err.Error()}
	}

	if err := mgr.AddAccountToPool(poolID, &account); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	return map[string]interface{}{"success": true}
}

// RefreshAccountInfo 刷新单个账号的健康状态、额度和模型信息
func (a *App) RefreshAccountInfo(poolID string, email string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())

	a.healthMu.Lock()
	defer a.healthMu.Unlock()

	p, err := mgr.Get(poolID)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	// 查找账号
	var targetAccount *pool.Account
	for _, acc := range p.Accounts {
		if acc.Email == email {
			targetAccount = acc
			break
		}
	}

	if targetAccount == nil {
		return map[string]interface{}{"error": "Account not found"}
	}

	// 刷新账号信息
	client := pool.NewKiroClient()
	if err := client.UpdateAccountInfo(targetAccount); err != nil {
		return map[string]interface{}{
			"error":  err.Error(),
			"status": targetAccount.HealthStatus,
		}
	}

	// 保存更新后的号池
	_, err = mgr.Update(p.ID, p.Name, p.Strategy, p.Accounts)
	if err != nil {
		return map[string]interface{}{"error": "Failed to save: " + err.Error()}
	}

	// 返回更新后的账号信息
	b, _ := json.Marshal(targetAccount)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

// 号池自动健康刷新参数
const (
	healthRefreshTick = 60 * time.Second // 轮询间隔
	healthRefreshTTL  = 5 * time.Minute  // 健康检测有效时长（超时才算需刷新）
	healthRefreshMax  = 3                // 每 tick 最多刷新账号数（防上游风暴）
)

// autoHealthRefreshLoop 后台增量刷新循环：每 60s 挑需要刷新的账号，最多刷 3 个
func (a *App) autoHealthRefreshLoop() {
	ticker := time.NewTicker(healthRefreshTick)
	defer ticker.Stop()
	for {
		select {
		case <-a.healthStop:
			return
		case <-ticker.C:
			a.refreshIncrementalAll()
		}
	}
}

// refreshIncrementalAll 遍历所有号池做增量刷新（与手动刷新互斥）
func (a *App) refreshIncrementalAll() {
	mgr := pool.GetManager(storage.GetDataDir())
	now := time.Now()
	client := pool.NewKiroClient()

	a.healthMu.Lock()
	defer a.healthMu.Unlock()

	refreshed := 0
	for _, p := range mgr.List() {
		changed := false
		for _, acc := range p.Accounts {
			if refreshed >= healthRefreshMax {
				break
			}
			if !pool.NeedsRefresh(acc, now, healthRefreshTTL) {
				continue
			}
			client.UpdateAccountInfo(acc)
			refreshed++
			changed = true
		}
		if changed {
			mgr.Update(p.ID, p.Name, p.Strategy, p.Accounts)
		}
	}
}

// RefreshAccountsIncremental 增量刷新指定号池：只刷需要刷新的账号（自动循环同款判定）
func (a *App) RefreshAccountsIncremental(poolID string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())

	p, err := mgr.Get(poolID)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	client := pool.NewKiroClient()
	now := time.Now()

	a.healthMu.Lock()
	defer a.healthMu.Unlock()

	refreshed, failed, skipped := 0, 0, 0
	for _, acc := range p.Accounts {
		if !pool.NeedsRefresh(acc, now, healthRefreshTTL) {
			skipped++
			continue
		}
		if err := client.UpdateAccountInfo(acc); err != nil {
			failed++
		} else {
			refreshed++
		}
	}

	// 保存更新后的号池
	_, err = mgr.Update(p.ID, p.Name, p.Strategy, p.Accounts)
	if err != nil {
		return map[string]interface{}{"error": "Failed to save: " + err.Error()}
	}

	return map[string]interface{}{
		"refreshed": refreshed,
		"failed":    failed,
		"skipped":   skipped,
	}
}

// RefreshAllAccountsInfo 批量刷新号池中所有账号的信息
func (a *App) RefreshAllAccountsInfo(poolID string) map[string]interface{} {
	mgr := pool.GetManager(storage.GetDataDir())

	p, err := mgr.Get(poolID)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	client := pool.NewKiroClient()

	a.healthMu.Lock()
	defer a.healthMu.Unlock()

	successCount := 0
	failedCount := 0

	for _, acc := range p.Accounts {
		if err := client.UpdateAccountInfo(acc); err != nil {
			failedCount++
		} else {
			successCount++
		}
	}

	// 保存更新后的号池
	_, err = mgr.Update(p.ID, p.Name, p.Strategy, p.Accounts)
	if err != nil {
		return map[string]interface{}{"error": "Failed to save: " + err.Error()}
	}

	// 重新获取更新后的池
	p, _ = mgr.Get(poolID)
	b, _ := json.Marshal(p)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	m["refreshed"] = successCount
	m["failed"] = failedCount

	return m
}
