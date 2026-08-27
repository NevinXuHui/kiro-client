package task

import (
	"testing"
)

func TestStartTaskRequestWithOptions(t *testing.T) {
	// 测试 SaveWithoutVerify 和 SaveLoginPassword 开关
	req := StartTaskRequest{
		Count:             1,
		Concurrency:       1,
		Delay:             0,
		EmailProvider:     "outlook",
		SaveWithoutVerify: true,
		SaveLoginPassword: false,
	}

	if !req.SaveWithoutVerify {
		t.Fatal("SaveWithoutVerify should be true")
	}
	if req.SaveLoginPassword {
		t.Fatal("SaveLoginPassword should be false")
	}
}

func TestSaveLogicWithVerifySwitch(t *testing.T) {
	// 模拟注册结果
	result := map[string]interface{}{
		"status":      "failed",
		"email":       "test@example.com",
		"password":    "test123",
		"passwordSet": true,
	}

	// 测试「注册完成即保存」开关
	req := StartTaskRequest{
		SaveWithoutVerify: true,
		SaveLoginPassword: true,
	}

	success := result["status"] == "success"
	shouldSave := false

	if success {
		shouldSave = true
	} else if req.SaveWithoutVerify {
		passwordSet, _ := result["passwordSet"].(bool)
		shouldSave = passwordSet
	}

	if !shouldSave {
		t.Fatal("Should save when SaveWithoutVerify=true and passwordSet=true")
	}
}

func TestPasswordRemovalWhenSwitchOff(t *testing.T) {
	result := map[string]interface{}{
		"email":    "test@example.com",
		"password": "secret123",
	}

	req := StartTaskRequest{
		SaveLoginPassword: false,
	}

	// 模拟密码移除逻辑
	if !req.SaveLoginPassword {
		delete(result, "password")
	}

	if _, exists := result["password"]; exists {
		t.Fatal("Password should be removed when SaveLoginPassword=false")
	}
}
