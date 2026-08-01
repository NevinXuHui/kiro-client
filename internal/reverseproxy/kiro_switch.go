package reverseproxy

// 本文件（接入适配阶段）为双轨接入方案：灰度染色 + 全局开关 + 异常熔断。
//
// 无侵入设计：不改动 proxy.go / legacy 任何业务代码。接入方（app 层或后续
// 网关改造）在每请求入口调用 UseNewKiroProvider() 决定走新链路（kiro_provider.go）
// 还是 legacy 链路：
//
//	if reverseproxy.UseNewKiroProvider() {
//	    // 新版链路：KiroTranslateChatRequest → KiroEnsureAccessToken → KiroSendChat
//	} else {
//	    // legacy 链路：proxy.go handleChat 原逻辑
//	}
//
// 配置（内存 + 环境变量兜底，可经 app 层持久化到 storage.conf）：
//   - EnableNewKiroProvider：全局布尔开关（环境变量 KIRO_NEW_PROVIDER_ENABLED）
//   - 灰度染色比例 0-100：随机分配指定比例流量走新链路（环境变量 KIRO_GRAY_PERCENT）
//   - 熔断器：新链路连续失败达到阈值后自动降级 legacy（熔断冷却 30s），
//     成功即复位计数。

import (
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	kiroCircuitCooldown   = 30 * time.Second
	kiroDefaultFailThresh = 5
)

// KiroProviderSwitch 双轨切换配置。
type KiroProviderSwitch struct {
	mu sync.RWMutex

	// EnableNewKiroProvider 全局开关：true 时全部流量走新链路。
	Enabled bool
	// GrayPercent 灰度染色比例（0-100）：随机分流比例，Enabled 优先。
	GrayPercent int

	// 熔断器状态
	CircuitOpen      bool
	ConsecutiveFails int
	FailThreshold    int
	CircuitOpenUntil time.Time

	randSrc *rand.Rand
}

// kiroProviderSwitch 包级单例。
var kiroProviderSwitch = &KiroProviderSwitch{
	FailThreshold: kiroDefaultFailThresh,
	randSrc:       rand.New(rand.NewSource(time.Now().UnixNano())),
}

func init() {
	if v := os.Getenv("KIRO_NEW_PROVIDER_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			kiroProviderSwitch.Enabled = b
		}
	}
	if v := os.Getenv("KIRO_GRAY_PERCENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			kiroProviderSwitch.setGrayPercent(n)
		}
	}
}

func (s *KiroProviderSwitch) setGrayPercent(p int) {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	s.GrayPercent = p
}

// SetKiroProviderEnabled 全局开关：true 全部走新链路。
func SetKiroProviderEnabled(enabled bool) {
	kiroProviderSwitch.mu.Lock()
	kiroProviderSwitch.Enabled = enabled
	kiroProviderSwitch.mu.Unlock()
}

// KiroProviderEnabled 读取全局开关。
func KiroProviderEnabled() bool {
	kiroProviderSwitch.mu.RLock()
	defer kiroProviderSwitch.mu.RUnlock()
	return kiroProviderSwitch.Enabled
}

// SetKiroGrayPercent 设置灰度染色比例（0-100，越界钳位）。
func SetKiroGrayPercent(percent int) {
	kiroProviderSwitch.mu.Lock()
	kiroProviderSwitch.setGrayPercent(percent)
	kiroProviderSwitch.mu.Unlock()
}

// KiroGrayPercent 读取灰度比例。
func KiroGrayPercent() int {
	kiroProviderSwitch.mu.RLock()
	defer kiroProviderSwitch.mu.RUnlock()
	return kiroProviderSwitch.GrayPercent
}

// SetKiroFailThreshold 设置熔断连续失败阈值（<=0 恢复默认 5）。
func SetKiroFailThreshold(n int) {
	kiroProviderSwitch.mu.Lock()
	if n <= 0 {
		n = kiroDefaultFailThresh
	}
	kiroProviderSwitch.FailThreshold = n
	kiroProviderSwitch.mu.Unlock()
}

// ResetKiroCircuitBreaker 手动复位熔断器（开关/回滚操作后调用）。
func ResetKiroCircuitBreaker() {
	kiroProviderSwitch.mu.Lock()
	kiroProviderSwitch.CircuitOpen = false
	kiroProviderSwitch.ConsecutiveFails = 0
	kiroProviderSwitch.CircuitOpenUntil = time.Time{}
	kiroProviderSwitch.mu.Unlock()
}

// UseNewKiroProvider 请求级决策：本请求是否走新链路。
// 优先级：熔断否决（安全兜底，覆盖一切）> 全局开关 > 灰度随机。
func UseNewKiroProvider() bool {
	kiroProviderSwitch.mu.Lock()
	defer kiroProviderSwitch.mu.Unlock()
	s := kiroProviderSwitch

	// 熔断为最高优先级：即使运维显式开启全局开关，熔断期也强制 legacy
	if s.CircuitOpen {
		// 熔断冷却到期自动复位
		if time.Now().Before(s.CircuitOpenUntil) {
			return false
		}
		s.CircuitOpen = false
		s.ConsecutiveFails = 0
	}
	if s.Enabled {
		return true
	}
	if s.GrayPercent <= 0 {
		return false
	}
	if s.GrayPercent >= 100 {
		return true
	}
	return s.randSrc.Intn(100) < s.GrayPercent
}

// KiroCircuitTripped 熔断是否开启（开启期间新链路请求应短路，由网关返回 503
// 而非继续打上游）。双轨已整合为单轨后，这是熔断器的落地检查点。
func KiroCircuitTripped() bool {
	kiroProviderSwitch.mu.RLock()
	defer kiroProviderSwitch.mu.RUnlock()
	s := kiroProviderSwitch
	if !s.CircuitOpen {
		return false
	}
	if time.Now().Before(s.CircuitOpenUntil) {
		return true
	}
	// 冷却到期自动复位（延迟写锁）
	kiroProviderSwitch.mu.RUnlock()
	kiroProviderSwitch.mu.Lock()
	s.CircuitOpen = false
	s.ConsecutiveFails = 0
	kiroProviderSwitch.mu.Unlock()
	return false
}

// KiroProviderFailure 记录新链路失败：连续失败达阈值 → 熔断开启（冷却 30s），
// 期间 KiroCircuitTripped 返回 true（网关 503 短路）。
func KiroProviderFailure() {
	kiroProviderSwitch.mu.Lock()
	defer kiroProviderSwitch.mu.Unlock()
	s := kiroProviderSwitch
	s.ConsecutiveFails++
	if s.ConsecutiveFails >= s.FailThreshold {
		s.CircuitOpen = true
		s.CircuitOpenUntil = time.Now().Add(kiroCircuitCooldown)
	}
}

// KiroProviderSuccess 记录新链路成功：复位连续失败计数。
func KiroProviderSuccess() {
	kiroProviderSwitch.mu.Lock()
	defer kiroProviderSwitch.mu.Unlock()
	s := kiroProviderSwitch
	s.ConsecutiveFails = 0
	s.CircuitOpen = false
	s.CircuitOpenUntil = time.Time{}
}
