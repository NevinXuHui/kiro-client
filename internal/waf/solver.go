package waf

// solver.go — WAF Challenge 绕过：在真实浏览器内执行 Step6 POST。
//
// 根因：AWS WAF 基于 TLS 指纹（JA3/JA4）拦截 bogdanfinn/tls-client，
// 而 Edge/Chrome 的 TLS 指纹合法，直接通过。
// 因此不收 token，而是直接在浏览器内执行完整的 Step6 请求并返回响应。

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// BrowserSession 是一个可复用的浏览器会话，支持在同一 session 内执行多个 POST。
type BrowserSession struct {
	taskCtx     context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	seq         int64
	curHandle   string    // 页面自身 XHR 请求中最新使用的 workflowStateHandle
	ua          string    // UA 覆盖值：与 tls-client 一致，保证跨客户端会话上下文对齐
	closeOnce   sync.Once // 确保 cancel 只执行一次，避免 chromedp 内部 channel 重复关闭
	taskCancel  context.CancelFunc
	ctxCancel   context.CancelFunc
	allocCancel context.CancelFunc
}

// xhrHandleHook 注入页面，拦截页面自身 XHR 响应并记录最新 workflowStateHandle
// 与全部 api/execute 响应体。页面 init 的 api/execute 调用会轮换 handle，
// 直接读 URL 里的旧 handle 提交会被服务端拒为 CONNECTION_ISSUE。
// 响应里的 handle 才是 app 下一步会使用的值。
const xhrHandleHook = `(function(){
	if (window.__wafH !== undefined) return;
	window.__wafH = '';
	window.__wafBodies = [];
	var origSend = XMLHttpRequest.prototype.send;
	XMLHttpRequest.prototype.send = function(body) {
		this.addEventListener('load', function() {
			try {
				var d = JSON.parse(this.responseText);
				if (d && typeof d.workflowStateHandle === 'string' && d.workflowStateHandle) {
					window.__wafH = d.workflowStateHandle;
				}
				var u = this.responseURL || '';
				if (u.indexOf('api/execute') >= 0) {
					window.__wafBodies.push(this.responseText);
					if (window.__wafBodies.length > 30) window.__wafBodies.shift();
				}
			} catch(e) {}
		});
		return origSend.apply(this, arguments);
	};
})();`

// NewBrowserSession 启动浏览器并导航到登录页建立 session cookie。
// ua 用于覆盖浏览器 UA，与 tls-client 侧请求保持一致（服务端按 UA 绑定工作流上下文）。
// visible=true 时非 headless（手动注册模式：用户可见可操作），
// 会话超时放宽到 15 分钟（用户手动注册+授权需 2-5 分钟，120s 会自动关窗）。
func NewBrowserSession(parent context.Context, proxy, loginURL, ua string, visible bool) (*BrowserSession, error) {
	timeout := 120 * time.Second
	if visible {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)

	allocCtx, allocCancel, err := newAllocator(ctx, proxy, visible)
	if err != nil {
		cancel()
		return nil, err
	}

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)

	// 创建 session 对象，封装清理函数
	s := &BrowserSession{
		taskCtx:     taskCtx,
		taskCancel:  taskCancel,
		ctxCancel:   cancel,
		allocCancel: allocCancel,
		ua:          ua,
	}

	// 代理鉴权。
	pi, _ := parseProxy(proxy)
	if pi.hasAuth {
		enableAuth, teardown := setupProxyAuth(taskCtx, pi)
		defer teardown()
		if err := chromedp.Run(taskCtx); err != nil {
			s.Close()
			return nil, fmt.Errorf("浏览器启动失败: %w", err)
		}
		if enableAuth != nil {
			if err := chromedp.Run(taskCtx, enableAuth); err != nil {
				s.Close()
				return nil, fmt.Errorf("代理鉴权拦截安装失败: %w", err)
			}
		}
	}

	installNetworkLogging(taskCtx, s)

	// 导航到登录页建立 session cookie。
	// 先注入 XHR 钩子（在新文档脚本执行前生效），再导航。
	log.Printf("[WAF] 浏览器导航: %s", loginURL)
	err = chromedp.Run(taskCtx,
		chromedp.ActionFunc(func(c context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(xhrHandleHook).Do(c)
			return err
		}),
		chromedp.ActionFunc(func(c context.Context) error {
			_ = chromedp.Evaluate(`Object.defineProperty(navigator,'webdriver',{get:()=>undefined})`, nil).Do(c)
			return nil
		}),
		chromedp.ActionFunc(func(c context.Context) error {
			return s.applyUA(c)
		}),
		chromedp.Navigate(loginURL),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
	)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("登录页导航失败: %w", err)
	}

	return s, nil
}

// applyUA 将当前 frame 的 UA 覆盖为会话 UA（与 tls-client 对齐）。
// 每次导航后新 frame 需重新应用，因此所有导航/请求动作前都调用。
func (s *BrowserSession) applyUA(c context.Context) error {
	if s.ua == "" {
		return nil
	}
	return emulation.SetUserAgentOverride(s.ua).Do(c)
}

// captureHandle 记录页面自身 XHR 最新使用的 workflowStateHandle。
func (s *BrowserSession) captureHandle(handle string) {
	if handle == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.curHandle = handle
}

// CurrentHandle 返回页面自身 XHR 最新使用的 workflowStateHandle。
func (s *BrowserSession) CurrentHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.curHandle
}

// WaitForHandle 轮询等待页面自身 XHR 请求中的 workflowStateHandle 出现（postData 捕获，回退用）。
func (s *BrowserSession) WaitForHandle(ctx context.Context, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h := s.CurrentHandle(); h != "" {
			return h
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(300 * time.Millisecond):
		}
	}
	return ""
}

// WaitForPageHandle 轮询等待页面自身 XHR 响应中的最新 workflowStateHandle（钩子捕获）。
// 响应里的 handle 才是 app 下一步将使用的值，优先于请求侧捕获。
func (s *BrowserSession) WaitForPageHandle(ctx context.Context, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var h string
		if err := chromedp.Run(s.taskCtx, chromedp.Evaluate(`window.__wafH || ""`, &h)); err == nil && h != "" {
			return h
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(300 * time.Millisecond):
		}
	}
	return ""
}

// NavigateTo 导航到指定 URL。注入的 XHR 钩子对新文档依然生效，收割数据随之重置。
func (s *BrowserSession) NavigateTo(targetURL string) error {
	log.Printf("[WAF] 浏览器导航: %s", targetURL)
	return chromedp.Run(s.taskCtx,
		chromedp.ActionFunc(func(c context.Context) error {
			return s.applyUA(c)
		}),
		chromedp.Navigate(targetURL),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
	)
}

// WaitForPageQuiet 等待页面自身 api/execute XHR 数量稳定（初始化完成）。
func (s *BrowserSession) WaitForPageQuiet(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := -1
	for time.Now().Before(deadline) {
		var n float64
		if err := chromedp.Run(s.taskCtx, chromedp.Evaluate(`(window.__wafBodies||[]).length`, &n)); err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		if int(n) == last && int(n) > 0 {
			return nil
		}
		last = int(n)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1200 * time.Millisecond):
		}
	}
	return fmt.Errorf("等待页面初始化超时 (已收到 %d 个响应)", last)
}

// ReadPageHarvest 读取页面收割结果：最新 workflowStateHandle 与全部 api/execute 响应体。
func (s *BrowserSession) ReadPageHarvest(ctx context.Context) (string, []string, error) {
	var handle string
	var bodies []string
	err := chromedp.Run(s.taskCtx,
		chromedp.Evaluate(`window.__wafH || ""`, &handle),
		chromedp.Evaluate(`window.__wafBodies || []`, &bodies),
	)
	if err != nil {
		return "", nil, err
	}
	return handle, bodies, nil
}

// FetchPost 在浏览器内用 fetch 执行一次 POST，返回 status + body。
// rid 会作为 x-amzn-requestid 请求头（与页面 app.js 行为一致）。
func (s *BrowserSession) FetchPost(apiURL, postBodyJSON, rid string) (int, string, error) {
	if err := chromedp.Run(s.taskCtx, chromedp.ActionFunc(func(c context.Context) error {
		return s.applyUA(c)
	})); err != nil {
		return -1, "", err
	}
	return browserFetch(s.taskCtx, apiURL, postBodyJSON, rid)
}

// ReadCookies 读取浏览器 cookie。
func (s *BrowserSession) ReadCookies() (map[string]string, error) {
	return readAllCookies(s.taskCtx)
}

// SetCookies 将 key=value cookie 以 forURL 为作用域写入浏览器 jar。
// 用于把 tls-client 侧积累的跨域 cookie（如 profile 域 ubid/d2c-token）同步给浏览器。
func (s *BrowserSession) SetCookies(pairs map[string]string, forURL string) error {
	for k, v := range pairs {
		k, v := k, v
		if err := chromedp.Run(s.taskCtx, chromedp.ActionFunc(func(c context.Context) error {
			return network.SetCookie(k, v).WithURL(forURL).Do(c)
		})); err != nil {
			return fmt.Errorf("注入 cookie %s 失败: %w", k, err)
		}
	}
	return nil
}

// Close 关闭浏览器。
func (s *BrowserSession) Close() {
	s.closeOnce.Do(func() {
		if s.taskCancel != nil {
			s.taskCancel()
		}
		if s.ctxCancel != nil {
			s.ctxCancel()
		}
		if s.allocCancel != nil {
			s.allocCancel()
		}
	})
}

// browserFetch 在浏览器内用 fetch 执行一次 POST，返回 status + body。
// 请求头完全复刻页面 app.js 的请求层（Content-Type / x-amzn-requestid / X-Amz-Date / Accept），
// 缺头或 body 非 JSON 字符串会导致服务端返回 400 CONNECTION_ISSUE。
func browserFetch(ctx context.Context, apiURL, postBodyJSON, rid string) (int, string, error) {
	jsFetch := fmt.Sprintf(`(async () => {
		try {
			const resp = await fetch(%q, {
				method: 'POST',
				credentials: 'include',
				headers: {
					'Content-Type': 'application/json; charset=UTF-8',
					'x-amzn-requestid': %q,
					'X-Amz-Date': new Date().toUTCString(),
					'Accept': 'application/json, text/plain, */*'
				},
				body: JSON.stringify(%s)
			});
			const text = await resp.text();
			window.__wafR = {status: resp.status, body: text};
		} catch(e) {
			window.__wafR = {status: -1, body: e.message};
		}
	})()`, apiURL, rid, postBodyJSON)

	err := chromedp.Run(ctx,
		chromedp.Evaluate(jsFetch, nil),
		chromedp.Sleep(8*time.Second),
	)
	if err != nil {
		return -1, "", fmt.Errorf("fetch 执行失败: %w", err)
	}

	var status float64
	err = chromedp.Run(ctx, chromedp.Evaluate(`window.__wafR ? window.__wafR.status : -2`, &status))
	if err != nil {
		return -1, "", fmt.Errorf("读取 status 失败: %w", err)
	}
	var bodyStr string
	err = chromedp.Run(ctx, chromedp.Evaluate(`window.__wafR ? window.__wafR.body : ""`, &bodyStr))
	if err != nil {
		return -1, "", fmt.Errorf("读取 body 失败: %w", err)
	}
	if int(status) == -1 {
		return -1, "", fmt.Errorf("fetch 报错: %s", bodyStr)
	}
	if int(status) == -2 {
		return -1, "", fmt.Errorf("fetch 结果未就绪")
	}
	return int(status), bodyStr, nil
}

// readAllCookies 读取浏览器的所有 cookie 并返回 map。
func readAllCookies(ctx context.Context) (map[string]string, error) {
	var cookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		var err error
		cookies, err = network.GetCookies().Do(c)
		return err
	}))
	if err != nil {
		return nil, fmt.Errorf("读取 cookie 失败: %w", err)
	}

	log.Printf("[WAF] 浏览器 cookie 列表（共 %d 个）:", len(cookies))
	out := make(map[string]string)
	for _, ck := range cookies {
		if len(ck.Value) > 40 {
			log.Printf("[WAF]   %s=%s... (domain=%s)", ck.Name, ck.Value[:40], ck.Domain)
		} else {
			log.Printf("[WAF]   %s=%s (domain=%s)", ck.Name, ck.Value, ck.Domain)
		}
		// 保留关键 session cookie。
		name := strings.ToLower(ck.Name)
		if name == "aws-usi-authn" || name == "awsccc" ||
			strings.Contains(name, "csrf") || strings.Contains(name, "workflow") ||
			strings.Contains(name, "login-interview") || strings.Contains(name, "platform-ubid") {
			out[ck.Name] = ck.Value
		}
	}
	return out, nil
}

// loginPageURLWithHandle 把 API URL 转成带 workflowStateHandle 的登录页 URL。
func loginPageURLWithHandle(apiURL, handle string) string {
	u, err := parseURL(apiURL)
	if err != nil {
		return apiURL
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "platform" {
		base := fmt.Sprintf("%s://%s/platform/%s/login", u.Scheme, u.Host, parts[1])
		if handle != "" {
			base += "?workflowStateHandle=" + handle
		}
		return base
	}
	return fmt.Sprintf("%s://%s/", u.Scheme, u.Host)
}

// installNetworkLogging 启用 CDP Network domain 并安装响应监听。
func installNetworkLogging(ctx context.Context, s *BrowserSession) {
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			if e.Request == nil || !strings.Contains(e.Request.URL, "api/execute") {
				return
			}
			hdrs := make([]string, 0, len(e.Request.Headers))
			for k, v := range e.Request.Headers {
				hdrs = append(hdrs, k+": "+fmt.Sprint(v))
			}
			log.Printf("[WAF-Net] → REQ %s %s hdrs=[%s]", e.Request.Method, e.Request.URL, strings.Join(hdrs, " | "))
			// postData 需单独取回；直接经 executor 发 CDP 命令，避免在监听回调内嵌套 chromedp.Run。
			go func(rid network.RequestID) {
				pd, err := network.GetRequestPostData(rid).Do(cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Target))
				if err != nil {
					return
				}
				if len(pd) > 300 {
					pd = pd[:300]
				}
				log.Printf("[WAF-Net] → REQ postData: %s", pd)
				// 页面自身 XHR 会轮换 workflowStateHandle；捕获其最新值供后续 fetch 使用。
				if i := strings.Index(string(pd), `"workflowStateHandle":"`); i >= 0 {
					v := pd[i+len(`"workflowStateHandle":"`):]
					if j := bytes.IndexByte(v, '"'); j > 0 {
						s.captureHandle(string(v[:j]))
					}
				}
			}(e.RequestID)
		case *network.EventResponseReceived:
			if e.Response == nil || e.Response.URL == "" {
				return
			}
			u := e.Response.URL
			if !strings.Contains(u, "signin.aws") && !strings.Contains(u, "amazonaws.com") {
				return
			}
			log.Printf("[WAF-Net] ← %d %s (type=%s)", e.Response.Status, u, e.Type)
			for k, v := range e.Response.Headers {
				lk := strings.ToLower(k)
				if lk == "x-amzn-waf-action" || lk == "set-cookie" ||
					lk == "content-type" || lk == "server" {
					log.Printf("[WAF-Net]   %s: %v", k, v)
				}
			}
		case *network.EventResponseReceivedExtraInfo:
			if e.Headers != nil {
				for k, vals := range e.Headers {
					lk := strings.ToLower(k)
					if lk == "x-amzn-waf-action" || strings.Contains(lk, "waf") {
						log.Printf("[WAF-Net]   header %s: %v", k, vals)
					}
				}
			}
		case *network.EventLoadingFailed:
			log.Printf("[WAF-Net] ✗ FAILED %s (error=%v)", e.Type, e.ErrorText)
		}
	})
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		return network.Enable().Do(c)
	}))
}

// parseURL 是 url.Parse 的薄包装，方便测试和简化导入。
func parseURL(rawURL string) (*url.URL, error) {
	return url.Parse(rawURL)
}
