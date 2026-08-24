package email

import (
	"os"
	"time"

	"reg_go/internal/storage"
)

// AddHttpAPIAccounts 添加 HTTP API 邮箱到持久化存储
func AddHttpAPIAccounts(data string) map[string]interface{} {
	accounts := ParseHttpAPILines(data)
	if len(accounts) == 0 {
		return map[string]interface{}{"error": "未解析到有效账号（格式：邮箱----API地址）"}
	}

	addedCount := 0
	now := time.Now().Format("2006-01-02 15:04:05")
	storage.ModifyHttpAPICached(func(existing []map[string]interface{}) []map[string]interface{} {
		for _, acc := range accounts {
			exists := false
			for _, e := range existing {
				if e["email"] == acc.Email {
					exists = true
					break
				}
			}
			if !exists {
				existing = append(existing, map[string]interface{}{
					"email":      acc.Email,
					"apiUrl":     acc.APIURL,
					"registered": false,
					"success":    false,
					"addedAt":    now,
				})
				addedCount++
			}
		}
		return existing
	})

	return map[string]interface{}{
		"added": addedCount,
		"total": len(storage.GetHttpAPICached()),
	}
}

// GetHttpAPIAccounts 获取 HTTP API 邮箱列表
func GetHttpAPIAccounts() []map[string]interface{} {
	return storage.GetHttpAPICached()
}

// UpdateHttpAPIAccountStatus 更新账号注册状态
func UpdateHttpAPIAccountStatus(emailAddr string, registered bool, success bool) map[string]interface{} {
	found := false
	now := time.Now().Format("2006-01-02 15:04:05")
	storage.ModifyHttpAPICached(func(accounts []map[string]interface{}) []map[string]interface{} {
		for i, acc := range accounts {
			if acc["email"] == emailAddr {
				accounts[i]["registered"] = registered
				accounts[i]["success"] = success
				accounts[i]["registeredAt"] = now
				found = true
				break
			}
		}
		return accounts
	})
	if !found {
		return map[string]interface{}{"error": "账号不存在"}
	}
	return map[string]interface{}{"status": "updated"}
}

// DeleteHttpAPIAccount 删除单个 HTTP API 邮箱
func DeleteHttpAPIAccount(emailAddr string) map[string]interface{} {
	found := false
	newLen := 0
	storage.ModifyHttpAPICached(func(accounts []map[string]interface{}) []map[string]interface{} {
		newAccounts := make([]map[string]interface{}, 0, len(accounts))
		for _, acc := range accounts {
			if acc["email"] == emailAddr {
				found = true
				continue
			}
			newAccounts = append(newAccounts, acc)
		}
		newLen = len(newAccounts)
		return newAccounts
	})
	if !found {
		return map[string]interface{}{"error": "账号不存在"}
	}
	return map[string]interface{}{
		"status": "deleted",
		"total":  newLen,
	}
}

// ClearHttpAPIAccounts 清空所有 HTTP API 邮箱
func ClearHttpAPIAccounts() map[string]interface{} {
	storage.SetHttpAPICached([]map[string]interface{}{})
	return map[string]interface{}{"status": "cleared"}
}

// ClearRegisteredHttpAPIAccounts 仅清除已标记为已注册的账号
func ClearRegisteredHttpAPIAccounts() map[string]interface{} {
	removed := 0
	newLen := 0
	storage.ModifyHttpAPICached(func(accounts []map[string]interface{}) []map[string]interface{} {
		out := make([]map[string]interface{}, 0, len(accounts))
		for _, acc := range accounts {
			if reg, _ := acc["registered"].(bool); reg {
				removed++
				continue
			}
			out = append(out, acc)
		}
		newLen = len(out)
		return out
	})
	return map[string]interface{}{"status": "ok", "removed": removed, "total": newLen}
}

// ImportHttpAPIFile 导入 HTTP API 邮箱文件
func ImportHttpAPIFile(filePath string) map[string]interface{} {
	if filePath == "" {
		return map[string]interface{}{"error": "未选择文件"}
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return map[string]interface{}{"error": "读取文件失败: " + err.Error()}
	}
	return AddHttpAPIAccounts(string(data))
}
