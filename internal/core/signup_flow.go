package core

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"reg_go/internal/email"
	httputil "reg_go/internal/http"
	"reg_go/internal/waf"
)

// Step6SubmitEmail 提交邮箱
func (r *Registrar) Step6SubmitEmail() (string, error) {
	log.Printf("[6] 提交邮箱 %s", r.Email)
	api := fmt.Sprintf("%s/platform/%s/api/execute", r.Cfg.SigninBase, r.Cfg.DirectoryID)

	// 执行一次 Step6 请求。
	fp := r.GenFP("signin", "PageSubmit", len(r.Email), r.Email)
	body, status, respH, err := r.step6Post(api, fp)
	if err != nil {
		return "", err
	}
	httputil.SaveCookies(r.Cookies, respH)

	// 解析响应。
	var data map[string]interface{}
	json.Unmarshal(body, &data)
	if wh, ok := data["workflowStateHandle"].(string); ok {
		r.WorkflowHandle = wh
	}

	if status == 400 {
		return "signup", nil
	} else if status == 200 {
		return "login", nil
	}

	// 非预期状态码：打印诊断信息并返回错误
	logUnexpectedResponse("Step6SubmitEmail", status, respH, body)
	return "", fmt.Errorf("提交邮箱失败: %d - %s", status, string(body)[:min(200, len(body))])
}

// step6Post 执行一次 Step6 的 POST 请求并返回原始响应。提取为独立方法以便重放。
func (r *Registrar) step6Post(api, fp string) ([]byte, int, map[string][]string, error) {
	ref := fmt.Sprintf("%s/platform/%s/login?workflowStateHandle=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, r.WorkflowHandle)
	rid := NewUUID()
	h := r.BuildHeaders(ref, r.Cfg.SigninBase)
	h["x-amzn-requestid"] = rid
	h["x-amz-date"] = GmtDate()
	h["priority"] = "u=1, i"

	return r.DoPostRaw(api, map[string]interface{}{
		"stepId":              "get-identity-user",
		"workflowStateHandle": r.WorkflowHandle,
		"actionId":            "SUBMIT",
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "ApplicationTypeRequestInput", "applicationType": "SSO_INDIVIDUAL_ID"},
			map[string]interface{}{
				"input_type":  "UserEventRequestInput",
				"directoryId": r.Cfg.DirectoryID,
				"userName":    r.Email,
				"userEvents": []map[string]interface{}{{
					"input_type":      "UserEvent",
					"eventType":       "PAGE_SUBMIT",
					"pageName":        "IDENTIFICATION",
					"timeSpentOnPage": 5000,
				}},
			},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp},
		},
		"visitorId": r.VisitorID,
		"requestId": rid,
	}, h)
}

// wafBrowser 返回任务共享的浏览器会话，首次调用时启动并等待页面轮换 handle。
func (r *Registrar) wafBrowser() (*waf.BrowserSession, error) {
	r.wafMu.Lock()
	defer r.wafMu.Unlock()
	if r.wafSess != nil {
		return r.wafSess, nil
	}
	loginURL := fmt.Sprintf("%s/platform/%s/login?workflowStateHandle=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, r.WorkflowHandle)
	session, err := waf.NewBrowserSession(r.Ctx, r.Cfg.Proxy, loginURL, r.Identity.UA, false)
	if err != nil {
		return nil, err
	}
	// 页面加载时其自身 XHR 会轮换 workflowStateHandle（每次 api/execute 返回新 handle），
	// 继续用 URL 里的旧 handle 提交会被服务端拒为 CONNECTION_ISSUE。
	// 优先取页面 XHR 响应中 app 即将使用的 handle，取不到则回退请求侧捕获。
	h := session.WaitForPageHandle(r.Ctx, 8*time.Second)
	if h == "" {
		h = session.WaitForHandle(r.Ctx, 3*time.Second)
	}
	if h != "" && h != r.WorkflowHandle {
		log.Printf("[WAF] 页面已轮换 workflowStateHandle: %s -> %s", r.WorkflowHandle, h)
		r.WorkflowHandle = h
	}
	r.wafSess = session
	return session, nil
}

// closeWafBrowser 关闭任务共享的浏览器会话。
func (r *Registrar) closeWafBrowser() {
	r.wafMu.Lock()
	defer r.wafMu.Unlock()
	if r.wafSess != nil {
		r.wafSess.Close()
		r.wafSess = nil
	}
}

// apiExecute 优先用 tls-client 执行 POST；响应带 WAF challenge 头时改用共享浏览器会话执行。
func (r *Registrar) apiExecute(apiURL string, payload map[string]interface{}, h map[string]string) (int, []byte, map[string][]string, error) {
	body, status, respH, err := r.DoPostRaw(apiURL, payload, h)
	if err != nil || !isWAFChallenge(respH) {
		return status, body, respH, err
	}
	log.Printf("[WAF] %s 被 WAF 拦截（TLS 指纹），改用浏览器执行...", apiURL)
	session, serr := r.wafBrowser()
	if serr != nil {
		return status, body, respH, fmt.Errorf("浏览器启动失败: %w", serr)
	}
	bodyJSON, _ := json.Marshal(payload)
	rid := h["x-amzn-requestid"]
	if rid == "" {
		rid = NewUUID()
	}
	st, bodyStr, ferr := session.FetchPost(apiURL, string(bodyJSON), rid)
	if ferr != nil {
		return status, body, respH, ferr
	}
	return st, []byte(bodyStr), nil, nil
}

// executeStep6InBrowser 在一个浏览器会话内依次执行 Step6+Step7+Step7.5，
// 绕过 WAF TLS 指纹检测。成功后将浏览器 cookie 注入 r.Cookies，
// 并更新 r.WorkflowHandle / r.WorkflowID / r.BrowserStep6Status。
func (r *Registrar) executeStep6InBrowser(api string) error {
	signupAPI := fmt.Sprintf("%s/platform/%s/signup/api/execute", r.Cfg.SigninBase, r.Cfg.DirectoryID)

	// 启动（或复用）浏览器会话。
	session, err := r.wafBrowser()
	if err != nil {
		return fmt.Errorf("浏览器启动失败: %w", err)
	}

	// === Step6 ===
	fp6 := r.GenFP("signin", "PageSubmit", len(r.Email), r.Email)
	rid6 := NewUUID()
	step6Body, _ := json.Marshal(map[string]interface{}{
		"stepId": "get-identity-user", "workflowStateHandle": r.WorkflowHandle,
		"actionId": "SUBMIT",
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "ApplicationTypeRequestInput", "applicationType": "SSO_INDIVIDUAL_ID"},
			map[string]interface{}{
				"input_type": "UserEventRequestInput", "directoryId": r.Cfg.DirectoryID,
				"userName": r.Email,
				"userEvents": []map[string]interface{}{{
					"input_type": "UserEvent", "eventType": "PAGE_SUBMIT",
					"pageName": "IDENTIFICATION", "timeSpentOnPage": 5000,
				}},
			},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp6},
		},
		"visitorId": r.VisitorID, "requestId": rid6,
	})

	log.Printf("[WAF] 浏览器执行 Step6...")
	step6Status, step6BodyStr, err := session.FetchPost(api, string(step6Body), rid6)
	if err != nil {
		return fmt.Errorf("浏览器 Step6 失败: %w", err)
	}
	log.Printf("[WAF] Step6 结果: status=%d, body_len=%d", step6Status, len(step6BodyStr))

	r.BrowserStep6Status = step6Status
	var step6Data map[string]interface{}
	json.Unmarshal([]byte(step6BodyStr), &step6Data)
	dumpStepBody("Step6", step6Status, step6Data, step6BodyStr)
	if wh, ok := step6Data["workflowStateHandle"].(string); ok {
		r.WorkflowHandle = wh
	}

	// 注入浏览器 session cookie（Step6 后）。
	cookies, _ := session.ReadCookies()
	for k, v := range cookies {
		r.Cookies[k] = v
	}

	if step6Status != 200 && step6Status != 400 {
		logUnexpectedResponse("Step6-browser", step6Status, nil, []byte(step6BodyStr))
	}

	// === Step7 ===
	log.Printf("[WAF] 浏览器执行 Step7...")
	fp7 := r.GenFP("signup", "PageSubmit", 0, "")
	rid7 := NewUUID()
	step7Ref := fmt.Sprintf("%s/platform/%s/login?workflowStateHandle=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, r.WorkflowHandle)
	step7Body, _ := json.Marshal(map[string]interface{}{
		"stepId": "get-identity-user", "workflowStateHandle": r.WorkflowHandle,
		"actionId": "SIGNUP",
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp7},
		},
		"visitorId": r.VisitorID, "requestId": rid7,
	})
	_ = step7Ref // used only for reference header construction, not needed in browser

	step7Status, step7BodyStr, err := session.FetchPost(api, string(step7Body), rid7)
	if err != nil {
		log.Printf("[WAF] Step7 浏览器执行失败: %v", err)
	} else {
		log.Printf("[WAF] Step7 结果: status=%d, body_len=%d", step7Status, len(step7BodyStr))
		var step7Data map[string]interface{}
		json.Unmarshal([]byte(step7BodyStr), &step7Data)
		dumpStepBody("Step7", step7Status, step7Data, step7BodyStr)
		if redir, ok := step7Data["redirect"].(map[string]interface{}); ok {
			if rurl, ok := redir["url"].(string); ok && strings.Contains(rurl, "workflowStateHandle=") {
				r.WorkflowHandle = httputil.SplitAfter(rurl, "workflowStateHandle=")
			}
		}
	}

	// === Step7.5 ===
	log.Printf("[WAF] 浏览器执行 Step7.5...")
	fp75 := r.GenFP("signup", "first_load", 0, "")
	rid75 := NewUUID()
	step75Body, _ := json.Marshal(map[string]interface{}{
		"stepId": "", "workflowStateHandle": r.WorkflowHandle,
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp75},
		},
		"visitorId": r.VisitorID, "requestId": rid75,
	})

	step75Status, step75BodyStr, err := session.FetchPost(signupAPI, string(step75Body), rid75)
	if err != nil {
		log.Printf("[WAF] Step7.5 浏览器执行失败: %v", err)
		return nil // Step6 已成功，Step7.5 失败不阻塞
	}
	log.Printf("[WAF] Step7.5 结果: status=%d, body_len=%d", step75Status, len(step75BodyStr))
	var step75Data map[string]interface{}
	json.Unmarshal([]byte(step75BodyStr), &step75Data)
	dumpStepBody("Step7.5", step75Status, step75Data, step75BodyStr)
	if wh, ok := step75Data["workflowStateHandle"].(string); ok {
		r.WorkflowHandle = wh
	}
	if step75Data["stepId"] != "start" {
		log.Printf("[WAF] Step7.5 意外 stepId: %v", step75Data["stepId"])
	}
	if redir, ok := step75Data["redirect"].(map[string]interface{}); ok {
		if rurl, ok := redir["url"].(string); ok && strings.Contains(rurl, "workflowID=") {
			wid := httputil.SplitAfter(rurl, "workflowID=")
			if i := strings.IndexByte(wid, '#'); i >= 0 {
				wid = wid[:i]
			}
			r.WorkflowID = wid
		}
	}

	// === Step7.5 第二阶段: stepId="start"（返回 redirect 中的 workflowID）===
	fp752 := r.GenFP("signup", "PageLoad", 0, "")
	rid752 := NewUUID()
	step752Body, _ := json.Marshal(map[string]interface{}{
		"stepId": "start", "workflowStateHandle": r.WorkflowHandle,
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp752},
		},
		"visitorId": r.VisitorID, "requestId": rid752,
	})
	log.Printf("[WAF] 浏览器执行 Step7.5 第二阶段 (start)...")
	st752, body752, err := session.FetchPost(signupAPI, string(step752Body), rid752)
	if err != nil {
		log.Printf("[WAF] Step7.5 第二阶段失败: %v", err)
	} else {
		log.Printf("[WAF] Step7.5 第二阶段结果: status=%d, body_len=%d", st752, len(body752))
		var d752 map[string]interface{}
		json.Unmarshal([]byte(body752), &d752)
		dumpStepBody("Step7.5b", st752, d752, body752)
		if wh, ok := d752["workflowStateHandle"].(string); ok {
			r.WorkflowHandle = wh
		}
		if redir, ok := d752["redirect"].(map[string]interface{}); ok {
			if rurl, ok := redir["url"].(string); ok && strings.Contains(rurl, "workflowID=") {
				wid := httputil.SplitAfter(rurl, "workflowID=")
				if i := strings.IndexByte(wid, '#'); i >= 0 {
					wid = wid[:i]
				}
				r.WorkflowID = wid
			}
		}
	}

	// 更新 cookie（Step7/7.5 可能产生新 cookie）。
	cookies, _ = session.ReadCookies()
	for k, v := range cookies {
		r.Cookies[k] = v
	}

	// 浏览器内已走完 SIGNUP + signup init 且拿到 workflowID → 主流程跳过 tls-client 重跑。
	r.BrowserSignupDone = r.WorkflowID != ""

	return nil
}

// dumpStepBody 打印浏览器步骤响应体的解析结果，用于定位步骤推进失败。
func dumpStepBody(step string, status int, data map[string]interface{}, body string) {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	log.Printf("[WAF]   %s 解析: keys=%v stepId=%v redirect=%v", step, keys, data["stepId"], data["redirect"])
	if len(body) > 600 {
		body = body[:600]
	}
	log.Printf("[WAF]   %s body: %s", step, body)
}

// isWAFChallenge 判断响应头是否表示 AWS WAF Challenge 动作。
func isWAFChallenge(respH map[string][]string) bool {
	for _, v := range respH["X-Amzn-Waf-Action"] {
		if strings.EqualFold(v, "challenge") {
			return true
		}
	}
	return false
}

// logUnexpectedResponse 打印非预期响应的诊断信息。
// AWS WAF 的 challenge/captcha 动作会返回 202 并带 x-amzn-waf-action 头，
// 与"接口变更"和"代理异常"在状态码上无法区分，只能靠响应头定性。
func logUnexpectedResponse(step string, status int, respH map[string][]string, body []byte) {
	log.Printf("[诊断] %s 非预期状态码 %d, body 长度=%d", step, status, len(body))
	for k, v := range respH {
		log.Printf("[诊断]   %s: %s", k, strings.Join(v, ", "))
	}
	if len(body) == 0 {
		log.Printf("[诊断]   body 为空")
		return
	}
	preview := body
	if len(preview) > 500 {
		preview = preview[:500]
	}
	if isPrintable(preview) {
		log.Printf("[诊断]   body: %s", string(preview))
	} else {
		// 响应体不可打印，通常意味着是压缩数据而未解压
		log.Printf("[诊断]   body 不可打印(疑似压缩未解压), 前 32 字节: % x", preview[:min(32, len(preview))])
	}
}

// isPrintable 判断字节切片是否为可打印文本（允许常见空白字符）。
func isPrintable(b []byte) bool {
	for _, c := range b {
		if c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// Step7Signup 注册
func (r *Registrar) Step7Signup() error {
	log.Println("[7] 注册 (SIGNUP)")
	api := fmt.Sprintf("%s/platform/%s/api/execute", r.Cfg.SigninBase, r.Cfg.DirectoryID)
	ref := fmt.Sprintf("%s/platform/%s/login?workflowStateHandle=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, r.WorkflowHandle)
	fp := r.GenFP("signup", "PageSubmit", 0, "")

	rid := NewUUID()
	h := r.BuildHeaders(ref, r.Cfg.SigninBase)
	h["x-amzn-requestid"] = rid
	h["x-amz-date"] = GmtDate()
	h["priority"] = "u=1, i"

	body, _, respH, err := r.DoPostRaw(api, map[string]interface{}{
		"stepId":              "get-identity-user",
		"workflowStateHandle": r.WorkflowHandle,
		"actionId":            "SIGNUP",
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp},
		},
		"visitorId": r.VisitorID,
		"requestId": rid,
	}, h)
	if err != nil {
		return err
	}
	httputil.SaveCookies(r.Cookies, respH)

	var data map[string]interface{}
	json.Unmarshal(body, &data)
	if redir, ok := data["redirect"].(map[string]interface{}); ok {
		if rurl, ok := redir["url"].(string); ok && strings.Contains(rurl, "workflowStateHandle=") {
			r.WorkflowHandle = httputil.SplitAfter(rurl, "workflowStateHandle=")
		}
	}
	return nil
}

// Step7_5SignupInit Signup API 初始化
func (r *Registrar) Step7_5SignupInit() error {
	log.Println("[7.5] Signup API 初始化")
	api := fmt.Sprintf("%s/platform/%s/signup/api/execute", r.Cfg.SigninBase, r.Cfg.DirectoryID)
	ref := fmt.Sprintf("%s/platform/%s/signup?workflowStateHandle=%s",
		r.Cfg.SigninBase, r.Cfg.DirectoryID, r.WorkflowHandle)

	fp := r.GenFP("signup", "first_load", 0, "")
	rid := NewUUID()
	h := r.BuildHeaders(ref, r.Cfg.SigninBase)
	h["x-amzn-requestid"] = rid
	h["x-amz-date"] = GmtDate()
	h["priority"] = "u=1, i"

	body, _, respH, err := r.DoPostRaw(api, map[string]interface{}{
		"stepId": "", "workflowStateHandle": r.WorkflowHandle,
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp},
		},
		"visitorId": r.VisitorID, "requestId": rid,
	}, h)
	if err != nil {
		return err
	}
	httputil.SaveCookies(r.Cookies, respH)

	var data map[string]interface{}
	json.Unmarshal(body, &data)
	if wh, ok := data["workflowStateHandle"].(string); ok {
		r.WorkflowHandle = wh
	}
	if data["stepId"] != "start" {
		return fmt.Errorf("Signup init 返回意外 stepId: %v", data["stepId"])
	}

	// 第二次请求
	fp = r.GenFP("signup", "PageLoad", 0, "")
	rid = NewUUID()
	h = r.BuildHeaders(ref, r.Cfg.SigninBase)
	h["x-amzn-requestid"] = rid
	h["x-amz-date"] = GmtDate()
	h["priority"] = "u=1, i"

	body, _, respH, err = r.DoPostRaw(api, map[string]interface{}{
		"stepId": "start", "workflowStateHandle": r.WorkflowHandle,
		"inputs": []interface{}{
			map[string]string{"input_type": "UserRequestInput", "username": r.Email},
			map[string]string{"input_type": "FingerPrintRequestInput", "fingerPrint": fp},
		},
		"visitorId": r.VisitorID, "requestId": rid,
	}, h)
	if err != nil {
		return err
	}
	httputil.SaveCookies(r.Cookies, respH)

	json.Unmarshal(body, &data)
	if wh, ok := data["workflowStateHandle"].(string); ok {
		r.WorkflowHandle = wh
	}
	if redir, ok := data["redirect"].(map[string]interface{}); ok {
		if rurl, ok := redir["url"].(string); ok && strings.Contains(rurl, "workflowID=") {
			wid := httputil.SplitAfter(rurl, "workflowID=")
			if i := strings.IndexByte(wid, '#'); i >= 0 {
				wid = wid[:i]
			}
			r.WorkflowID = wid
		}
	}
	if r.WorkflowID == "" {
		return fmt.Errorf("Signup init 未返回 workflowID")
	}
	return nil
}

// Step7_8ProfileInit Profile 页面初始化
func (r *Registrar) Step7_8ProfileInit() error {
	log.Println("[7.8] Profile 页面初始化")
	r.Ubid = httputil.UbidGen()
	r.Cookies["aws-user-profile-ubid"] = r.Ubid
	r.Cookies["i18next"] = "zh-CN"
	if _, ok := r.Cookies["awsccc"]; !ok {
		r.Cookies["awsccc"] = httputil.Awsccc()
	}

	url := fmt.Sprintf("%s/?workflowID=%s", r.Cfg.ProfileBase, r.WorkflowID)
	_, _, respH, err := r.DoGet(url, map[string]string{
		"Accept":         "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"User-Agent":     r.Identity.UA,
		"sec-fetch-dest": "document",
		"sec-fetch-mode": "navigate",
	})
	if err != nil {
		return err
	}
	httputil.SaveCookies(r.Cookies, respH)
	r.FPCtx.ResetPerfTiming()
	return r.FetchD2CToken(r.Cfg.ProfileBase, url)
}

// Step8ProfileStart Profile 启动
func (r *Registrar) Step8ProfileStart() error {
	log.Println("[8] Profile 启动")
	ref := fmt.Sprintf("%s/?workflowID=%s", r.Cfg.ProfileBase, r.WorkflowID)
	fp := r.GenFP("profile", "PageLoad", 0, "")

	body, _, _, err := r.DoPostRaw(r.Cfg.ProfileBase+"/api/start", map[string]interface{}{
		"workflowID": r.WorkflowID,
		"browserData": map[string]interface{}{
			"attributes": map[string]interface{}{
				"fingerprint":     fp,
				"eventTimestamp":  time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
				"timeSpentOnPage": "38",
				"eventType":       "PageLoad",
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
	r.WorkflowState, _ = data["workflowState"].(string)
	if r.WorkflowState == "" {
		return fmt.Errorf("Profile start 未返回 workflowState: %s", string(body))
	}
	if len(r.WorkflowState) > 30 {
		log.Printf("workflowState=%s...", r.WorkflowState[:30])
	}
	return nil
}

// Step9SendOTP 发送验证码
func (r *Registrar) Step9SendOTP() error {
	log.Println("[9] 发送验证码")

	// Outlook 模式: 记录发送前的邮件数量
	if r.Cfg.UseOutlook && r.Cfg.OutlookAccount != nil {
		count, err := email.GetInboxCount(*r.Cfg.OutlookAccount)
		if err != nil {
			log.Printf("获取邮件数量失败: %v, 默认为0", err)
		} else {
			r.OutlookMailCount = count
			log.Printf("发送前邮件数: %d", count)
		}
	}
	if r.Cfg.UseHttpAPI && r.Cfg.HttpAPIAccount != nil {
		r.HttpAPISeenCodes = email.SnapshotHttpAPICodes(*r.Cfg.HttpAPIAccount)
		log.Printf("发送前已见验证码数: %d", len(r.HttpAPISeenCodes))
	}

	ref := fmt.Sprintf("%s/?workflowID=%s", r.Cfg.ProfileBase, r.WorkflowID)
	timeOnPage := 5000 + rand.Intn(3001)
	fp := r.GenFPWithTime("profile", "PageSubmit", timeOnPage, len(r.Email), r.Email)
	tsp := fmt.Sprintf("%d", timeOnPage)

	reqPayload := map[string]interface{}{
		"workflowState": r.WorkflowState,
		"email":         r.Email,
		"browserData": map[string]interface{}{
			"attributes": map[string]interface{}{
				"fingerprint":     fp,
				"eventTimestamp":  time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
				"timeSpentOnPage": tsp,
				"pageName":        "EMAIL_COLLECTION",
				"eventType":       "PageSubmit",
				"ubid":            r.Ubid,
				"visitorId":       r.VisitorID,
			},
			"cookies": map[string]interface{}{},
		},
	}

	respBody, status, respH, err := r.DoPostRaw(r.Cfg.ProfileBase+"/api/send-otp", reqPayload, r.BuildProfileHeaders(ref))
	if err != nil {
		return err
	}
	if status != 200 {
		logUnexpectedResponse("Step9SendOTP", status, respH, respBody)
		if r.Cfg.Debug {
			log.Printf("[DEBUG] send-otp 失败: status=%d, body=%s, fp_len=%d", status, string(respBody), len(fp))
		}

		// 把响应体里的风控标记（如 BLOCKED/TES）带进 error，供上游 formatError / isKillSwitchError 精准识别
		bodyStr := string(respBody)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200]
		}
		return fmt.Errorf("send-otp 失败 (%d): %s", status, bodyStr)
	}
	log.Println("验证码已发送")
	return nil
}

// sendOTPInBrowser 在浏览器内执行 profile 流程（/api/start + /api/send-otp），
// 绕过 TES 行为指纹风控。复用 Step6 启动的共享浏览器会话，真 Chrome 的
// TLS + 行为指纹与真人一致，TES 放行；tls-client 则被判定非真人浏览器拦截。
func (r *Registrar) sendOTPInBrowser(payload map[string]interface{}) (int, string, error) {
	session, err := r.wafBrowser()
	if err != nil {
		return -1, "", fmt.Errorf("浏览器启动失败: %w", err)
	}

	profileURL := fmt.Sprintf("%s/?workflowID=%s", r.Cfg.ProfileBase, r.WorkflowID)

	// 把 tls-client 侧积累的 profile 域 cookies（ubid/d2c-token 等）同步给浏览器，
	// 保证 workflowID 会话在浏览器侧延续。
	sync := map[string]string{}
	for _, k := range []string{"awsccc", "aws-user-profile-ubid", "i18next", "awsd2c-token", "awsd2c-token-c"} {
		if v, ok := r.Cookies[k]; ok {
			sync[k] = v
		}
	}
	if len(sync) > 0 {
		if err := session.SetCookies(sync, r.Cfg.ProfileBase); err != nil {
			log.Printf("[WAF] 注入 profile cookie 失败: %v", err)
		}
	}

	// 导航到 profile 页，建立同源上下文（Origin/Referer 正确）。
	if err := session.NavigateTo(profileURL); err != nil {
		return -1, "", fmt.Errorf("导航 profile 页失败: %w", err)
	}
	// 等 SPA 初始化（其自身 XHR 会调用 /api/start），避免后续请求竞争。
	time.Sleep(3 * time.Second)

	// 在浏览器内重新执行 /api/start，拿当前上下文的 workflowState。
	startPayload, _ := json.Marshal(map[string]interface{}{
		"workflowID": r.WorkflowID,
		"browserData": map[string]interface{}{
			"attributes": map[string]interface{}{
				"fingerprint":     r.GenFP("profile", "PageLoad", 0, ""),
				"eventTimestamp":  time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
				"timeSpentOnPage": "38",
				"eventType":       "PageLoad",
				"ubid":            r.Ubid,
				"visitorId":       r.VisitorID,
			},
			"cookies": map[string]interface{}{},
		},
	})
	startStatus, startBody, err := session.FetchPost(r.Cfg.ProfileBase+"/api/start", string(startPayload), NewUUID())
	if err != nil {
		return -1, "", fmt.Errorf("浏览器 /api/start 失败: %w", err)
	}
	if startStatus == 200 {
		var sd map[string]interface{}
		if json.Unmarshal([]byte(startBody), &sd) == nil {
			if ws, ok := sd["workflowState"].(string); ok && ws != "" {
				r.WorkflowState = ws
				log.Printf("[WAF] 浏览器 /api/start 已刷新 workflowState: %s...", ws[:min(30, len(ws))])
			}
		}
	} else {
		log.Printf("[WAF] 浏览器 /api/start 非 200: %d（沿用 tls-client 的 workflowState）", startStatus)
	}

	// 更新 payload：用浏览器侧拿到的 workflowState + 新事件时间戳。
	payload["workflowState"] = r.WorkflowState
	if attrs, ok := payload["browserData"].(map[string]interface{}); ok {
		if a, ok := attrs["attributes"].(map[string]interface{}); ok {
			a["eventTimestamp"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}

	payloadJSON, _ := json.Marshal(payload)
	return session.FetchPost(r.Cfg.ProfileBase+"/api/send-otp", string(payloadJSON), NewUUID())
}

// truncateStr 截断字符串到 n 字节。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Step10GetOTP 等待验证码 (临时邮箱 / Outlook IMAP / HTTP API)
func (r *Registrar) Step10GetOTP() (string, error) {
	log.Println("[10] 等待验证码")
	if r.Cfg.UseOutlook && r.Cfg.OutlookAccount != nil {
		code, err := email.WaitForOTP(*r.Cfg.OutlookAccount, r.OutlookMailCount, 120, 5)
		if err != nil {
			return "", err
		}
		log.Printf("验证码: %s", code)
		return code, nil
	}
	if r.Cfg.UseHttpAPI && r.Cfg.HttpAPIAccount != nil {
		code, err := email.WaitForHttpAPICode(*r.Cfg.HttpAPIAccount, r.HttpAPISeenCodes, 120, 3)
		if err != nil {
			return "", err
		}
		log.Printf("验证码: %s", code)
		return code, nil
	}
	code, err := r.EmailSvc.WaitForCode(120, 3)
	if err != nil {
		return "", err
	}
	log.Printf("验证码: %s", code)
	return code, nil
}
