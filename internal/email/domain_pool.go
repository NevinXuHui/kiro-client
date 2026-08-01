package email

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DomainState 域名状态（域名池覆盖层：域名本体由 CloudMail 配置自动发现聚合）
type DomainState struct {
	Enabled bool   `json:"enabled"` // 手动启用/禁用（false=注册时跳过）
	Banned  bool   `json:"banned"`  // 自动拉黑（TES 400 BLOCKED 域名级熔断，持久化）
	Note    string `json:"note,omitempty"`
}

// DomainPoolEntry 聚合后的域名池条目（供 UI 展示）
type DomainPoolEntry struct {
	Domain      string `json:"domain"`
	ConfigCount int    `json:"configCount"` // 提供该域名的 Cloud-Mail 服务器数
	Enabled     bool   `json:"enabled"`
	Banned      bool   `json:"banned"`
}

var (
	domainPoolMu     sync.RWMutex
	domainPoolPath   string
	domainPoolLoaded bool
	domainStates     map[string]DomainState
)

// InitDomainPool 初始化域名池（应用启动时调用）
func InitDomainPool(dataDir string) {
	domainPoolMu.Lock()
	defer domainPoolMu.Unlock()
	domainPoolPath = filepath.Join(dataDir, "domain_pool.json")
	domainPoolLoaded = false
	_ = loadDomainPoolLocked()
}

func loadDomainPoolLocked() error {
	if domainPoolLoaded {
		return nil
	}
	domainStates = map[string]DomainState{}
	domainPoolLoaded = true
	b, err := os.ReadFile(domainPoolPath)
	if err != nil {
		return nil
	}
	var m map[string]DomainState
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	for k, v := range m {
		domainStates[k] = v
	}
	return nil
}

func saveDomainPoolLocked() error {
	if domainPoolPath == "" {
		return nil
	}
	b, err := json.MarshalIndent(domainStates, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(domainPoolPath), 0o755); err != nil {
		return err
	}
	tmp := domainPoolPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, domainPoolPath)
}

// ensureDomainLocked 取状态（缺省：启用、未拉黑）
func ensureDomainLocked(domain string) DomainState {
	st, ok := domainStates[domain]
	if !ok {
		st = DomainState{Enabled: true}
	}
	return st
}

// ListDomainPool 聚合所有 Cloud-Mail 配置发现的域名 + 池状态覆盖
func ListDomainPool() []DomainPoolEntry {
	domainPoolMu.Lock()
	defer domainPoolMu.Unlock()
	_ = loadDomainPoolLocked()

	// 域名 → 配置数（来自所有 Cloud-Mail 配置的域名列表）
	configCount := map[string]int{}
	for _, cfg := range GetCloudMailConfigs() {
		for _, d := range cfg.Domains {
			configCount[d]++
		}
	}

	// 池中记录但配置已不存在的域名：清理（配置删除后域名自然消失）
	for d := range domainStates {
		if _, ok := configCount[d]; !ok {
			delete(domainStates, d)
		}
	}

	entries := make([]DomainPoolEntry, 0, len(configCount))
	for d, n := range configCount {
		st := ensureDomainLocked(d)
		entries = append(entries, DomainPoolEntry{
			Domain:      d,
			ConfigCount: n,
			Enabled:     st.Enabled,
			Banned:      st.Banned,
		})
	}
	return entries
}

// SetDomainEnabled 手动启用/禁用域名
func SetDomainEnabled(domain string, enabled bool) {
	domainPoolMu.Lock()
	defer domainPoolMu.Unlock()
	_ = loadDomainPoolLocked()
	st := ensureDomainLocked(domain)
	st.Enabled = enabled
	domainStates[domain] = st
	_ = saveDomainPoolLocked()
}

// MarkDomainBanned 标记域名被 TES 拉黑（coordinator 熔断时调用，持久化）
func MarkDomainBanned(domain string) {
	domainPoolMu.Lock()
	defer domainPoolMu.Unlock()
	_ = loadDomainPoolLocked()
	st := ensureDomainLocked(domain)
	st.Banned = true
	st.Enabled = false
	domainStates[domain] = st
	_ = saveDomainPoolLocked()
}

// UnbanDomain 解除拉黑（同时恢复启用）
func UnbanDomain(domain string) {
	domainPoolMu.Lock()
	defer domainPoolMu.Unlock()
	_ = loadDomainPoolLocked()
	st := ensureDomainLocked(domain)
	st.Banned = false
	st.Enabled = true
	domainStates[domain] = st
	_ = saveDomainPoolLocked()
}

// LoadBannedDomains 返回所有已拉黑域名（任务启动时预加载）
func LoadBannedDomains() []string {
	domainPoolMu.Lock()
	defer domainPoolMu.Unlock()
	_ = loadDomainPoolLocked()
	var out []string
	for d, st := range domainStates {
		if st.Banned {
			out = append(out, d)
		}
	}
	return out
}

// LoadDisabledDomains 返回所有手动禁用域名（任务启动时预加载）
func LoadDisabledDomains() []string {
	domainPoolMu.Lock()
	defer domainPoolMu.Unlock()
	_ = loadDomainPoolLocked()
	var out []string
	for d, st := range domainStates {
		if !st.Enabled {
			out = append(out, d)
		}
	}
	return out
}
