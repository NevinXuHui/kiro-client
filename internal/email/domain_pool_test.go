package email

import (
	"os"
	"path/filepath"
	"testing"

	"reg_go/internal/storage"
)

// TestDomainPool 验证域名池状态持久化 + 聚合（临时目录）
func TestDomainPool(t *testing.T) {
	tmp := t.TempDir()

	// SetDataDirPath 会写全局 storage.conf（data_dir 指向临时目录），
	// 测试结束前必须恢复，否则污染真实配置
	confPath := filepath.Join(storage.GetDefaultDataDir(), "storage.conf")
	backup, _ := os.ReadFile(confPath)
	defer func() {
		if backup != nil {
			_ = os.WriteFile(confPath, backup, 0600)
		}
	}()

	storage.SetDataDirPath(tmp)
	InitDomainPool(tmp)

	// 准备 Cloud-Mail 配置（两个服务器提供域名）
	cfgJSON := `[{"name":"s1","url":"http://s1","email":"a@b.com","password":"p","domains":["aa.com","bb.com"]},` +
		`{"name":"s2","url":"http://s2","email":"c@d.com","password":"p","domains":["bb.com","cc.com"]}]`
	if res := SaveCloudMailConfigs(cfgJSON); res["error"] != nil {
		t.Fatalf("save configs: %v", res["error"])
	}

	// 聚合：aa.com×1、bb.com×2、cc.com×1
	entries := ListDomainPool()
	byDomain := map[string]DomainPoolEntry{}
	for _, e := range entries {
		byDomain[e.Domain] = e
	}
	if len(byDomain) != 3 {
		t.Fatalf("want 3 domains, got %d: %+v", len(byDomain), entries)
	}
	if byDomain["bb.com"].ConfigCount != 2 {
		t.Errorf("bb.com configCount = %d, want 2", byDomain["bb.com"].ConfigCount)
	}
	if !byDomain["aa.com"].Enabled || byDomain["aa.com"].Banned {
		t.Errorf("aa.com should be default enabled/unbanned")
	}

	// 拉黑 + 禁用
	MarkDomainBanned("aa.com")
	SetDomainEnabled("cc.com", false)

	// 持久化验证：重新初始化后状态保留
	InitDomainPool(tmp)
	banned := LoadBannedDomains()
	if len(banned) != 1 || banned[0] != "aa.com" {
		t.Errorf("banned = %v, want [aa.com]", banned)
	}
	disabled := LoadDisabledDomains()
	if len(disabled) != 2 { // cc.com 手动禁用 + aa.com 拉黑时禁用
		t.Errorf("disabled = %v, want 2 entries", disabled)
	}

	// 解除拉黑：恢复启用
	UnbanDomain("aa.com")
	InitDomainPool(tmp)
	if len(LoadBannedDomains()) != 0 {
		t.Errorf("banned should be empty after unban")
	}
}
