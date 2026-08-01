package core

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	httputil "reg_go/internal/http"
)

// Step11CreateIdentity 创建身份
// 注：浏览器会话无法停留 profile 域（无 profile 授权会话，会被踢回 signin login），
// 故 create-identity 仍走 tls-client；与续页的上下文对齐交由浏览器会话 UA 覆盖
// （wafBrowser 以 r.Identity.UA 启动）实现——state 签发与消费使用同一 UA。
func (r *Registrar) Step11CreateIdentity(otp string) error {
	log.Println("[11] 创建身份")
	ref := fmt.Sprintf("%s/?workflowID=%s", r.Cfg.ProfileBase, r.WorkflowID)
	fp := r.GenFP("profile", "EmailVerification", 0, "")

	body, _, _, err := r.DoPostRaw(r.Cfg.ProfileBase+"/api/create-identity", map[string]interface{}{
		"workflowState": r.WorkflowState,
		"userData":      map[string]string{"email": r.Email, "fullName": r.Cfg.FullName},
		"otpCode":       otp,
		"browserData": map[string]interface{}{
			"attributes": map[string]interface{}{
				"fingerprint":     fp,
				"eventTimestamp":  time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
				"timeSpentOnPage": "45000",
				"pageName":        "EMAIL_VERIFICATION",
				"eventType":       "EmailVerification",
				"ubid":            r.Ubid,
				"visitorId":       r.VisitorID,
			},
			"cookies": map[string]interface{}{},
		},
	}, r.BuildProfileHeaders(ref))
	if err != nil {
		return err
	}

	var data map[string]interface{}
	json.Unmarshal(body, &data)
	r.RegCode, _ = data["registrationCode"].(string)
	r.SignState, _ = data["signInState"].(string)
	if r.RegCode == "" {
		return fmt.Errorf("create-identity 未返回 registrationCode: %s", string(body))
	}
	if len(r.RegCode) > 20 {
		log.Printf("regCode=%s...", r.RegCode[:20])
	}
	return nil
}

// Step12SetPassword 设置密码
func (r *Registrar) Step12SetPassword() error {
	log.Println("[12] 设置密码")
	api := fmt.Sprintf("%s/platform/%s/signup/api/execute", r.Cfg.SigninBase, r.Cfg.DirectoryID)
	ref := fmt.Sprintf("%s/platform/%s/signup?registrationCode=%s&state=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, r.RegCode, r.SignState)
	fp := r.GenFP("signup", "PageSubmit", 0, "")

	// 12a: 获取加密公钥（tls-client 被 WAF 拦截或响应不含公钥时，
	// 导航到 signup 续页，由页面自身初始化 XHR 完成并收割公钥 + 最新 handle）
	encCtx, err := r.fetchEncryptionContext(api, ref, fp)
	if err != nil {
		return err
	}
	pubKeyMap := httputil.GetNestedStringMap(encCtx, "publicKey")
	if pubKeyMap == nil || pubKeyMap["n"] == "" {
		return fmt.Errorf("未获取到加密公钥")
	}

	issuer, _ := encCtx["issuer"].(string)
	if issuer == "" {
		issuer = "signin"
	}
	audience, _ := encCtx["audience"].(string)
	if audience == "" {
		audience = "AWSPasswordService"
	}
	region, _ := encCtx["region"].(string)
	if region == "" {
		region = "us-east-1"
	}

	encrypted, err := r.JWE.Encrypt(r.Cfg.Password, pubKeyMap, issuer, audience, region)
	if err != nil {
		return fmt.Errorf("JWE 加密失败: %w", err)
	}

	// 12b: 提交密码
	fp = r.GenFP("signup", "PageSubmit", 0, "")
	rid := NewUUID()
	h := r.BuildHeaders(ref, r.Cfg.SigninBase)
	h["x-amzn-requestid"] = rid
	h["x-amz-date"] = GmtDate()
	h["priority"] = "u=1, i"

	_, body, respH, err := r.apiExecute(api, map[string]interface{}{
		"stepId":              "get-new-password-for-password-creation",
		"workflowStateHandle": r.WorkflowHandle,
		"actionId":            "SUBMIT",
		"inputs": []interface{}{
			map[string]interface{}{
				"input_type":            "PasswordRequestInput",
				"password":              encrypted,
				"successfullyEncrypted": "SUCCESSFUL",
			},
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp},
		},
		"visitorId": r.VisitorID, "requestId": rid,
	}, h)
	if err != nil {
		return err
	}
	if respH != nil {
		httputil.SaveCookies(r.Cookies, respH)
	}

	var data map[string]interface{}
	json.Unmarshal(body, &data)
	redir, _ := data["redirect"].(map[string]interface{})
	rurl, _ := redir["url"].(string)
	if rurl == "" {
		return fmt.Errorf("密码设置未返回 redirect: %s", string(body))
	}

	wh := httputil.ExtractParam(rurl, "workflowStateHandle")
	st := httputil.ExtractParam(rurl, "state")
	rh := httputil.ExtractParam(rurl, "workflowResultHandle")
	return r.completeSignup(wh, st, rh)
}

// fetchEncryptionContext 获取密码设置的加密公钥与最新 workflowStateHandle。
// 注意：signInState 是一次性的——绝不能先在浏览器里用 fetch 重发本请求，
// 否则会消费掉 state，导致页面自身初始化报 SIGNIN_BAD_REQUEST_ERROR/BLOCKED。
// 因此这里只走 tls-client；被 WAF 拦时 state 未被消费，直接交给页面初始化收割。
func (r *Registrar) fetchEncryptionContext(api, ref, fp string) (map[string]interface{}, error) {
	rid := NewUUID()
	h := r.BuildHeaders(ref, r.Cfg.SigninBase)
	h["x-amzn-requestid"] = rid
	h["x-amz-date"] = GmtDate()
	h["priority"] = "u=1, i"

	body, _, respH, err := r.DoPostRaw(api, map[string]interface{}{
		"stepId": "", "state": r.SignState,
		"inputs": []interface{}{
			map[string]string{
				"input_type":       "UserRegistrationRequestInput",
				"registrationCode": r.RegCode, "state": r.SignState,
			},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp},
		},
		"requestId": rid,
	}, h)
	if err != nil {
		return nil, err
	}
	if respH != nil {
		httputil.SaveCookies(r.Cookies, respH)
	}

	var data map[string]interface{}
	json.Unmarshal(body, &data)
	if wh, ok := data["workflowStateHandle"].(string); ok {
		r.WorkflowHandle = wh
	}
	encCtx := httputil.GetNestedMap(data, "workflowResponseData", "encryptionContextResponse")
	if encCtx != nil && len(encCtx) > 0 {
		return encCtx, nil
	}

	if !isWAFChallenge(respH) {
		log.Printf("[12a] 直接请求未取到公钥, body: %.400s", string(body))
	}
	return r.harvestEncryptionContext()
}

// harvestEncryptionContext 导航到 signup 续页，等待页面自身初始化 XHR 完成，
// 从收割的响应体中提取 workflowStateHandle 与 encryptionContextResponse（含公钥）。
func (r *Registrar) harvestEncryptionContext() (map[string]interface{}, error) {
	session, err := r.wafBrowser()
	if err != nil {
		return nil, fmt.Errorf("浏览器启动失败: %w", err)
	}
	signupURL := fmt.Sprintf("%s/platform/%s/signup?registrationCode=%s&state=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, r.RegCode, r.SignState)
	if err := session.NavigateTo(signupURL); err != nil {
		return nil, fmt.Errorf("signup 页导航失败: %w", err)
	}
	// 等待页面初始化 XHR 全部完成（到达密码表单）。
	if err := session.WaitForPageQuiet(r.Ctx, 15*time.Second); err != nil {
		log.Printf("[WAF] 页面初始化等待: %v", err)
	}
	handle, bodies, err := session.ReadPageHarvest(r.Ctx)
	if err != nil {
		return nil, fmt.Errorf("读取页面收割数据失败: %w", err)
	}
	if handle != "" {
		r.WorkflowHandle = handle
	}
	log.Printf("[WAF] 页面收割: %d 个 api/execute 响应, handle=%s", len(bodies), handle)
	// 从后往前找最后一个含 encryptionContext 的响应（密码表单初始化）。
	for i := len(bodies) - 1; i >= 0; i-- {
		var d map[string]interface{}
		if json.Unmarshal([]byte(bodies[i]), &d) != nil {
			continue
		}
		dumpStepBody("harvest", 0, d, bodies[i])
		encCtx := httputil.GetNestedMap(d, "workflowResponseData", "encryptionContextResponse")
		if encCtx != nil && len(encCtx) > 0 {
			return encCtx, nil
		}
	}
	return nil, fmt.Errorf("页面初始化未提供加密公钥（收到 %d 个响应）", len(bodies))
}

// completeSignup 完成注册工作流
func (r *Registrar) completeSignup(wh, state, rh string) error {
	log.Println("[12.5] 完成注册工作流")
	api := fmt.Sprintf("%s/platform/%s/api/execute", r.Cfg.SigninBase, r.Cfg.DirectoryID)
	ref := fmt.Sprintf("%s/platform/%s/login?workflowStateHandle=%s&state=%s&workflowResultHandle=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, wh, state, rh)
	fp := r.GenFP("signin", "PageLoad", 0, "")

	rid := NewUUID()
	h := r.BuildHeaders(ref, r.Cfg.SigninBase)
	h["x-amzn-requestid"] = rid
	h["x-amz-date"] = GmtDate()
	h["priority"] = "u=1, i"

	_, body, respH, err := r.apiExecute(api, map[string]interface{}{
		"stepId": "", "workflowStateHandle": wh,
		"workflowResultHandle": rh, "state": state,
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp},
		},
		"visitorId": r.VisitorID, "requestId": rid,
	}, h)
	if err != nil {
		return err
	}
	if respH != nil {
		httputil.SaveCookies(r.Cookies, respH)
	}

	var data map[string]interface{}
	json.Unmarshal(body, &data)
	if data["stepId"] != "end-of-workflow-success" {
		return fmt.Errorf("完成工作流失败: %v", data["stepId"])
	}

	if redir, ok := data["redirect"].(map[string]interface{}); ok {
		if rurl, ok := redir["url"].(string); ok {
			r.AuthCode = httputil.ExtractParam(rurl, "workflowResultHandle")
			r.SSOState = httputil.ExtractParam(rurl, "state")
			r.WdcCSRFToken = httputil.ExtractParam(rurl, "wdc_csrf_token")
		}
	}
	return nil
}
