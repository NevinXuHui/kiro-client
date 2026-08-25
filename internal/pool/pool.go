package pool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Strategy 号池策略
type Strategy string

const (
	StrategySequential Strategy = "sequential" // 顺序
	StrategyRoundRobin Strategy = "roundrobin" // 轮询
	StrategyRandom     Strategy = "random"     // 随机
)

// Account Kiro 账号结构（从 accounts.json 导入）
type Account struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
	Email        string `json:"email"`
	Password     string `json:"password,omitempty"`
	Provider     string `json:"provider"`
	ProxyIP      string `json:"proxy_ip,omitempty"`
	ProxyRegion  string `json:"proxy_region,omitempty"`
	Region       string `json:"region"`
	Subscription string `json:"subscription"`
	CreditLimit  int    `json:"creditLimit"`
	CreditUsed   int    `json:"creditUsed"`
	CreditResetAt string `json:"creditResetAt,omitempty"` // 额度重置时间（9router nextDateReset）
	ProfileArn    string `json:"profileArn,omitempty"`    // CodeWhisperer profile ARN（IdC 用量接口需要）
	Time          string `json:"time"`

	// 运行时字段（不持久化，从 API 实时查询）
	HealthStatus    string                 `json:"healthStatus,omitempty"`    // healthy / unhealthy / unknown
	HealthCode      string                 `json:"healthCode,omitempty"`      // 细分状态码：AUTH / 429 / 502 / 503 / 504 / NET / 其他数字（9router 同款分类）
	HealthError     string                 `json:"healthError,omitempty"`     // 健康检测错误信息
	HealthCheckedAt string                 `json:"healthCheckedAt,omitempty"` // 上次检测时间
	UsageQuotas     map[string]interface{} `json:"usageQuotas,omitempty"`       // 额度详情
	AvailableModels []string               `json:"availableModels,omitempty"` // 可用模型列表
	LastUpdatedAt   string                 `json:"lastUpdatedAt,omitempty"`   // 上次刷新时间
}

// Pool 号池
type Pool struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Strategy  Strategy   `json:"strategy"`
	Accounts  []*Account `json:"accounts"` // 改为 Account 对象数组
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// Manager 号池管理器
type Manager struct {
	mu       sync.RWMutex
	pools    map[string]*Pool
	filePath string
	index    map[string]int // 轮询索引
}

var defaultManager *Manager
var once sync.Once

// GetManager 获取全局号池管理器
func GetManager(dataDir string) *Manager {
	once.Do(func() {
		defaultManager = &Manager{
			pools:    make(map[string]*Pool),
			filePath: filepath.Join(dataDir, "pools.json"),
			index:    make(map[string]int),
		}
		defaultManager.load()
	})
	return defaultManager
}

// load 从文件加载号池
func (m *Manager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var pools []*Pool
	if err := json.Unmarshal(data, &pools); err != nil {
		return err
	}

	for _, p := range pools {
		m.pools[p.ID] = p
	}
	return nil
}

// save 保存号池到文件
func (m *Manager) save() error {
	pools := make([]*Pool, 0, len(m.pools))
	for _, p := range m.pools {
		pools = append(pools, p)
	}

	data, err := json.MarshalIndent(pools, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(m.filePath, data, 0644)
}

// ValidateAccount 验证账号有效性（参照 9router 标准）
func ValidateAccount(acc *Account) error {
	if acc == nil {
		return fmt.Errorf("account is nil")
	}
	
	// 必须字段
	if acc.Email == "" {
		return fmt.Errorf("email is required")
	}
	if acc.ClientID == "" {
		return fmt.Errorf("clientId is required")
	}
	if acc.ClientSecret == "" {
		return fmt.Errorf("clientSecret is required")
	}
	if acc.RefreshToken == "" {
		return fmt.Errorf("refreshToken is required")
	}
	
	// 9router 验证规则：refreshToken 必须以 "aorAAAAAG" 开头
	if !strings.HasPrefix(acc.RefreshToken, "aorAAAAAG") {
		return fmt.Errorf("invalid refreshToken format (must start with aorAAAAAG)")
	}
	
	return nil
}

// Create 创建号池
func (m *Manager) Create(name string, strategy Strategy, accounts []*Account) (*Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查名称是否已存在
	for _, p := range m.pools {
		if p.Name == name {
			return nil, fmt.Errorf("pool name already exists")
		}
	}

	// 验证所有账号
	validAccounts := make([]*Account, 0)
	for _, acc := range accounts {
		if err := ValidateAccount(acc); err == nil {
			validAccounts = append(validAccounts, acc)
		}
	}

	if len(validAccounts) == 0 {
		return nil, fmt.Errorf("no valid accounts to add")
	}

	pool := &Pool{
		ID:        uuid.New().String(),
		Name:      name,
		Strategy:  strategy,
		Accounts:  validAccounts,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	m.pools[pool.ID] = pool
	if err := m.save(); err != nil {
		delete(m.pools, pool.ID)
		return nil, err
	}

	return pool, nil
}

// Update 更新号池
func (m *Manager) Update(id string, name string, strategy Strategy, accounts []*Account) (*Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[id]
	if !exists {
		return nil, fmt.Errorf("pool not found")
	}

	// 检查名称冲突
	for _, p := range m.pools {
		if p.ID != id && p.Name == name {
			return nil, fmt.Errorf("pool name already exists")
		}
	}

	// 验证所有账号
	validAccounts := make([]*Account, 0)
	for _, acc := range accounts {
		if err := ValidateAccount(acc); err == nil {
			validAccounts = append(validAccounts, acc)
		}
	}

	if len(validAccounts) == 0 {
		return nil, fmt.Errorf("no valid accounts to add")
	}

	pool.Name = name
	pool.Strategy = strategy
	pool.Accounts = validAccounts
	pool.UpdatedAt = time.Now()

	if err := m.save(); err != nil {
		return nil, err
	}

	return pool, nil
}

// Delete 删除号池
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pools[id]; !exists {
		return fmt.Errorf("pool not found")
	}

	delete(m.pools, id)
	delete(m.index, id)
	return m.save()
}

// Get 获取号池
func (m *Manager) Get(id string) (*Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pool, exists := m.pools[id]
	if !exists {
		return nil, fmt.Errorf("pool not found")
	}

	return pool, nil
}

// List 列出所有号池
func (m *Manager) List() []*Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pools := make([]*Pool, 0, len(m.pools))
	for _, p := range m.pools {
		pools = append(pools, p)
	}
	return pools
}

// GetNextAccount 根据策略获取下一个账号
func (m *Manager) GetNextAccount(poolID string) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[poolID]
	if !exists {
		return nil, fmt.Errorf("pool not found")
	}

	if len(pool.Accounts) == 0 {
		return nil, fmt.Errorf("pool is empty")
	}

	var account *Account

	switch pool.Strategy {
	case StrategySequential:
		// 顺序：总是返回第一个
		account = pool.Accounts[0]

	case StrategyRoundRobin:
		// 轮询
		idx := m.index[poolID]
		account = pool.Accounts[idx%len(pool.Accounts)]
		m.index[poolID] = (idx + 1) % len(pool.Accounts)

	case StrategyRandom:
		// 随机
		idx := time.Now().UnixNano() % int64(len(pool.Accounts))
		account = pool.Accounts[idx]

	default:
		account = pool.Accounts[0]
	}

	return account, nil
}

// AddAccountToPool 添加账号到号池（注册后调用）
func (m *Manager) AddAccountToPool(poolID string, account *Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, exists := m.pools[poolID]
	if !exists {
		return fmt.Errorf("pool not found")
	}

	// 验证账号
	if err := ValidateAccount(account); err != nil {
		return fmt.Errorf("invalid account: %w", err)
	}

	// 检查是否已存在（根据 email）
	for _, acc := range pool.Accounts {
		if acc.Email == account.Email {
			return fmt.Errorf("account already exists in pool")
		}
	}

	pool.Accounts = append(pool.Accounts, account)
	pool.UpdatedAt = time.Now()

	return m.save()
}

// ImportAccountsFromFile 从 JSON 文件导入账号（支持单对象或数组）
func ImportAccountsFromFile(filePath string) ([]*Account, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	accounts, err := ParseAccountJSON(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// 验证并过滤有效账号
	validAccounts := make([]*Account, 0)
	invalidCount := 0
	
	for _, acc := range accounts {
		if err := ValidateAccount(acc); err == nil {
			validAccounts = append(validAccounts, acc)
		} else {
			invalidCount++
		}
	}

	if len(validAccounts) == 0 {
		return nil, fmt.Errorf("no valid accounts found in file (all %d accounts failed validation)", invalidCount)
	}

	return validAccounts, nil
}
