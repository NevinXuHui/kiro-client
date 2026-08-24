package proxy

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PoolEntry 多代理池条目
type PoolEntry struct {
	ID      string `json:"id"`      // 内部 ID，UI 用
	Name    string `json:"name"`    // 用户可见名称
	URL     string `json:"url"`     // 完整代理 URL（已归一化）
	Weight  int    `json:"weight"`  // 1-100，越高被选中概率越大
	Enabled bool   `json:"enabled"` // 关闭时不参与抽签
}

// poolFile JSON 持久化结构
type poolFile struct {
	Entries []PoolEntry `json:"entries"`
}

const (
	// Power 用于"软最大化"：>1 时拉大权重差，<1 时压平。0.6 保证哪怕权重 1 vs 100 也有 ~6% 概率被选中。
	weightPower = 0.6
)

var (
	poolMu      sync.Mutex
	poolLoaded  bool
	poolEntries []PoolEntry
	poolPath    string
)

// InitPool 在 App 启动时调用一次，传入数据目录
func InitPool(dataDir string) {
	poolMu.Lock()
	defer poolMu.Unlock()
	poolPath = filepath.Join(dataDir, "proxy_pool.json")
	poolLoaded = false
	_ = loadPoolLocked()
}

func loadPoolLocked() error {
	if poolLoaded {
		return nil
	}
	poolEntries = nil
	poolLoaded = true
	b, err := os.ReadFile(poolPath)
	if err != nil {
		return nil
	}
	var pf poolFile
	if err := json.Unmarshal(b, &pf); err != nil {
		return fmt.Errorf("解析代理池失败: %w", err)
	}
	poolEntries = pf.Entries
	return nil
}

func savePoolLocked() error {
	if poolPath == "" {
		return fmt.Errorf("代理池未初始化")
	}
	b, err := json.MarshalIndent(poolFile{Entries: poolEntries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(poolPath), 0o755); err != nil {
		return err
	}
	tmp := poolPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, poolPath)
}

// List 返回当前所有代理（含禁用项）
func List() []PoolEntry {
	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()
	out := make([]PoolEntry, len(poolEntries))
	copy(out, poolEntries)
	return out
}

func newID() string {
	return fmt.Sprintf("p_%d_%04d", time.Now().UnixNano(), rand.Intn(10000))
}

// Add 新增一条代理。url 会被外部归一化后传入。
func Add(entry PoolEntry) (PoolEntry, error) {
	entry.URL = strings.TrimSpace(entry.URL)
	if entry.URL == "" {
		return entry, fmt.Errorf("代理地址不能为空")
	}
	if entry.Weight <= 0 {
		entry.Weight = 50
	}
	if entry.Weight > 100 {
		entry.Weight = 100
	}
	entry.Name = strings.TrimSpace(entry.Name)
	if entry.Name == "" {
		entry.Name = entry.URL
	}
	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()
	for _, e := range poolEntries {
		if e.URL == entry.URL {
			return entry, fmt.Errorf("该代理已存在")
		}
	}
	if entry.ID == "" {
		entry.ID = newID()
	}
	entry.Enabled = true
	poolEntries = append(poolEntries, entry)
	if err := savePoolLocked(); err != nil {
		// 回滚
		poolEntries = poolEntries[:len(poolEntries)-1]
		return entry, err
	}
	return entry, nil
}

// BatchAddResult 批量添加结果
type BatchAddResult struct {
	Success   int      `json:"success"`   // 成功添加数量
	Failed    int      `json:"failed"`    // 失败数量
	Skipped   int      `json:"skipped"`   // 跳过数量（重复）
	Total     int      `json:"total"`     // 总数量
	Errors    []string `json:"errors"`    // 错误信息列表
	Added     []string `json:"added"`     // 成功添加的代理地址
}

// BatchAdd 批量新增代理
func BatchAdd(urls []string, defaultWeight int) BatchAddResult {
	result := BatchAddResult{
		Total:  len(urls),
		Errors: []string{},
		Added:  []string{},
	}

	if defaultWeight <= 0 {
		defaultWeight = 50
	}
	if defaultWeight > 100 {
		defaultWeight = 100
	}

	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()

	// 构建已存在的 URL 集合
	existingURLs := make(map[string]bool)
	for _, e := range poolEntries {
		existingURLs[e.URL] = true
	}

	// 处理每个 URL
	for _, url := range urls {
		url = strings.TrimSpace(url)

		// 跳过空行
		if url == "" {
			continue
		}

		// 跳过注释行
		if strings.HasPrefix(url, "#") || strings.HasPrefix(url, "//") {
			continue
		}

		// 检查是否已存在
		if existingURLs[url] {
			result.Skipped++
			continue
		}

		// 创建条目
		entry := PoolEntry{
			ID:      newID(),
			Name:    url,
			URL:     url,
			Weight:  defaultWeight,
			Enabled: true,
		}

		// 添加到池中
		poolEntries = append(poolEntries, entry)
		existingURLs[url] = true // 防止批量中的重复
		result.Success++
		result.Added = append(result.Added, url)
	}

	// 计算失败数
	result.Failed = result.Total - result.Success - result.Skipped

	// 保存
	if result.Success > 0 {
		if err := savePoolLocked(); err != nil {
			// 保存失败，回滚
			poolEntries = poolEntries[:len(poolEntries)-result.Success]
			result.Errors = append(result.Errors, fmt.Sprintf("保存失败: %v", err))
			result.Failed = result.Success
			result.Success = 0
			result.Added = []string{}
		}
	}

	return result
}

// Update 修改一条（按 id 匹配）。url 不允许改成已存在的另一条。
func Update(id string, patch PoolEntry) (PoolEntry, error) {
	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()
	idx := -1
	for i, e := range poolEntries {
		if e.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return PoolEntry{}, fmt.Errorf("代理不存在")
	}
	cur := poolEntries[idx]
	if name := strings.TrimSpace(patch.Name); name != "" {
		cur.Name = name
	}
	if u := strings.TrimSpace(patch.URL); u != "" && u != cur.URL {
		for j, e := range poolEntries {
			if j != idx && e.URL == u {
				return PoolEntry{}, fmt.Errorf("该代理 URL 已存在")
			}
		}
		cur.URL = u
	}
	if patch.Weight > 0 {
		w := patch.Weight
		if w > 100 {
			w = 100
		}
		cur.Weight = w
	}
	cur.Enabled = patch.Enabled
	poolEntries[idx] = cur
	if err := savePoolLocked(); err != nil {
		return PoolEntry{}, err
	}
	return cur, nil
}

// Delete 按 id 删除
func Delete(id string) error {
	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()
	for i, e := range poolEntries {
		if e.ID == id {
			poolEntries = append(poolEntries[:i], poolEntries[i+1:]...)
			return savePoolLocked()
		}
	}
	return fmt.Errorf("代理不存在")
}

// PickRandom 按权重抽签返回一个启用的代理 URL；池为空或全部禁用返回空串。
// 使用 weightPower 软化：让低权重也有非零概率被命中，避免全部任务落到单一代理。
func PickRandom() string {
	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()

	type cand struct {
		url  string
		soft float64
	}
	candidates := make([]cand, 0, len(poolEntries))
	var total float64
	for _, e := range poolEntries {
		if e.URL == "" {
			continue
		}
		w := e.Weight
		if w <= 0 {
			w = 1
		}
		soft := math.Pow(float64(w), weightPower)
		candidates = append(candidates, cand{e.URL, soft})
		total += soft
	}
	if total <= 0 || len(candidates) == 0 {
		return ""
	}
	r := rand.Float64() * total
	for _, c := range candidates {
		r -= c.soft
		if r <= 0 {
			return c.url
		}
	}
	return candidates[len(candidates)-1].url
}

// HasEnabled 是否至少一个启用的池条目
func HasEnabled() bool {
	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()
	for _, e := range poolEntries {
		if e.Enabled && e.URL != "" {
			return true
		}
	}
	return false
}

// CountEnabled 返回已启用且 URL 非空的池条目总数（用于熔断计数展示）。
func CountEnabled() int {
	poolMu.Lock()
	defer poolMu.Unlock()
	loadPoolLocked()
	n := 0
	for _, e := range poolEntries {
		if e.Enabled && e.URL != "" {
			n++
		}
	}
	return n
}
