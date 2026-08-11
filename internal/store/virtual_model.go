package store

import (
	"time"
)

// VirtualModel 虚拟模型（模型路由）
// 下游请求使用虚拟模型名，中转站根据路由规则转发到实际模型：
//   - 无图请求 → main_model（纯文本主模型）
//   - 有图请求 → 先调 vision_model 识图，图片替换为描述后调 main_model
type VirtualModel struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`         // 虚拟模型名（下游请求使用）
	DisplayName string    `json:"display_name"` // 展示名
	MainModel   string    `json:"main_model"`   // 主模型 ModelID（纯文本）
	VisionModel string    `json:"vision_model"` // 识图模型 ModelID（多模态），空=未启用识图扩展
	Description string    `json:"description"`
	Status      string    `json:"status"` // active, inactive
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// VirtualModelRepo 虚拟模型数据访问
type VirtualModelRepo struct {
	db *DB
}

func NewVirtualModelRepo(db *DB) *VirtualModelRepo {
	return &VirtualModelRepo{db: db}
}

func (r *VirtualModelRepo) List() ([]VirtualModel, error) {
	rows, err := r.db.Query(`SELECT id, name, display_name, main_model, vision_model, description, status, created_at, updated_at FROM virtual_models ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []VirtualModel
	for rows.Next() {
		var m VirtualModel
		if err := rows.Scan(&m.ID, &m.Name, &m.DisplayName, &m.MainModel, &m.VisionModel, &m.Description, &m.Status, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// GetByName 按虚拟模型名查询
func (r *VirtualModelRepo) GetByName(name string) (*VirtualModel, error) {
	var m VirtualModel
	err := r.db.QueryRow(
		`SELECT id, name, display_name, main_model, vision_model, description, status, created_at, updated_at FROM virtual_models WHERE name = ?`, name,
	).Scan(&m.ID, &m.Name, &m.DisplayName, &m.MainModel, &m.VisionModel, &m.Description, &m.Status, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *VirtualModelRepo) Create(m *VirtualModel) (int64, error) {
	result, err := r.db.Exec(
		`INSERT INTO virtual_models (name, display_name, main_model, vision_model, description, status) VALUES (?, ?, ?, ?, ?, ?)`,
		m.Name, m.DisplayName, m.MainModel, m.VisionModel, m.Description, m.Status,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *VirtualModelRepo) Update(m *VirtualModel) error {
	_, err := r.db.Exec(
		`UPDATE virtual_models SET name=?, display_name=?, main_model=?, vision_model=?, description=?, status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		m.Name, m.DisplayName, m.MainModel, m.VisionModel, m.Description, m.Status, m.ID,
	)
	return err
}

func (r *VirtualModelRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM virtual_models WHERE id = ?`, id)
	return err
}

// GetByID 按 ID 查询
func (r *VirtualModelRepo) GetByID(id int64) (*VirtualModel, error) {
	var m VirtualModel
	err := r.db.QueryRow(
		`SELECT id, name, display_name, main_model, vision_model, description, status, created_at, updated_at FROM virtual_models WHERE id = ?`, id,
	).Scan(&m.ID, &m.Name, &m.DisplayName, &m.MainModel, &m.VisionModel, &m.Description, &m.Status, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ToggleStatus 切换启用/禁用状态
func (r *VirtualModelRepo) ToggleStatus(id int64) error {
	_, err := r.db.Exec(`UPDATE virtual_models SET status = CASE WHEN status='active' THEN 'inactive' ELSE 'active' END, updated_at=CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}
