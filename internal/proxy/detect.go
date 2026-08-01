package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	httputil "reg_go/internal/http"
	"reg_go/internal/storage"
)

// Info 代理检测结果
type Info struct {
	OK      bool   `json:"ok"`
	Scheme  string `json:"scheme"`
	IP      string `json:"ip"`
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	ISP     string `json:"isp"`
	Error   string `json:"error,omitempty"`
}

// Detect 通过给定代理访问 google 官网验证连通性（http/https/socks5 均可），
// 成功后 best-effort 查询出口 IP 归属信息。
func Detect(proxyURL string) Info {
	proxyURL = strings.TrimSpace(proxyURL)
	// 归一化简写（host:port 等），与保存时的处理一致，避免 tls-client 解析失败
	proxyURL = storage.NormalizeProxyAddress(proxyURL)
	if proxyURL == "" {
		return Info{Error: "代理为空"}
	}

	scheme := "http"
	if i := strings.Index(proxyURL, "://"); i > 0 {
		scheme = strings.ToLower(proxyURL[:i])
	}

	client := httputil.NewTLSClient(proxyURL, true)

	// 1) 连通性：google 官网 generate_204（官方轻量端点，成功即 204）
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result := make(chan Info, 1)
	go func() {
		req, _ := fhttp.NewRequest("GET", "https://www.google.com/generate_204", nil)
		req.Header.Set("User-Agent", "kiro-client/proxy-check")
		resp, err := client.Do(req)
		if err != nil {
			result <- Info{Scheme: scheme, Error: simplifyProxyErr(err.Error())}
			return
		}
		resp.Body.Close()
		if resp.StatusCode != 204 {
			result <- Info{Scheme: scheme, Error: fmt.Sprintf("google 返回 HTTP %d", resp.StatusCode)}
			return
		}
		result <- Info{OK: true, Scheme: scheme}
	}()

	var info Info
	select {
	case info = <-result:
	case <-ctx.Done():
		return Info{Scheme: scheme, Error: "检测超时"}
	}
	if !info.OK {
		return info
	}

	// 2) 出口 IP 归属（best-effort，失败不影响连通结果）
	geo, err := probeGeo(client)
	if err == nil {
		info.IP = geo.IP
		info.Country = geo.Country
		info.Region = geo.Region
		info.City = geo.City
		info.ISP = geo.ISP
	}
	return info
}

func probeGeo(client tls_client.HttpClient) (Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan Info, 1)
	go func() {
		req, _ := fhttp.NewRequest("GET", "http://ip-api.com/json/?lang=zh-CN&fields=status,message,country,regionName,city,isp,query", nil)
		req.Header.Set("User-Agent", "kiro-client/proxy-check")
		resp, err := client.Do(req)
		if err != nil {
			done <- Info{}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			done <- Info{}
			return
		}
		var data struct {
			Status, Message, Country, RegionName, City, ISP, Query string
		}
		if err := json.Unmarshal(body, &data); err != nil || data.Status != "success" {
			done <- Info{}
			return
		}
		done <- Info{IP: data.Query, Country: data.Country, Region: data.RegionName, City: data.City, ISP: data.ISP}
	}()
	select {
	case info := <-done:
		return info, nil
	case <-ctx.Done():
		return Info{}, ctx.Err()
	}
}

func simplifyProxyErr(s string) string {
	switch {
	case strings.Contains(s, "connection refused"):
		return "连接被拒绝"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline"):
		return "连接超时"
	case strings.Contains(s, "no such host"):
		return "域名解析失败"
	case strings.Contains(s, "socks"):
		return "SOCKS 协商失败"
	case strings.Contains(s, "proxy"):
		return "代理握手失败"
	}
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}
