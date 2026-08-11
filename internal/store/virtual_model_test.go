package store

import (
	"path/filepath"
	"testing"
)

// setupVirtualModelTestDB 创建临时数据库
func setupVirtualModelTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "virtualmodel.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// 测试虚拟模型 CRUD
func TestVirtualModelCRUD(t *testing.T) {
	db := setupVirtualModelTestDB(t)
	repo := NewVirtualModelRepo(db)

	// 创建
	vm := &VirtualModel{
		Name:        "text-vision",
		DisplayName: "文本+识图",
		MainModel:   "deepseek-chat",
		VisionModel: "gpt-4o-mini",
		Description: "测试",
		Status:      "active",
	}
	id, err := repo.Create(vm)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 按名查询
	got, err := repo.GetByName("text-vision")
	if err != nil {
		t.Fatalf("GetByName 失败: %v", err)
	}
	if got.ID != id || got.MainModel != "deepseek-chat" || got.VisionModel != "gpt-4o-mini" {
		t.Errorf("查询结果不匹配: %+v", got)
	}

	// 列表
	list, err := repo.List()
	if err != nil || len(list) != 1 {
		t.Errorf("列表应为 1 条，got %d (%v)", len(list), err)
	}

	// 更新
	vm.ID = id
	vm.VisionModel = "gpt-4o"
	vm.Description = "updated"
	if err := repo.Update(vm); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got, _ = repo.GetByName("text-vision")
	if got.VisionModel != "gpt-4o" || got.Description != "updated" {
		t.Errorf("更新后不匹配: %+v", got)
	}

	// 切换状态
	if err := repo.ToggleStatus(id); err != nil {
		t.Fatalf("Toggle 失败: %v", err)
	}
	got, _ = repo.GetByName("text-vision")
	if got.Status != "inactive" {
		t.Errorf("Toggle 后应为 inactive，got %s", got.Status)
	}

	// 删除
	if err := repo.Delete(id); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := repo.GetByName("text-vision"); err == nil {
		t.Error("删除后应查询不到")
	}
}

// 测试虚拟模型名校验唯一性
func TestVirtualModelUniqueName(t *testing.T) {
	db := setupVirtualModelTestDB(t)
	repo := NewVirtualModelRepo(db)

	vm1 := &VirtualModel{Name: "dup", MainModel: "a", Status: "active"}
	if _, err := repo.Create(vm1); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	vm2 := &VirtualModel{Name: "dup", MainModel: "b", Status: "active"}
	if _, err := repo.Create(vm2); err == nil {
		t.Error("重复虚拟模型名应报错")
	}
}
