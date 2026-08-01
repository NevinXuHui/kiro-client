package core

// manual.go — 手动注册模式：设备授权 + 可见浏览器。
//
// 自动注册被 TES 行为指纹风控拦截（send-otp BLOCKED / 域名拉黑）时，
// 自动注册永远无法通过——真人操作才能过。本文件实现手动闭环：
//
//  1. 程序自动（tls-client，不受 TES 影响）：
//     Step1 OIDC 注册 + Step2 设备授权 → 拿 deviceCode + userCode
//  2. 弹出可见浏览器 → 导航到设备授权页，用户在浏览器里手动完成
//     AWS Builder ID 注册（真实浏览器 + 真人操作，TES 放行）+ 授权
//  3. 程序轮询 /token（device_code grant）→ 用户授权后自动拿到 awsToken
//  4. 验活 + 返回结果（结构对齐 Run()，可直接走 SaveKiroSuccess/autoAddToPool）
//
// 结果字段与 Run() 保持一致：email/password/client_id/client_secret/
// aws_token/verify/status，供 coordinator 的落盘与入库链路复用。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	httputil "reg_go/internal/http"
	"reg_go/internal/waf"
)

// ManualRegister 手动注册：返回与 Run() 同构的结果 map。
// 调用方（app 层）负责在 goroutine 中执行并通过事件回传状态。
func (r *Registrar) ManualRegister() map[string]interface{} {
	prefix := "[Kiro][手动]"
	// app 层 goroutine 启动时 Ctx 可能为 nil，浏览器会话需要有效 parent context
	if r.Ctx == nil {
		r.Ctx = context.Background()
	}

	// === 1. OIDC 注册 + 设备授权（tls-client，不受 TES 影响）===
	if err := r.Step1OIDC(); err != nil {
		return r.manualFail(prefix, "OIDC", err)
	}
	if err := r.Step2Device(); err != nil {
		return r.manualFail(prefix, "Device", err)
	}
	log.Printf("%s 设备授权码: %s", prefix, r.UserCode)

	// === 2. 弹出可见浏览器，导航到设备授权页 ===
	authURL := fmt.Sprintf("%s/start/#/device?user_code=%s", r.Cfg.ViewBase, r.UserCode)
	log.Printf("%s 打开浏览器: %s", prefix, authURL)
	log.Printf("%s 请在浏览器中完成 AWS Builder ID 注册并授权设备", prefix)
	log.Printf("%s 提示：注册邮箱可用任意邮箱（真实邮箱/临时邮箱均可，真人操作不受域名拉黑影响）", prefix)

	session, err := waf.NewBrowserSession(r.Ctx, r.Cfg.Proxy, authURL, r.Identity.UA, true)
	if err != nil {
		return map[string]interface{}{
			"status": "failed", "error": fmt.Sprintf("浏览器启动失败: %v", err), "email": r.Email,
		}
	}
	defer session.Close()

	// 等待用户操作：轮询 /token（device_code grant），最长 10 分钟
	awsToken, err := r.pollDeviceToken(prefix, 10*time.Minute)
	if err != nil {
		return map[string]interface{}{
			"status": "failed", "error": err.Error(), "email": r.Email,
		}
	}
	log.Printf("%s 授权成功，已获取令牌", prefix)

	// === 3. 验活 ===
	verify := r.VerifyAlive(awsToken)
	if suspended, _ := verify["suspended"].(bool); suspended {
		log.Printf("%s 账号已被封禁", prefix)
		return map[string]interface{}{
			"status": "failed", "error": "suspended", "email": r.Email,
			"passwordSet": true,
		}
	}
	alive, _ := verify["alive"].(bool)
	if alive {
		log.Printf("%s 注册成功", prefix)
	} else {
		log.Printf("%s 注册完成", prefix)
	}

	// email 兜底链：验活结果 → accessToken JWT。
	// 手动注册用户自填邮箱，程序侧 r.Email 为空；验活降级路径（Q 端点不可达）
	// 也不返回 email，此时从 accessToken JWT 提取（email/preferred_username/sub）。
	emailAddr, _ := verify["email"].(string)
	if emailAddr == "" {
		if at, ok := awsToken["accessToken"].(string); ok {
			emailAddr = extractEmailFromJWT(at)
		}
	}

	return map[string]interface{}{
		"email":         emailAddr,
		"password":      r.Cfg.Password,
		"status":        "success",
		"passwordSet":   true,
		"client_id":     r.ClientID,
		"client_secret": r.ClientSecret,
		"device_code":   r.DeviceCode,
		"aws_token":     awsToken,
		"verify":        verify,
	}
}

// extractEmailFromJWT 从 access token JWT 提取邮箱（email || preferred_username || sub）。
// 与 reverseproxy.ExtractKiroEmailFromJWT 同构，core 不依赖网关包。
func extractEmailFromJWT(token string) string {
	if token == "" {
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	pad := parts[1]
	if m := len(pad) % 4; m != 0 {
		pad += strings.Repeat("=", 4-m)
	}
	pad = strings.ReplaceAll(pad, "-", "+")
	pad = strings.ReplaceAll(pad, "_", "/")
	decoded, err := base64.StdEncoding.DecodeString(pad)
	if err != nil {
		return ""
	}
	var claims map[string]interface{}
	if json.Unmarshal(decoded, &claims) != nil {
		return ""
	}
	for _, k := range []string{"email", "preferred_username", "sub"} {
		if v, ok := claims[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// pollDeviceToken 轮询设备授权 token 端点（device_code grant）。
// 用户在浏览器完成授权后，AWS 侧绑定 deviceCode，轮询即返回 token。
func (r *Registrar) pollDeviceToken(prefix string, timeout time.Duration) (map[string]interface{}, error) {
	deadline := time.Now().Add(timeout)
	tick := 0
	for time.Now().Before(deadline) {
		if r.ctxCancelled() {
			return nil, fmt.Errorf("任务已取消")
		}
		body, status, _, err := r.DoPostRaw(r.Cfg.OIDCBase+"/token", map[string]interface{}{
			"clientId": r.ClientID, "clientSecret": r.ClientSecret,
			"deviceCode": r.DeviceCode,
			"grantType":  "urn:ietf:params:oauth:grant-type:device_code",
		}, map[string]string{"Content-Type": "application/json"})
		if err != nil {
			// 设备授权等待期网络瞬断（EOF/重置）不终止轮询——用户在浏览器操作中，
			// 下次轮询继续。仅在 context 取消时退出（上面已检查）。
			log.Printf("%s 轮询瞬断，继续等待: %v", prefix, err)
			time.Sleep(2 * time.Second)
			continue
		}
		if status == 200 {
			var result map[string]interface{}
			json.Unmarshal(body, &result)
			return result, nil
		}
		tick++
		if tick%10 == 0 {
			elapsed := int(deadline.Sub(time.Now()).Seconds())
			log.Printf("%s 等待授权中... (剩余 %ds)", prefix, elapsed)
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("授权超时（%v 未完成），请在浏览器中完成注册并授权", timeout)
}

// manualFail 手动注册失败结果（与 Run() 失败路径同构）。
func (r *Registrar) manualFail(prefix, step string, err error) map[string]interface{} {
	friendly := r.formatError(step, err)
	log.Printf("%s %s", prefix, friendly)
	return map[string]interface{}{
		"status": "failed", "error": friendly, "email": "",
	}
}

// 保持 httputil 引用（UbidGen 等后续扩展可能用到）。
var _ = httputil.UbidGen
