package store

import (
	"path/filepath"
	"testing"
)

// setupAPIKeyTestDB 创建临时数据库
func setupAPIKeyTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "apikey.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// 测试创建 API Key 默认不限制
func TestAPIKeyCreateDefault(t *testing.T) {
	db := setupAPIKeyTestDB(t)
	repo := NewAPIKeyRepo(db)

	k, err := repo.Create("test-key")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if k.QuotaEnabled {
		t.Error("默认不应启用额度限制")
	}
	if k.AllowedModels != "[]" && k.AllowedModels != "" {
		t.Errorf("默认 allowed_models 应为空，got %s", k.AllowedModels)
	}
	// 通过 key 查询
	got, err := repo.GetByKey(k.Key)
	if err != nil {
		t.Fatalf("GetByKey 失败: %v", err)
	}
	if got.ID != k.ID {
		t.Errorf("ID 不匹配: %d vs %d", got.ID, k.ID)
	}
}

// 测试额度扣减
func TestAPIKeyDeductQuota(t *testing.T) {
	db := setupAPIKeyTestDB(t)
	repo := NewAPIKeyRepo(db)

	k, err := repo.Create("quota-key")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 启用额度，初始 1.0
	updated, err := repo.UpdateConfig(k.ID, true, 1.0, "[]")
	if err != nil {
		t.Fatalf("UpdateConfig 失败: %v", err)
	}
	if !updated.QuotaEnabled || updated.QuotaBalance != 1.0 {
		t.Errorf("配置错误: enabled=%v balance=%v", updated.QuotaEnabled, updated.QuotaBalance)
	}

	// 扣减 0.3 → 余额 0.7
	bal, err := repo.DeductQuota(k.ID, 0.3)
	if err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	if bal < 0.699 || bal > 0.701 {
		t.Errorf("扣减后余额应为 0.7，got %v", bal)
	}

	// 再扣 0.8 → 超过余额，应为 0（不为负）
	bal, err = repo.DeductQuota(k.ID, 0.8)
	if err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	if bal != 0 {
		t.Errorf("超出额度扣减后应为 0，got %v", bal)
	}

	// 验证已用量累计 1.1（0.3 + 0.8）
	got, err := repo.GetByKey(k.Key)
	if err != nil {
		t.Fatalf("GetByKey 失败: %v", err)
	}
	if got.QuotaUsed < 1.09 || got.QuotaUsed > 1.11 {
		t.Errorf("累计已用应为 1.1，got %v", got.QuotaUsed)
	}
}

// 测试未启用额度时扣减：余额不变，但累计已用仍统计
func TestAPIKeyDeductQuotaDisabled(t *testing.T) {
	db := setupAPIKeyTestDB(t)
	repo := NewAPIKeyRepo(db)

	k, err := repo.Create("no-quota-key")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	// 未启用额度，扣减不应改变余额（且 should return 0 balance marker）
	bal, err := repo.DeductQuota(k.ID, 0.5)
	if err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	if bal != 0 {
		t.Errorf("未启用额度时余额应为 0，got %v", bal)
	}

	// 再扣 0.3，累计已用应累加为 0.8（即使未启用额度）
	if _, err := repo.DeductQuota(k.ID, 0.3); err != nil {
		t.Fatalf("再次扣减失败: %v", err)
	}
	got, err := repo.GetByKey(k.Key)
	if err != nil {
		t.Fatalf("GetByKey 失败: %v", err)
	}
	if got.QuotaUsed < 0.79 || got.QuotaUsed > 0.81 {
		t.Errorf("未启用额度时累计已用应统计为 0.8，got %v", got.QuotaUsed)
	}
	if got.QuotaBalance != 0 {
		t.Errorf("未启用额度时余额应保持 0，got %v", got.QuotaBalance)
	}
}

// 测试模型配置（allowed_models）
func TestAPIKeyUpdateAllowedModels(t *testing.T) {
	db := setupAPIKeyTestDB(t)
	repo := NewAPIKeyRepo(db)

	k, err := repo.Create("models-key")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	updated, err := repo.UpdateConfig(k.ID, false, 0, `["deepseek-chat","gpt-4o"]`)
	if err != nil {
		t.Fatalf("UpdateConfig 失败: %v", err)
	}
	if updated.AllowedModels != `["deepseek-chat","gpt-4o"]` {
		t.Errorf("allowed_models 不匹配: %s", updated.AllowedModels)
	}
}
