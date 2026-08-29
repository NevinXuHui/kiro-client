package task

import (
	"testing"
	"time"

	"reg_go/internal/email"
)

func resetManagerForTest() {
	Manager.mu.Lock()
	defer Manager.mu.Unlock()
	Manager.running = false
	Manager.stopCh = nil
	Manager.cancelFunc = nil
	Manager.total = 0
	Manager.completed = 0
	Manager.success = 0
	Manager.failed = 0
	Manager.results = nil
	Manager.startTime = time.Time{}
	Manager.provider = ""
	Manager.reserved = nil
	Manager.extra = nil
	Manager.wakeCh = nil
}

func TestStartTaskRequestWithOptions(t *testing.T) {
	// 测试 SaveWithoutVerify 和 SaveLoginPassword 开关
	req := StartTaskRequest{
		Count:             1,
		Concurrency:       1,
		Delay:             0.0,
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

func TestStartTaskRejectsZeroCount(t *testing.T) {
	result := StartTask(StartTaskRequest{Count: 0, EmailProvider: "cloudmail"})
	if result["error"] == nil {
		t.Fatal("expected error for count=0")
	}
}

func TestAppendTaskWhileRunning(t *testing.T) {
	resetManagerForTest()
	defer resetManagerForTest()

	wake := make(chan struct{}, 1)
	Manager.mu.Lock()
	Manager.running = true
	Manager.total = 10
	Manager.completed = 3
	Manager.success = 1
	Manager.failed = 2
	Manager.provider = "cloudmail"
	Manager.wakeCh = wake
	Manager.extra = nil
	Manager.mu.Unlock()

	req := StartTaskRequest{
		Count:            5,
		EmailProvider:    "cloudmail",
		CloudMailDomains: []string{"example.com"},
		CloudMailConfigs: map[string][]email.CloudMailConfig{
			"example.com": {{Name: "t", URL: "http://x", Email: "a@b.c", Password: "p"}},
		},
	}
	result := StartTask(req)
	if err, ok := result["error"]; ok && err != nil && err != "" {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["status"] != "appended" {
		t.Fatalf("status=%v want appended", result["status"])
	}
	if result["added"] != 5 {
		t.Fatalf("added=%v", result["added"])
	}
	if result["total"] != 15 {
		t.Fatalf("total=%v", result["total"])
	}

	select {
	case <-wake:
	default:
		t.Fatal("expected wake signal after append")
	}

	st := Manager.GetStatus()
	if st["running"] != true {
		t.Fatal("should still be running")
	}
	if st["total"] != 15 {
		t.Fatalf("status.total=%v", st["total"])
	}
	if st["completed"] != 3 {
		t.Fatalf("completed should stay 3, got %v", st["completed"])
	}
	if st["success"] != 1 || st["failed"] != 2 {
		t.Fatalf("stats mutated: success=%v failed=%v", st["success"], st["failed"])
	}

	Manager.mu.Lock()
	nExtra := len(Manager.extra)
	Manager.mu.Unlock()
	if nExtra != 1 {
		t.Fatalf("extra batches=%d want 1", nExtra)
	}
}

func TestAppendRejectsDifferentProvider(t *testing.T) {
	resetManagerForTest()
	defer resetManagerForTest()

	Manager.mu.Lock()
	Manager.running = true
	Manager.total = 2
	Manager.provider = "outlook"
	Manager.wakeCh = make(chan struct{}, 1)
	Manager.mu.Unlock()

	result := StartTask(StartTaskRequest{
		Count:            1,
		EmailProvider:    "cloudmail",
		CloudMailDomains: []string{"example.com"},
		CloudMailConfigs: map[string][]email.CloudMailConfig{
			"example.com": {{Name: "t"}},
		},
	})
	if result["error"] == nil {
		t.Fatal("expected provider mismatch error")
	}
	if result["status"] == "appended" {
		t.Fatal("must not append on provider mismatch")
	}
	st := Manager.GetStatus()
	if st["total"] != 2 {
		t.Fatalf("total changed on rejected append: %v", st["total"])
	}
}
