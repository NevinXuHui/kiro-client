package waf

// proxyauth.go — 代理 URL 解析与 CDP Fetch 域的代理鉴权拦截。

import (
	"context"
	"errors"
	"net/url"
	"sync"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/chromedp"
)

// proxyInfo 是从归一化代理 URL 中解析出的结构。
// hostport 用于 --proxy-server；user/pass 非空表示需要 CDP 注入鉴权。
type proxyInfo struct {
	hostport string // 例如 "127.0.0.1:7897" 或 "host:1080"
	user     string
	pass     string
	hasAuth  bool
}

// parseProxy 解析归一化代理 URL（scheme://[user:pass@]host:port）。
// 空串返回零值（无代理）。解析失败返回错误。
func parseProxy(raw string) (proxyInfo, error) {
	if raw == "" {
		return proxyInfo{}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return proxyInfo{}, err
	}
	pi := proxyInfo{hostport: u.Host}
	if u.User != nil {
		pi.user = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			pi.pass = pw
		}
		pi.hasAuth = pi.user != "" || pi.pass != ""
	}
	return pi, nil
}

// proxyForFlag 返回传给 chromedp.ProxyServer / --proxy-server 的字符串（无账密）。
// 第二个返回值表示是否启用代理。
func proxyForFlag(raw string) (string, bool) {
	pi, err := parseProxy(raw)
	if err != nil || pi.hostport == "" {
		return "", false
	}
	// 保留 scheme（socks5/http），但去掉 userinfo。
	scheme := "http"
	if u, e := url.Parse(raw); e == nil && u.Scheme != "" {
		scheme = u.Scheme
	}
	return scheme + "://" + pi.hostport, true
}

// setupProxyAuth 在目标 context 上安装 CDP Fetch 域监听，
// 拦截代理 401/407 鉴权挑战并注入账密。仅当代理需要鉴权时才启用。
//
// 返回一个“已启用”的标记动作（必须在导航前 chromedp.Run），以及一个卸载函数。
// 设计要点：chromedp 通过 ListenTarget 订阅事件；fetch.Enable 打开拦截，
// fetch.ContinueWithAuth 应答鉴权。未被暂停的普通请求需要 ContinueRequest 放行。
func setupProxyAuth(ctx context.Context, pi proxyInfo) (chromedp.Action, func()) {
	if !pi.hasAuth {
		// 无鉴权：不需要任何动作，直接返回。
		return nil, func() {}
	}

	var mu sync.Mutex
	handled := map[fetch.RequestID]bool{} // 已应答的 requestId，避免重复处理

	// 启用 Fetch 域，要求把鉴权挑战事件交给本端处理。
	enable := fetch.Enable().WithHandleAuthRequests(true)

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *fetch.EventAuthRequired:
			// 仅应答 Proxy 来源的鉴权挑战（HTTP/代理 407）。
			// Server 来源（网站 401）不自动应答，避免向 AWS 提交错误凭据。
			if e.AuthChallenge == nil {
				go chromedp.Run(ctx, fetch.ContinueWithAuth(e.RequestID, &fetch.AuthChallengeResponse{
					Response: fetch.AuthChallengeResponseResponseCancelAuth,
				}))
				return
			}
			mu.Lock()
			if handled[e.RequestID] {
				mu.Unlock()
				return
			}
			handled[e.RequestID] = true
			mu.Unlock()

			resp := &fetch.AuthChallengeResponse{
				Response: fetch.AuthChallengeResponseResponseProvideCredentials,
				Username: pi.user,
				Password: pi.pass,
			}
			go chromedp.Run(ctx, fetch.ContinueWithAuth(e.RequestID, resp))

		case *fetch.EventRequestPaused:
			// 普通请求被暂停（因为 Fetch 域拦截范围较广）→ 直接放行。
			mu.Lock()
			seen := handled[e.RequestID]
			mu.Unlock()
			if !seen {
				go chromedp.Run(ctx, fetch.ContinueRequest(e.RequestID))
			}
		}
	})

	return enable, func() {
		// 导航完成后关闭 Fetch 域，恢复正常网络栈行为。
		go chromedp.Run(ctx, fetch.Disable())
	}
}

// errNoBrowser 在找不到浏览器可执行文件时返回。
var errNoBrowser = errors.New("未找到 Chrome/Edge 可执行文件；请设置 KIRO_BROWSER_PATH 环境变量或安装 Edge/Chrome")
