package waf

// pool.go — aws-waf-token 的内存缓存（按 proxy 出口 + 域名键控，TTL 过期重收割）。

import (
	"net/url"
	"sync"
	"time"
)

// tokenTTL 是单个 WAF token 的有效时长。
// AWS WAF token 默认 30 分钟有效，留 5 分钟安全边际。
const tokenTTL = 25 * time.Minute

// cachedEntry 缓存一条收割结果。
type cachedEntry struct {
	cookies map[string]string // 至少含 aws-waf-token；可包含其他 WAF 相关 cookie
	fetched time.Time
}

// TokenCache 是并发安全的 WAF token 缓存。
// 键 = proxyKey + "|" + domain，保证不同代理出口、不同域名的 token 不混用。
type TokenCache struct {
	mu      sync.RWMutex
	entries map[string]cachedEntry
}

var (
	globalCache    *TokenCache
	globalCacheOnce sync.Once
)

// Cache 返回进程级单例缓存。
func Cache() *TokenCache {
	globalCacheOnce.Do(func() {
		globalCache = &TokenCache{entries: make(map[string]cachedEntry)}
	})
	return globalCache
}

// Get 返回未过期的 token（若存在）。第二个返回值表示命中。
// 命中时返回的 map 是拷贝，调用方可安全写入 Registrar.Cookies。
func (c *TokenCache) Get(proxyKey, domain string) (map[string]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[cacheKey(proxyKey, domain)]
	if !ok {
		return nil, false
	}
	if time.Since(e.fetched) > tokenTTL {
		return nil, false
	}
	// 拷贝一份，避免外部修改污染缓存。
	out := make(map[string]string, len(e.cookies))
	for k, v := range e.cookies {
		out[k] = v
	}
	return out, true
}

// Put 写入收割结果。cookies 至少应包含 aws-waf-token。
func (c *TokenCache) Put(proxyKey, domain string, cookies map[string]string) {
	if len(cookies) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey(proxyKey, domain)] = cachedEntry{
		cookies: cookies,
		fetched: time.Now(),
	}
}

// Invalidate 删除指定键的缓存（例如检测到 token 已失效时）。
func (c *TokenCache) Invalidate(proxyKey, domain string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, cacheKey(proxyKey, domain))
}

// proxyCacheKey 把一个归一化代理 URL 归约成稳定的缓存键（去掉账密，保留 host:port+scheme）。
// 同一出口 IP 的不同写法（带不带账密）应映射到同一 key。
// ProxyCacheKey 把一个归一化代理 URL 归约成稳定的缓存键（去掉账密，保留 host:port+scheme）。
// 同一出口 IP 的不同写法（带不带账密）应映射到同一 key。
// 导出供 core 包在 token 失效时主动清除缓存。
func ProxyCacheKey(proxy string) string {
	pi, err := parseProxy(proxy)
	if err != nil || pi.hostport == "" {
		return "direct"
	}
	scheme := "http"
	if u, e := url.Parse(proxy); e == nil && u.Scheme != "" {
		scheme = u.Scheme
	}
	return scheme + "://" + pi.hostport
}

func cacheKey(proxyKey, domain string) string {
	return ProxyCacheKey(proxyKey) + "|" + domain
}
