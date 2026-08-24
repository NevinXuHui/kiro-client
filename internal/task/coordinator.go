package task

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"reg_go/internal/core"
	"reg_go/internal/data"
	"reg_go/internal/email"
	"reg_go/internal/pool"
	"reg_go/internal/proxy"
	"reg_go/internal/storage"
)

// StartTaskRequest 启动任务请求
type StartTaskRequest struct {
	Count         int    `json:"count"`
	Concurrency   int    `json:"concurrency"`
	Delay         int    `json:"delay"`
	OutputPath    string `json:"outputPath"`
	EmailProvider string `json:"emailProvider"` // "outlook" / "cloudmail" / "httpapi"

	CloudMailDomains    []string                           `json:"cloudmailDomains"`
	CloudMailConfigs    map[string][]email.CloudMailConfig `json:"cloudmailConfigs"`
	CloudMailRandomMode bool                               `json:"cloudmailRandomMode"`
}

// StartTask 公开方法（包装器）
func StartTask(req StartTaskRequest) map[string]interface{} {
	return startTask(req)
}

// startTask 启动注册任务（私有方法）
func startTask(req StartTaskRequest) map[string]interface{} {
	Manager.mu.Lock()
	if Manager.running {
		Manager.mu.Unlock()
		return map[string]interface{}{"error": "任务正在运行中"}
	}

	// 根据邮箱提供商类型处理
	emailProvider := req.EmailProvider
	if emailProvider == "" {
		emailProvider = "outlook" // 默认使用 Outlook
	}

	var outlookAccounts []email.OutlookAccount
	var httpAPIAccounts []email.HttpAPIAccount

	if emailProvider == "cloudmail" {
		if len(req.CloudMailDomains) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "请选择至少一个 cloud-mail 域名"}
		}
		if len(req.CloudMailConfigs) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "cloud-mail 配置缺失"}
		}
	} else if emailProvider == "httpapi" {
		storedAccounts := storage.GetHttpAPICached()
		if len(storedAccounts) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "请先添加 HTTP 邮箱账号"}
		}
		for _, acc := range storedAccounts {
			registered, _ := acc["registered"].(bool)
			if registered {
				continue
			}
			emailAddr, _ := acc["email"].(string)
			apiURL, _ := acc["apiUrl"].(string)
			if emailAddr == "" || apiURL == "" {
				continue
			}
			httpAPIAccounts = append(httpAPIAccounts, email.HttpAPIAccount{
				Email:  emailAddr,
				APIURL: apiURL,
			})
		}
		if len(httpAPIAccounts) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "没有可用的 HTTP 邮箱账号（所有账号已注册成功）"}
		}
		if len(httpAPIAccounts) < req.Count {
			Manager.mu.Unlock()
			return map[string]interface{}{
				"error": fmt.Sprintf("可用 HTTP 邮箱不足: 需要 %d, 仅有 %d", req.Count, len(httpAPIAccounts)),
			}
		}
	} else {
		// Outlook 模式：加载账号列表
		storedAccounts := storage.GetAccountsCached()
		if len(storedAccounts) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "请先添加 Outlook 账号"}
		}

		// 筛选未注册的账号
		for _, acc := range storedAccounts {
			registered, _ := acc["registered"].(bool)
			if !registered {
				emailAddr, _ := acc["email"].(string)
				password, _ := acc["password"].(string)
				clientID, _ := acc["clientId"].(string)
				refreshToken, _ := acc["refreshToken"].(string)

				outlookAccounts = append(outlookAccounts, email.OutlookAccount{
					Email:        emailAddr,
					Password:     password,
					ClientID:     clientID,
					RefreshToken: refreshToken,
				})
			}
		}

		if len(outlookAccounts) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "没有可用的 Outlook 账号（所有账号已注册成功）"}
		}

		if len(outlookAccounts) < req.Count {
			Manager.mu.Unlock()
			return map[string]interface{}{
				"error": fmt.Sprintf("可用 Outlook 账号不足: 需要 %d, 仅有 %d", req.Count, len(outlookAccounts)),
			}
		}
	}

	// 初始化状态
	Manager.running = true
	Manager.stopCh = make(chan struct{})
	Manager.total = req.Count
	Manager.completed = 0
	Manager.success = 0
	Manager.failed = 0
	Manager.results = nil
	Manager.startTime = time.Now()
	Manager.mu.Unlock()

	// 清空日志
	Manager.logsMu.Lock()
	Manager.logs = nil
	Manager.logsMu.Unlock()

	// 后台执行
	go runBatch(req, emailProvider, outlookAccounts, httpAPIAccounts)

	return map[string]interface{}{"status": "started"}
}

// StopTask 停止任务（强制取消所有 HTTP 请求）
func StopTask(force bool) map[string]interface{} {
	Manager.mu.Lock()
	if !Manager.running {
		Manager.mu.Unlock()
		return map[string]interface{}{"error": "没有正在运行的任务"}
	}

	select {
	case <-Manager.stopCh:
	default:
		close(Manager.stopCh)
	}

	// 强制取消所有进行中的 HTTP 请求
	if Manager.cancelFunc != nil {
		Manager.cancelFunc()
	}

	Manager.running = false
	log.Println("[Kiro] 任务已强制停止，所有请求已取消")
	Manager.mu.Unlock()
	return map[string]interface{}{"status": "force_stopped"}
}

// runBatch 执行批量注册
func runBatch(req StartTaskRequest, emailProvider string, outlookAccounts []email.OutlookAccount, httpAPIAccounts []email.HttpAPIAccount) {
	// 创建可取消的 context，停止时立即中断所有 HTTP 请求
	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()

	Manager.mu.Lock()
	Manager.cancelFunc = taskCancel
	Manager.mu.Unlock()

	defer func() {
		Manager.mu.Lock()
		Manager.running = false
		Manager.cancelFunc = nil
		Manager.mu.Unlock()
	}()

	outDir := req.OutputPath
	if outDir == "" {
		outDir = storage.GetResultOutputDir()
	}
	os.MkdirAll(outDir, 0755)

	taskConfig := core.NewConfig()
	taskConfig.EmailProvider = emailProvider
	taskConfig.Proxy = storage.GetProxy()
	if taskConfig.Proxy != "" {
		log.Printf("[Kiro] 已启用代理")
	}

	// 预先准备 CloudMail 域名池
	var cloudmailDomainPool []string
	var cloudmailDomainConfigs map[string][]email.CloudMailConfig
	if emailProvider == "cloudmail" {
		taskConfig.UseCloudMail = true
		cloudmailDomainPool = req.CloudMailDomains
		cloudmailDomainConfigs = req.CloudMailConfigs

		if len(cloudmailDomainPool) == 0 || len(cloudmailDomainConfigs) == 0 {
			log.Println("[Kiro] cloud-mail 域名或配置为空，任务终止")
			Manager.mu.Lock()
			Manager.running = false
			Manager.mu.Unlock()
			return
		}

		log.Printf("[Kiro] cloud-mail 域名池: %v (共 %d 个域名)", cloudmailDomainPool, len(cloudmailDomainPool))
	} else if emailProvider == "httpapi" {
		taskConfig.UseHttpAPI = true
	} else if emailProvider == "outlook" {
		taskConfig.UseOutlook = true
	}

	// 统计计数器
	var statsMu sync.Mutex
	var taskDurations []float64
	var failRegistered, failNetwork, failBanned, failOther int
	taskStartTime := time.Now()

	// 共享账号池（并发安全），goroutine 动态领取账号（Outlook / HTTP API）
	var accountPoolMu sync.Mutex
	accountPoolIdx := 0
	nextAccount := func() (email.OutlookAccount, bool) {
		accountPoolMu.Lock()
		defer accountPoolMu.Unlock()
		if accountPoolIdx >= len(outlookAccounts) {
			return email.OutlookAccount{}, false
		}
		acc := outlookAccounts[accountPoolIdx]
		accountPoolIdx++
		return acc, true
	}
	httpAPIPoolIdx := 0
	nextHttpAPIAccount := func() (email.HttpAPIAccount, bool) {
		accountPoolMu.Lock()
		defer accountPoolMu.Unlock()
		if httpAPIPoolIdx >= len(httpAPIAccounts) {
			return email.HttpAPIAccount{}, false
		}
		acc := httpAPIAccounts[httpAPIPoolIdx]
		httpAPIPoolIdx++
		return acc, true
	}

	// 逐代理熔断标记集合：记录已被 AWS 风控拦截的代理地址
	var bannedProxiesMu sync.Mutex
	bannedProxies := map[string]bool{}

	// 域名级熔断标记集合：记录已被 TES 拉黑的 cloud-mail 域名
	// （send-otp 400 BLOCKED = 域名在黑名单，与 IP/代理无关，换代理无效）
	// 预加载域名池持久化状态：拉黑/禁用域名重启后仍然生效
	var bannedDomainsMu sync.Mutex
	bannedDomains := map[string]bool{}
	if emailProvider == "cloudmail" {
		for _, d := range email.LoadBannedDomains() {
			bannedDomains[d] = true
		}
	}
	var disabledDomainsMu sync.RWMutex
	disabledDomains := map[string]bool{}
	if emailProvider == "cloudmail" {
		for _, d := range email.LoadDisabledDomains() {
			disabledDomains[d] = true
		}
	}

	// CloudMail 域名池索引（并发安全）——跳过被拉黑/禁用域名
	var cloudmailDomainIdx int
	var cloudmailDomainMu sync.Mutex
	nextCloudMailDomain := func() (string, email.CloudMailConfig) {
		cloudmailDomainMu.Lock()
		defer cloudmailDomainMu.Unlock()

		// 最多尝试全池轮数，跳过已被 TES 拉黑或手动禁用的域名
		for i := 0; i < len(cloudmailDomainPool); i++ {
			var domain string
			if req.CloudMailRandomMode {
				domain = cloudmailDomainPool[rand.Intn(len(cloudmailDomainPool))]
			} else {
				domain = cloudmailDomainPool[cloudmailDomainIdx%len(cloudmailDomainPool)]
				cloudmailDomainIdx++
			}
			bannedDomainsMu.Lock()
			_, banned := bannedDomains[domain]
			bannedDomainsMu.Unlock()
			if banned {
				continue
			}
			disabledDomainsMu.RLock()
			_, disabled := disabledDomains[domain]
			disabledDomainsMu.RUnlock()
			if disabled {
				continue
			}
			configs := cloudmailDomainConfigs[domain]
			return domain, configs[rand.Intn(len(configs))]
		}
		return "", email.CloudMailConfig{}
	}
	doTask := func(i int) {
		select {
		case <-Manager.stopCh:
			return
		default:
		}

		taskCfg := *taskConfig
		taskCfg.Password = core.GenPassword()
		// 多代理池：若存在启用项，按权重抽签覆盖单代理
		if picked := proxy.PickRandom(); picked != "" {
			taskCfg.Proxy = picked
			log.Printf("[Kiro][%d/%d] 选中代理 %s", i+1, req.Count, picked)
		}
		var currentEmail string

		// cloud-mail 模式专用：当前域名 + 创建邮箱闭包（换域名重试时复用）
		var currentDomain string
		newCloudMailMailbox := func() (string, bool) { return "", false }

		// 根据邮箱提供商类型获取邮箱
		if emailProvider == "outlook" {
			// Outlook 模式：从共享池领取账号
			acc, ok := nextAccount()
			if !ok {
				log.Printf("[Kiro][%d/%d] 无可用账号，跳过", i+1, req.Count)
				Manager.mu.Lock()
				Manager.completed++
				Manager.failed++
				Manager.mu.Unlock()
				return
			}
			taskCfg.OutlookAccount = &acc
			currentEmail = acc.Email
		} else if emailProvider == "httpapi" {
			acc, ok := nextHttpAPIAccount()
			if !ok {
				log.Printf("[Kiro][%d/%d] 无可用 HTTP 邮箱，跳过", i+1, req.Count)
				Manager.mu.Lock()
				Manager.completed++
				Manager.failed++
				Manager.mu.Unlock()
				return
			}
			taskCfg.HttpAPIAccount = &acc
			currentEmail = acc.Email
		} else if emailProvider == "cloudmail" {
			// 创建 cloud-mail 邮箱（换域名重试时复用）。返回邮箱地址与是否成功。
			newCloudMailMailbox = func() (string, bool) {
				domain, config := nextCloudMailDomain()
				if domain == "" {
					return "", false
				}
				emailName := email.GenerateEmailName(i)
				currentDomain = domain

				log.Printf("[Kiro][%d/%d] 创建 cloud-mail 邮箱: %s@%s (配置: %s)", i+1, req.Count, emailName, domain, config.Name)

				provider, err := email.NewCloudMailProvider(config, emailName, domain)
				if err != nil {
					log.Printf("[Kiro][%d/%d] 生成 cloud-mail 邮箱失败: %v", i+1, req.Count, err)
					return "", false
				}

				taskCfg.CloudMailProvider = provider
				cfgCopy := config
				taskCfg.CloudMailConfig = &cfgCopy
				return provider.GetAddress(), true
			}

			var ok bool
			currentEmail, ok = newCloudMailMailbox()
			if !ok {
				Manager.mu.Lock()
				Manager.completed++
				Manager.failed++
				Manager.mu.Unlock()
				return
			}
		}

		log.Printf("[Kiro][%d/%d] 开始注册", i+1, req.Count)
		itemStart := time.Now()

		const maxAttempts = 2
		// 代理切换预算独立于 attempt：被风控封的代理不算"重试"，只算换路
		const maxProxySwitches = 10
		proxySwitches := 0

		var result map[string]interface{}
	retryLoop:
		for attempt := 0; attempt < maxAttempts; attempt++ {
			// 每次重试前检查停止信号
			select {
			case <-Manager.stopCh:
				return
			default:
			}

			if attempt > 0 {
				log.Printf("[Kiro][%d/%d] 第 %d 次重试", i+1, req.Count, attempt)
				select {
				case <-Manager.stopCh:
					return
				case <-time.After(time.Duration(2+attempt) * time.Second):
				}
			}

			if taskCtx.Err() != nil {
				return
			}

			reg := core.NewRegistrar(&taskCfg)
			reg.Ctx = taskCtx
			reg.TaskLabel = fmt.Sprintf("%d/%d", i+1, req.Count)
			result = reg.Run()

			if result["status"] == "success" {
				break
			}

			errorMsg, _ := result["error"].(string)

			// 邮箱域名被 TES 拉黑（cloudmail）：标记域名 → 换域名重建邮箱重试。
			// 必须放在 isKillSwitchError 之前（文案含"注册被拦截"前缀）。
			if emailProvider == "cloudmail" && strings.Contains(errorMsg, "域名已被拉黑") {
				bannedDomainsMu.Lock()
				bannedDomains[currentDomain] = true
				bannedCount := len(bannedDomains)
				bannedDomainsMu.Unlock()
				// 持久化到域名池（重启后仍然跳过该域名）
				email.MarkDomainBanned(currentDomain)

				log.Printf("[Kiro][%d/%d] ⚠️ 邮箱域名 %s 被 TES 拉黑，已标记 (%d/%d 域名被封)",
					i+1, req.Count, currentDomain, bannedCount, len(cloudmailDomainPool))

				newEmail, ok := newCloudMailMailbox()
				if !ok {
					log.Printf("[Kiro][%d/%d] 所有域名均被拉黑，放弃重试", i+1, req.Count)
					break
				}
				currentEmail = newEmail
				attempt = -1 // 换域名：重置重试预算
				log.Printf("[Kiro][%d/%d] 换域名重试: %s", i+1, req.Count, currentEmail)
				continue retryLoop
			}

			// AWS 风控拦截（逐代理）：标记当前代理不可用，换代理重试，不灭门
			// 只有所有代理都被封时才终止任务
			if isKillSwitchError(errorMsg) {
				bannedProxiesMu.Lock()
				bannedProxies[taskCfg.Proxy] = true
				bannedCount := len(bannedProxies)
				bannedProxiesMu.Unlock()

				log.Printf("[Kiro][%d/%d] ⚠️ 代理 %s 被 AWS 风控拦截，已标记 (%d/%d 代理被封)",
					i+1, req.Count, taskCfg.Proxy, bannedCount, proxy.CountEnabled())

				// 尝试换代理重试
				var newProxy string
				for pickAttempt := 0; pickAttempt < 20; pickAttempt++ {
					candidate := proxy.PickRandom()
					if candidate == "" {
						break
					}
					bannedProxiesMu.Lock()
					isBanned := bannedProxies[candidate]
					bannedProxiesMu.Unlock()
					if !isBanned && candidate != taskCfg.Proxy {
						newProxy = candidate
						break
					}
				}

				if newProxy != "" && proxySwitches < maxProxySwitches {
					proxySwitches++
					taskCfg.Proxy = newProxy
					attempt-- // 代理切换不消耗常规重试预算
					log.Printf("[Kiro][%d/%d] 换代理 %s 重试 (代理切换 %d/%d)",
						i+1, req.Count, newProxy, proxySwitches, maxProxySwitches)
					continue retryLoop
				}

				log.Printf("[Kiro][%d/%d] 无可用代理（全部被封），放弃重试", i+1, req.Count)
				break
			}

			// 邮箱已注册：标记当前账号，换号重来（重置 attempt）
			if (taskConfig.UseOutlook || taskConfig.UseHttpAPI) && strings.Contains(errorMsg, "邮箱已注册过") {
				log.Printf("[Kiro][%d/%d] %s 已注册，标记并换号", i+1, req.Count, currentEmail)
				if taskConfig.UseOutlook {
					email.UpdateAccountStatus(currentEmail, true, false)
					acc, ok := nextAccount()
					if ok {
						taskCfg.OutlookAccount = &acc
						taskCfg.Password = core.GenPassword()
						currentEmail = acc.Email
						attempt = -1
						continue retryLoop
					}
				} else {
					email.UpdateHttpAPIAccountStatus(currentEmail, true, false)
					acc, ok := nextHttpAPIAccount()
					if ok {
						taskCfg.HttpAPIAccount = &acc
						taskCfg.Password = core.GenPassword()
						currentEmail = acc.Email
						attempt = -1
						continue retryLoop
					}
				}
				log.Printf("[Kiro][%d/%d] 账号池已耗尽", i+1, req.Count)
				break
			}

			// Point of no return：Step12 已完成但整体失败 → 邮箱已消耗，不换代理重试
			if pwSet, _ := result["passwordSet"].(bool); pwSet {
				log.Printf("[Kiro][%d/%d] 密码已设置但验活失败，邮箱已消耗，不再重试", i+1, req.Count)
				break
			}

			// 不重试的错误类型（含 context 取消 / 被封 / 临时邮箱重复）
			noRetryErrors := []string{"suspended", "临时邮箱不可能已存在", "邮箱创建失败", "context canceled", "context deadline exceeded"}
			shouldRetry := true
			for _, noRetry := range noRetryErrors {
				if strings.Contains(errorMsg, noRetry) {
					shouldRetry = false
					break
				}
			}

			if !shouldRetry || attempt >= maxAttempts-1 {
				break
			}

			log.Printf("[Kiro][%d/%d] 注册失败: %s，准备重试", i+1, req.Count, errorMsg)
		}

		itemDuration := time.Since(itemStart).Seconds()

		Manager.mu.Lock()
		Manager.results = append(Manager.results, result)
		Manager.completed++

		success := result["status"] == "success"
		if success {
			Manager.success++
		} else {
			Manager.failed++
		}
		completedCount := Manager.completed
		Manager.mu.Unlock()

		// 统计分类
		statsMu.Lock()
		taskDurations = append(taskDurations, itemDuration)
		if !success {
			errorMsg, _ := result["error"].(string)
			errClass := classifyError(errorMsg)
			switch errClass {
			case "registered":
				failRegistered++
			case "banned":
				failBanned++
			default:
				if strings.Contains(errorMsg, "timeout") || strings.Contains(errorMsg, "网络") || strings.Contains(errorMsg, "connection") || strings.Contains(errorMsg, "TLS") {
					failNetwork++
				} else {
					failOther++
				}
			}
		}
		statsMu.Unlock()

		// log.Printf 必须在 state.mu 外调用，否则与 logWriter 死锁
		if !success {
			if errMsg, ok := result["error"].(string); ok {
				log.Printf("[Kiro][%d/%d] 失败: %s (%s)", completedCount, req.Count, errMsg, currentEmail)
			}
		}

		// 只有设置完密码后（passwordSet=true）才标记邮箱为已注册
		// 之前步骤失败的邮箱不标记，等同于归还到邮箱池
		if taskConfig.UseOutlook && currentEmail != "" {
			passwordSet, _ := result["passwordSet"].(bool)
			if passwordSet {
				email.UpdateAccountStatus(currentEmail, true, success)
			}
		}
		if taskConfig.UseHttpAPI && currentEmail != "" {
			passwordSet, _ := result["passwordSet"].(bool)
			if passwordSet {
				email.UpdateHttpAPIAccountStatus(currentEmail, true, success)
			}
		}
		if success {
			if err := data.SaveKiroSuccess(result, outDir); err != nil {
				log.Printf("[Kiro] 保存结果失败: %v", err)
			}
			// 自动入库：将成功注册的账号添加到默认号池
			autoAddToPool(result)
		}
	}

	if req.Concurrency > 1 {
		log.Printf("[Kiro] 启动并发任务: %d 个任务，并发数 %d", req.Count, req.Concurrency)
		sem := make(chan struct{}, req.Concurrency)
		var wg sync.WaitGroup
	loop:
		for i := 0; i < req.Count; i++ {
			select {
			case <-Manager.stopCh:
				break loop
			default:
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()
				doTask(idx)
			}(i)
		}
		wg.Wait()
	} else {
		log.Printf("[Kiro] 启动串行任务: %d 个任务", req.Count)
		for i := 0; i < req.Count; i++ {
			select {
			case <-Manager.stopCh:
				log.Println("任务已停止")
				return
			default:
			}
			doTask(i)
			if req.Delay > 0 && i < req.Count-1 {
				time.Sleep(time.Duration(req.Delay) * time.Second)
			}
		}
	}

	totalDuration := time.Since(taskStartTime).Seconds()

	Manager.mu.Lock()
	sucCount := Manager.success
	failCount := Manager.failed
	totalCount := Manager.completed
	Manager.mu.Unlock()

	// 计算平均耗时
	var avgDur float64
	if len(taskDurations) > 0 {
		var sum float64
		for _, d := range taskDurations {
			sum += d
		}
		avgDur = sum / float64(len(taskDurations))
	}

	// 统计报告
	log.Println("[Kiro] ═══════════════════════════════")
	log.Printf("[Kiro] 任务完成 — 总计: %d, 成功: %d, 失败: %d", totalCount, sucCount, failCount)
	log.Printf("[Kiro] 总耗时: %.1fs, 平均耗时: %.1fs/个", totalDuration, avgDur)
	if totalCount > 0 {
		log.Printf("[Kiro] 成功率: %.1f%%", float64(sucCount)/float64(totalCount)*100)
	}
	if failCount > 0 {
		log.Printf("[Kiro] 失败明细:")
		if failRegistered > 0 {
			log.Printf("[Kiro]   邮箱已注册: %d (%.0f%%)", failRegistered, float64(failRegistered)/float64(totalCount)*100)
		}
		if failBanned > 0 {
			log.Printf("[Kiro]   账号封禁: %d (%.0f%%)", failBanned, float64(failBanned)/float64(totalCount)*100)
		}
		if failNetwork > 0 {
			log.Printf("[Kiro]   网络问题: %d (%.0f%%)", failNetwork, float64(failNetwork)/float64(totalCount)*100)
		}
		if failOther > 0 {
			log.Printf("[Kiro]   其他错误: %d (%.0f%%)", failOther, float64(failOther)/float64(totalCount)*100)
		}
	}
	if sucCount > 0 {
		log.Printf("[Kiro] 成功结果: %s", outDir)
	}
	log.Println("[Kiro] ═══════════════════════════════")
}

// classifyError 根据错误信息粗分类，用于统计展示。
func classifyError(errorMsg string) string {
	if errorMsg == "" {
		return "failed"
	}
	if strings.Contains(errorMsg, "suspended") {
		return "banned"
	}
	if strings.Contains(errorMsg, "邮箱已注册过") || strings.Contains(errorMsg, "临时邮箱不可能已存在") {
		return "registered"
	}
	return "failed"
}

// isKillSwitchError 判断该错误是否属于逐代理熔断级错误（该代理的 IP 或指纹已被 AWS 风控标记）。
// 不再全局灭门：仅标记当前代理为不可用，触发换代理重试，不影响其他代理上的并发任务。
func isKillSwitchError(errorMsg string) bool {
	if errorMsg == "" {
		return false
	}
	triggers := []string{
		"注册被拦截",       // formatError 对 BLOCKED/注册请求被拦截 的翻译
		"IP或浏览器指纹被检测", // 指纹/IP 被标记
		"BLOCKED",     // 响应体里直接包含的风控标记（含 Step9 现已传播的 TES BLOCKED）
		"注册请求被拦截",
	}
	for _, t := range triggers {
		if strings.Contains(errorMsg, t) {
			return true
		}
	}
	return false
}

// autoAddToPool 将成功注册的账号自动添加到默认号池
func autoAddToPool(result map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Kiro] 自动入库异常: %v", r)
		}
	}()

	// 提取账号信息
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
	if pw, _ := result["password"].(string); pw != "" {
		acc.Password = pw
	}

	// 填充可选字段
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

	// 查找"默认号池"
	var defaultPool *pool.Pool
	for _, p := range pools {
		if p.Name == "默认号池" {
			defaultPool = p
			break
		}
	}

	if defaultPool == nil {
		// 创建默认号池
		p, err := mgr.Create("默认号池", pool.StrategyRoundRobin, []*pool.Account{acc})
		if err != nil {
			log.Printf("[Kiro] 自动入库创建号池失败: %v", err)
			return
		}
		log.Printf("[Kiro] 自动入库: 创建默认号池并添加 %s", emailAddr)
		_ = p
		return
	}

	// 添加到已有号池
	if err := mgr.AddAccountToPool(defaultPool.ID, acc); err != nil {
		log.Printf("[Kiro] 自动入库失败: %v", err)
		return
	}
	log.Printf("[Kiro] 自动入库: %s → 默认号池", emailAddr)
}
