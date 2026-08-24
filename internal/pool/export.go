package pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ExportAccount 号池导出 JSON（字段与外部卡密格式对齐）
type ExportAccount struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	CreditLimit  int    `json:"creditLimit"`
	CreditUsed   int    `json:"creditUsed"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	Provider     string `json:"provider"`
	ProxyIP      string `json:"proxy_ip"`
	ProxyRegion  string `json:"proxy_region"`
	RefreshToken string `json:"refreshToken"`
	Region       string `json:"region"`
	Subscription string `json:"subscription"`
}

// ToExport 转成导出结构（缺省补齐）
func (a *Account) ToExport() ExportAccount {
	out := ExportAccount{
		Provider: "BuilderId",
		Region:   "us-east-1",
	}
	if a == nil {
		return out
	}
	out.ClientID = a.ClientID
	out.ClientSecret = a.ClientSecret
	out.CreditLimit = a.CreditLimit
	out.CreditUsed = a.CreditUsed
	out.Email = a.Email
	out.Password = a.Password
	out.ProxyIP = a.ProxyIP
	out.ProxyRegion = a.ProxyRegion
	out.RefreshToken = a.RefreshToken
	out.Subscription = a.Subscription
	if a.Provider != "" {
		out.Provider = a.Provider
	}
	if a.Region != "" {
		out.Region = a.Region
	}
	return out
}

// SanitizeFilename 把邮箱等字符串收成可作文件名的片段（保留 @ . - _）
func SanitizeFilename(name string) string {
	if name == "" {
		return "account"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == '@':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "account"
	}
	return out
}

// ExportAccountsJSON 导出账号列表（多条为数组，一条为对象）
func ExportAccountsJSON(accounts []*Account) ([]byte, error) {
	if len(accounts) == 1 {
		return json.MarshalIndent(accounts[0].ToExport(), "", "  ")
	}
	out := make([]ExportAccount, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, a.ToExport())
	}
	return json.MarshalIndent(out, "", "  ")
}

// ParseAccountJSON 解析单对象或数组
func ParseAccountJSON(data []byte) ([]*Account, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty json")
	}
	if data[0] == '{' {
		var acc Account
		if err := json.Unmarshal(data, &acc); err != nil {
			return nil, err
		}
		return []*Account{&acc}, nil
	}
	var accounts []*Account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}
