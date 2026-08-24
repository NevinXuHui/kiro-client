package main

import (
	"fmt"
	"strings"
)

// 测试 Outlook 账号格式是否正确

func main() {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔍 Outlook 账号格式诊断工具")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("")

	// 示例账号格式
	testCases := []struct {
		name   string
		input  string
		valid  bool
	}{
		{
			name:  "正确格式（4字段）",
			input: "test@outlook.com----password123----ncGq-IBFli-08ta0fuRcM3VzLWVhc3QtMQ----aorAAAAAGsC6aEcPRleBeej-mZweLFc",
			valid: true,
		},
		{
			name:  "正确格式（5字段，含 AccessToken）",
			input: "test@outlook.com----password123----ncGq-IBFli-08ta0fuRcM3VzLWVhc3QtMQ----aorAAAAAGsC6aEcPRleBeej-mZweLFc----eyJhbGc",
			valid: true,
		},
		{
			name:  "错误：分隔符错误（使用空格）",
			input: "test@outlook.com password123 cid rtoken",
			valid: false,
		},
		{
			name:  "错误：分隔符错误（使用2个短横线）",
			input: "test@outlook.com--password123--cid--rtoken",
			valid: false,
		},
		{
			name:  "错误：字段不足",
			input: "test@outlook.com----password123----cid",
			valid: false,
		},
		{
			name:  "错误：邮箱格式错误",
			input: "test----password123----cid----rtoken",
			valid: false,
		},
		{
			name:  "ClientID/RefreshToken 顺序互换（自动识别）",
			input: "test@outlook.com----password123----aorAAAAAGsC6aEcPRleBeej-mZweLFc----ncGq-IBFli-08ta0fuRcM3VzLWVhc3QtMQ",
			valid: true,
		},
	}

	fmt.Println("✅ 正确格式示例:")
	fmt.Println("   邮箱----密码----ClientID----RefreshToken")
	fmt.Println("   test@outlook.com----pass123----xxxnVzLWVhc3QtMQ----aorAAAAAGxxx")
	fmt.Println("")
	fmt.Println("📋 关键要求:")
	fmt.Println("   1. 分隔符必须是 ---- (4个短横线)")
	fmt.Println("   2. 必须至少4个字段")
	fmt.Println("   3. 邮箱必须包含 @")
	fmt.Println("   4. ClientID 通常以 nVzLWVhc3QtMQ 结尾")
	fmt.Println("   5. RefreshToken 通常以 aorAAAAAG 开头")
	fmt.Println("   6. ClientID 和 RefreshToken 顺序可互换（自动识别）")
	fmt.Println("")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🧪 测试案例:")
	fmt.Println("")

	for i, tc := range testCases {
		fmt.Printf("%d. %s\n", i+1, tc.name)
		result := validateOutlookFormat(tc.input)
		if result.valid == tc.valid {
			if result.valid {
				fmt.Printf("   ✅ 格式正确\n")
				fmt.Printf("   📧 邮箱: %s\n", result.email)
				fmt.Printf("   🔑 ClientID: %s...\n", truncate(result.clientID, 20))
				fmt.Printf("   🎫 RefreshToken: %s...\n", truncate(result.refreshToken, 20))
			} else {
				fmt.Printf("   ❌ 格式错误: %s\n", result.error)
			}
		} else {
			fmt.Printf("   ⚠️  测试失败（预期 %v，实际 %v）\n", tc.valid, result.valid)
		}
		fmt.Println("")
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("💡 如何使用:")
	fmt.Println("   1. 复制你的账号数据")
	fmt.Println("   2. 检查分隔符是否为 ---- (4个短横线)")
	fmt.Println("   3. 确认字段数量（至少4个）")
	fmt.Println("   4. 检查 ClientID 和 RefreshToken 格式")
	fmt.Println("")
	fmt.Println("❓ 仍然失败？")
	fmt.Println("   - 打开浏览器开发者工具 (F12)")
	fmt.Println("   - 查看 Console 控制台的错误信息")
	fmt.Println("   - 复制完整的错误信息")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}

type validateResult struct {
	valid        bool
	email        string
	password     string
	clientID     string
	refreshToken string
	error        string
}

func validateOutlookFormat(line string) validateResult {
	line = strings.TrimSpace(line)
	if line == "" {
		return validateResult{valid: false, error: "输入为空"}
	}

	parts := strings.Split(line, "----")
	if len(parts) < 4 {
		return validateResult{
			valid: false,
			error: fmt.Sprintf("字段不足（需要4个，实际%d个）", len(parts)),
		}
	}

	email := strings.TrimSpace(parts[0])
	password := strings.TrimSpace(parts[1])
	field3 := strings.TrimSpace(parts[2])
	field4 := strings.TrimSpace(parts[3])

	if email == "" {
		return validateResult{valid: false, error: "邮箱为空"}
	}
	if !strings.Contains(email, "@") {
		return validateResult{valid: false, error: "邮箱格式错误（缺少@）"}
	}
	if password == "" {
		return validateResult{valid: false, error: "密码为空"}
	}

	clientID, refreshToken := classifyClientAndRefresh(field3, field4)

	if clientID == "" {
		return validateResult{valid: false, error: "ClientID 为空或格式错误"}
	}
	if refreshToken == "" {
		return validateResult{valid: false, error: "RefreshToken 为空或格式错误"}
	}

	return validateResult{
		valid:        true,
		email:        email,
		password:     password,
		clientID:     clientID,
		refreshToken: refreshToken,
	}
}

// classifyClientAndRefresh 自动识别 ClientID 和 RefreshToken
func classifyClientAndRefresh(a, b string) (clientID, refresh string) {
	if strings.HasPrefix(a, "aorAAAAAG") {
		return b, a
	}
	if strings.HasPrefix(b, "aorAAAAAG") {
		return a, b
	}
	if strings.HasSuffix(a, "nVzLWVhc3QtMQ") || strings.Contains(a, "nVzLWVhc3QtMQ") {
		return a, b
	}
	if strings.HasSuffix(b, "nVzLWVhc3QtMQ") || strings.Contains(b, "nVzLWVhc3QtMQ") {
		return b, a
	}
	return a, b
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
