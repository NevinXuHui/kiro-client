package waf

// browser.go — 浏览器可执行文件定位与 chromedp allocator 构建（含 stealth）。

import (
	"context"
	"os"
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
// 顺序：环境变量 KIRO_BROWSER_PATH → Chrome（Program Files / 用户目录）→ Edge。
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
// Windows 优先 Edge（实测系统有 Edge 无 Chrome）；同时给出 Chrome 备选。
func candidatePaths() []string {
	if runtime.GOOS != "windows" {
		// 非 Windows：PATH 里的标准名称，以及 Linux/macOS 常见路径。
		return []string{
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
			"microsoft-edge",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	}
	prog := os.Getenv("ProgramFiles")
	prog86 := os.Getenv("ProgramFiles(x86)")
	local := os.Getenv("LOCALAPPDATA")
	var paths []string
	// Edge（当前系统确认存在）。
	if prog86 != "" {
		paths = append(paths, filepath.Join(prog86, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	if prog != "" {
		paths = append(paths, filepath.Join(prog, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	// Chrome。
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

// isExecutable 判断路径存在且（对绝对路径而言）是一个可执行文件。
// 对 PATH 中的裸名称（无路径分隔符）交给 exec.LookPath 在启动时处理，这里只验绝对路径。
func isExecutable(p string) bool {
	if p == "" {
		return false
	}
	// 裸名称（不含分隔符且非 Windows 绝对路径）→ 认为可用，由 chromedp 启动时解析。
	if !filepath.IsAbs(p) && filepath.Base(p) == p {
		return true
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
