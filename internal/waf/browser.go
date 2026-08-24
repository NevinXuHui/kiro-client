package waf

// browser.go — 浏览器可执行文件定位与 chromedp allocator 构建（含 stealth）。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/chromedp/chromedp"
)

// cacheFound 浏览器定位的结果，避免每次收割都重新探测文件系统。
var (
	cacheFound string
	cacheOnce  sync.Once
)

// findBrowser 按优先级定位系统上可用的 Chrome / Edge 可执行文件。
// 顺序：KIRO_BROWSER_PATH → 当前 OS 常见安装路径 → PATH 中的裸命令名。
// 找不到返回空串，调用方据此回退或报错。
func findBrowser() string {
	cacheOnce.Do(func() {
		cacheFound = locateBrowserOnce()
	})
	return cacheFound
}

// locateBrowserOnce 执行实际探测，仅会运行一次（由 findBrowser 经 sync.Once 保护）。
func locateBrowserOnce() string {
	// 1. 环境变量显式指定。
	if p := os.Getenv("KIRO_BROWSER_PATH"); p != "" {
		if isExecutable(p) {
			return p
		}
	}
	// 2. 候选路径列表（Windows 为 Chrome 与 Edge 的常见安装位置）。
	for _, c := range candidatePaths() {
		if isExecutable(c) {
			return c
		}
	}
	return ""
}

// candidatePaths 返回当前系统下 Chrome / Edge 的常见安装路径。
func candidatePaths() []string {
	switch runtime.GOOS {
	case "windows":
		return windowsCandidatePaths()
	case "darwin":
		return darwinCandidatePaths()
	default:
		return unixCandidatePaths()
	}
}

func windowsCandidatePaths() []string {
	prog := os.Getenv("ProgramFiles")
	prog86 := os.Getenv("ProgramFiles(x86)")
	local := os.Getenv("LOCALAPPDATA")
	var paths []string
	if prog86 != "" {
		paths = append(paths, filepath.Join(prog86, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	if prog != "" {
		paths = append(paths, filepath.Join(prog, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	if prog != "" {
		paths = append(paths, filepath.Join(prog, "Google", "Chrome", "Application", "chrome.exe"))
	}
	if prog86 != "" {
		paths = append(paths, filepath.Join(prog86, "Google", "Chrome", "Application", "chrome.exe"))
	}
	if local != "" {
		paths = append(paths, filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"))
	}
	return paths
}

func darwinCandidatePaths() []string {
	// 先查 .app 包内可执行文件。macOS 上 google-chrome 通常不在 PATH。
	paths := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
			filepath.Join(home, "Applications", "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge"),
			filepath.Join(home, "Applications", "Chromium.app", "Contents", "MacOS", "Chromium"),
		)
	}
	return append(paths, "google-chrome", "google-chrome-stable", "chromium", "microsoft-edge")
}

func unixCandidatePaths() []string {
	return []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"microsoft-edge",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/microsoft-edge",
		"/snap/bin/chromium",
	}
}

// isExecutable 判断路径存在且是一个可执行文件。
// 裸命令名必须能在 PATH 中解析，否则 Linux 名（google-chrome）在 macOS
// 上会被误判为可用，chromedp 启动时才报 executable file not found。
func isExecutable(p string) bool {
	if p == "" {
		return false
	}
	if !filepath.IsAbs(p) && filepath.Base(p) == p {
		_, err := exec.LookPath(p)
		return err == nil
	}
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// newAllocator 构建一个带 stealth 参数的 chromedp ExecAllocator。
// proxy 为归一化后的代理 URL；无鉴权代理直接透传，有鉴权代理透传 host:port，
// 真正的账密由调用方通过 CDP Fetch.authRequired 事件注入（见 proxyauth.go）。
//
// visible=true 时以非 headless 模式启动（手动注册模式：用户可见、可交互）。
// 返回的 context 已挂载 allocator，调用方需用它派生 NewContext 再操作浏览器。
func newAllocator(parent context.Context, proxy string, visible bool) (context.Context, context.CancelFunc, error) {
	exe := findBrowser()
	if exe == "" {
		return nil, nil, errNoBrowser
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(exe),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		// —— stealth：去掉自动化指纹 ——
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
		// —— 稳定性 ——
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		// 临时用户数据目录隔离，避免污染用户真实浏览器配置。
		chromedp.UserDataDir(""),
	}
	if !visible {
		opts = append(opts, chromedp.Headless)
	}

	// 代理透传。注意：Chrome 的 --proxy-server 会忽略嵌入的账密，
	// 因此这里只传 scheme://host:port，账密交给 CDP Fetch 域处理。
	if ps, ok := proxyForFlag(proxy); ok {
		opts = append(opts, chromedp.ProxyServer(ps))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(parent, opts...)
	return allocCtx, cancel, nil
}
