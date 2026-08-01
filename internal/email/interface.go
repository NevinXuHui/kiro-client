package email

import (
	"math/rand"
	"regexp"
	"strconv"
)

// GenerateEmailName 生成随机邮箱用户名（后缀任务序号防并发重复）
func GenerateEmailName(i int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for j := range b {
		b[j] = chars[rand.Intn(len(chars))]
	}
	return string(b) + strconv.Itoa(i)
}

// extractCodeFromText 从文本中提取验证码（正则第一个捕获组，无则返回空串）
func extractCodeFromText(text string, re *regexp.Regexp) string {
	if text == "" {
		return ""
	}
	m := re.FindStringSubmatch(text)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// TempEmailService 临时邮箱服务接口
type TempEmailService interface {
	// Create 创建临时邮箱，返回邮箱地址
	Create() string

	// WaitForCode 等待验证码，返回验证码字符串
	WaitForCode(timeoutSec, intervalSec int) (string, error)

	// GetAddress 获取当前邮箱地址
	GetAddress() string
}

// cloudMailAdapter 适配器，将 CloudMailProvider 包装为 TempEmailService
type cloudMailAdapter struct {
	provider *CloudMailProvider
}

// NewCloudMailService 用已创建的 CloudMailProvider 构造 TempEmailService
func NewCloudMailService(provider *CloudMailProvider) TempEmailService {
	return &cloudMailAdapter{provider: provider}
}

// Create 已在 NewCloudMailProvider 时创建好，直接返回地址
func (a *cloudMailAdapter) Create() string {
	if a.provider == nil {
		return ""
	}
	return a.provider.GetAddress()
}

// WaitForCode 等待验证码
func (a *cloudMailAdapter) WaitForCode(timeout, interval int) (string, error) {
	if a.provider == nil {
		return "", nil
	}
	return a.provider.WaitForCode(timeout, interval)
}

// GetAddress 获取邮箱地址
func (a *cloudMailAdapter) GetAddress() string {
	if a.provider == nil {
		return ""
	}
	return a.provider.GetAddress()
}
