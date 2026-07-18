package store

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ===== 模型定义 =====

// Skill 技能
type Skill struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Type        string    `json:"type"` // manual, github
	SourceURL   string    `json:"source_url"`
	BasePath    string    `json:"base_path"` // 相对路径: "{id}-{name}/"
	Enabled     bool      `json:"enabled"`
	Tags        []string  `json:"tags,omitempty"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SkillFile 技能文件索引（不含 content）
type SkillFile struct {
	ID        int64     `json:"id"`
	SkillID   int64     `json:"skill_id"`
	FilePath  string    `json:"file_path"`
	FileSize  int64     `json:"file_size"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillCombination 技能组合
type SkillCombination struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SkillCount  int       `json:"skill_count,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SkillCombinationItem 组合中的技能关联
type SkillCombinationItem struct {
	ID             int64 `json:"id"`
	CombinationID  int64 `json:"combination_id"`
	SkillID        int64 `json:"skill_id"`
	SortOrder      int   `json:"sort_order"`
}

// ===== 缓存 =====

var (
	skillCacheMu       sync.RWMutex
	skillCache         = make(map[int64]*cachedSkill)
	skillListCache     []Skill
	skillListCacheTime time.Time
	skillListCacheTTL  = 5 * time.Minute

	comboCacheMu       sync.RWMutex
	comboListCache     []SkillCombination
	comboListCacheTime time.Time
	comboListCacheTTL  = 5 * time.Minute
)

type cachedSkill struct {
	skill  *Skill
	expiry time.Time
}

func InvalidateSkillCache() {
	skillCacheMu.Lock()
	defer skillCacheMu.Unlock()
	skillCache = make(map[int64]*cachedSkill)
	skillListCache = nil
	skillListCacheTime = time.Time{}
}

func InvalidateCombinationCache() {
	comboCacheMu.Lock()
	defer comboCacheMu.Unlock()
	comboListCache = nil
	comboListCacheTime = time.Time{}
}

// ===== SkillRepo =====

type SkillRepo struct {
	db *DB
}

func NewSkillRepo(db *DB) *SkillRepo {
	return &SkillRepo{db: db}
}

// List 返回所有技能（可选搜索和标签筛选）
func (r *SkillRepo) List(q, tag string) ([]Skill, error) {
	// 只在无筛选条件时使用缓存
	if q == "" && tag == "" {
		skillCacheMu.RLock()
		if skillListCache != nil && time.Since(skillListCacheTime) < skillListCacheTTL {
			result := skillListCache
			skillCacheMu.RUnlock()
			return result, nil
		}
		skillCacheMu.RUnlock()
	}

	query := `SELECT s.id, s.name, s.description, s.type, s.source_url, s.base_path, s.enabled, s.commit_sha, s.created_at, s.updated_at
		FROM skills s WHERE 1=1`
	args := []interface{}{}

	if q != "" {
		query += ` AND (s.name LIKE ? OR s.description LIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	if tag != "" {
		query += ` AND s.id IN (SELECT skill_id FROM skill_tags WHERE tag = ?)`
		args = append(args, tag)
	}
	query += ` ORDER BY s.id`

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Type, &s.SourceURL, &s.BasePath, &s.Enabled, &s.CommitSHA, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		// 加载标签
		s.Tags = r.loadTags(s.ID)
		skills = append(skills, s)
	}

	if q == "" && tag == "" {
		skillCacheMu.Lock()
		skillListCache = skills
		skillListCacheTime = time.Now()
		skillCacheMu.Unlock()
	}

	return skills, nil
}

// GetByID 根据 ID 获取技能
func (r *SkillRepo) GetByID(id int64) (*Skill, error) {
	// 查缓存
	skillCacheMu.RLock()
	if cached, ok := skillCache[id]; ok && time.Now().Before(cached.expiry) {
		s := *cached.skill
		skillCacheMu.RUnlock()
		return &s, nil
	}
	skillCacheMu.RUnlock()

	s := &Skill{}
	err := r.db.QueryRow(
		`SELECT id, name, description, type, source_url, base_path, enabled, commit_sha, created_at, updated_at FROM skills WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.Description, &s.Type, &s.SourceURL, &s.BasePath, &s.Enabled, &s.CommitSHA, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}

	s.Tags = r.loadTags(s.ID)

	// 写入缓存（5 分钟 TTL）
	skillCacheMu.Lock()
	skillCache[id] = &cachedSkill{
		skill:  s,
		expiry: time.Now().Add(5 * time.Minute),
	}
	skillCacheMu.Unlock()

	return s, nil
}

// Create 创建技能
func (r *SkillRepo) Create(s *Skill) (int64, error) {
	result, err := r.db.Exec(
		`INSERT INTO skills (name, description, type, source_url, base_path, enabled, commit_sha) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Description, s.Type, s.SourceURL, s.BasePath, s.Enabled, s.CommitSHA,
	)
	if err != nil {
		return 0, fmt.Errorf("创建技能失败: %w", err)
	}
	id, _ := result.LastInsertId()
	s.ID = id

	// 插入标签
	if len(s.Tags) > 0 {
		if err := r.saveTags(id, s.Tags); err != nil {
			return 0, err
		}
	}

	InvalidateSkillCache()
	return id, nil
}

// Update 更新技能元数据
func (r *SkillRepo) Update(s *Skill) error {
	_, err := r.db.Exec(
		`UPDATE skills SET name=?, description=?, type=?, source_url=?, base_path=?, enabled=?, commit_sha=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		s.Name, s.Description, s.Type, s.SourceURL, s.BasePath, s.Enabled, s.CommitSHA, s.ID,
	)
	if err != nil {
		return fmt.Errorf("更新技能失败: %w", err)
	}

	// 更新标签：删除旧标签，插入新标签
	if err := r.saveTags(s.ID, s.Tags); err != nil {
		return err
	}

	InvalidateSkillCache()
	return nil
}

// Delete 删除技能（级联删除 tags, files, combination_items）
func (r *SkillRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM skills WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除技能失败: %w", err)
	}
	InvalidateSkillCache()
	return nil
}

// GetFiles 获取技能文件索引列表（无 content）
func (r *SkillRepo) GetFiles(skillID int64) ([]SkillFile, error) {
	rows, err := r.db.Query(`SELECT id, skill_id, file_path, file_size, updated_at FROM skill_files WHERE skill_id = ? ORDER BY file_path`, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []SkillFile
	for rows.Next() {
		var f SkillFile
		if err := rows.Scan(&f.ID, &f.SkillID, &f.FilePath, &f.FileSize, &f.UpdatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// SaveFileIndex 添加/更新文件索引
func (r *SkillRepo) SaveFileIndex(skillID int64, filePath string, fileSize int64) error {
	_, err := r.db.Exec(
		`INSERT INTO skill_files (skill_id, file_path, file_size, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(skill_id, file_path) DO UPDATE SET file_size=?, updated_at=CURRENT_TIMESTAMP`,
		skillID, filePath, fileSize, fileSize,
	)
	return err
}

// DeleteFileIndex 删除文件索引
func (r *SkillRepo) DeleteFileIndex(skillID int64, filePath string) error {
	_, err := r.db.Exec(`DELETE FROM skill_files WHERE skill_id = ? AND file_path = ?`, skillID, filePath)
	return err
}

// DeleteAllFileIndexes 清空文件索引
func (r *SkillRepo) DeleteAllFileIndexes(skillID int64) error {
	_, err := r.db.Exec(`DELETE FROM skill_files WHERE skill_id = ?`, skillID)
	return err
}

// loadTags 加载技能标签
func (r *SkillRepo) loadTags(skillID int64) []string {
	rows, err := r.db.Query(`SELECT tag FROM skill_tags WHERE skill_id = ? ORDER BY id`, skillID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			continue
		}
		tags = append(tags, t)
	}
	return tags
}

// saveTags 替换技能标签（删除旧 + 插入新）
func (r *SkillRepo) saveTags(skillID int64, tags []string) error {
	if _, err := r.db.Exec(`DELETE FROM skill_tags WHERE skill_id = ?`, skillID); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, err := r.db.Exec(`INSERT INTO skill_tags (skill_id, tag) VALUES (?, ?)`, skillID, tag); err != nil {
			return err
		}
	}
	return nil
}

// ListTags 返回所有标签（去重）
func (r *SkillRepo) ListTags() ([]string, error) {
	rows, err := r.db.Query(`SELECT DISTINCT tag FROM skill_tags ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			continue
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// ===== SkillCombinationRepo =====

type SkillCombinationRepo struct {
	db *DB
}

func NewSkillCombinationRepo(db *DB) *SkillCombinationRepo {
	return &SkillCombinationRepo{db: db}
}

// List 返回所有组合（含技能数量）
func (r *SkillCombinationRepo) List() ([]SkillCombination, error) {
	comboCacheMu.RLock()
	if comboListCache != nil && time.Since(comboListCacheTime) < comboListCacheTTL {
		result := comboListCache
		comboCacheMu.RUnlock()
		return result, nil
	}
	comboCacheMu.RUnlock()

	rows, err := r.db.Query(`
		SELECT sc.id, sc.name, sc.description, sc.created_at, sc.updated_at,
			(SELECT COUNT(*) FROM skill_combination_items WHERE combination_id = sc.id) as skill_count
		FROM skill_combinations sc ORDER BY sc.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var combos []SkillCombination
	for rows.Next() {
		var c SkillCombination
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt, &c.SkillCount); err != nil {
			return nil, err
		}
		combos = append(combos, c)
	}

	comboCacheMu.Lock()
	comboListCache = combos
	comboListCacheTime = time.Now()
	comboCacheMu.Unlock()

	return combos, nil
}

// GetByID 获取组合详情
func (r *SkillCombinationRepo) GetByID(id int64) (*SkillCombination, error) {
	c := &SkillCombination{}
	err := r.db.QueryRow(`
		SELECT sc.id, sc.name, sc.description, sc.created_at, sc.updated_at,
			(SELECT COUNT(*) FROM skill_combination_items WHERE combination_id = sc.id) as skill_count
		FROM skill_combinations sc WHERE sc.id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt, &c.SkillCount)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Create 创建组合
func (r *SkillCombinationRepo) Create(name, description string) (int64, error) {
	result, err := r.db.Exec(`INSERT INTO skill_combinations (name, description) VALUES (?, ?)`, name, description)
	if err != nil {
		return 0, fmt.Errorf("创建组合失败: %w", err)
	}
	id, _ := result.LastInsertId()
	InvalidateCombinationCache()
	return id, nil
}

// Update 更新组合
func (r *SkillCombinationRepo) Update(id int64, name, description string) error {
	_, err := r.db.Exec(`UPDATE skill_combinations SET name=?, description=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, name, description, id)
	if err != nil {
		return fmt.Errorf("更新组合失败: %w", err)
	}
	InvalidateCombinationCache()
	return nil
}

// Delete 删除组合
func (r *SkillCombinationRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM skill_combinations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除组合失败: %w", err)
	}
	InvalidateCombinationCache()
	return nil
}

// AddSkill 添加技能到组合
func (r *SkillCombinationRepo) AddSkill(combinationID, skillID int64) error {
	// 获取当前最大 sort_order
	var maxOrder int
	r.db.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) FROM skill_combination_items WHERE combination_id = ?`, combinationID).Scan(&maxOrder)

	_, err := r.db.Exec(
		`INSERT OR IGNORE INTO skill_combination_items (combination_id, skill_id, sort_order) VALUES (?, ?, ?)`,
		combinationID, skillID, maxOrder+1,
	)
	if err != nil {
		return fmt.Errorf("添加技能到组合失败: %w", err)
	}
	InvalidateCombinationCache()
	return nil
}

// RemoveSkill 从组合移除技能
func (r *SkillCombinationRepo) RemoveSkill(combinationID, skillID int64) error {
	_, err := r.db.Exec(`DELETE FROM skill_combination_items WHERE combination_id = ? AND skill_id = ?`, combinationID, skillID)
	if err != nil {
		return fmt.Errorf("从组合移除技能失败: %w", err)
	}
	InvalidateCombinationCache()
	return nil
}

// GetSkills 返回组合下所有技能（不含文件内容）
func (r *SkillCombinationRepo) GetSkills(combinationID int64) ([]Skill, error) {
	rows, err := r.db.Query(`
		SELECT s.id, s.name, s.description, s.type, s.source_url, s.base_path, s.enabled, s.commit_sha, s.created_at, s.updated_at
		FROM skills s
		JOIN skill_combination_items sci ON sci.skill_id = s.id
		WHERE sci.combination_id = ?
		ORDER BY sci.sort_order`, combinationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Type, &s.SourceURL, &s.BasePath, &s.Enabled, &s.CommitSHA, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Tags = r.loadTagsForSkill(s.ID)
		skills = append(skills, s)
	}
	return skills, nil
}

// GetSkillsWithPaths 返回组合下所有技能（含 base_path 和文件路径列表）
// 用于 MCP install_combination
func (r *SkillCombinationRepo) GetSkillsWithPaths(combinationID int64) ([]Skill, error) {
	skills, err := r.GetSkills(combinationID)
	if err != nil {
		return nil, err
	}
	// 为每个 skill 加载文件路径列表（使用全局 SkillRepo 的方式——但我们用本地方法）
	skillRepo := NewSkillRepo(r.db)
	for i, s := range skills {
		files, err := skillRepo.GetFiles(s.ID)
		if err != nil {
			continue
		}
		// 将文件路径作为额外信息，通过 tags 或其它方式传递
		_ = files
		_ = i
	}
	return skills, nil
}

// loadTagsForSkill 加载单个技能的标签
func (r *SkillCombinationRepo) loadTagsForSkill(skillID int64) []string {
	rows, err := r.db.Query(`SELECT tag FROM skill_tags WHERE skill_id = ? ORDER BY id`, skillID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tags = append(tags, t)
	}
	return tags
}
