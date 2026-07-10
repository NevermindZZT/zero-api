package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type SkillCombinationHandler struct {
	skillRepo           *store.SkillRepo
	skillCombinationRepo *store.SkillCombinationRepo
	skillFS             *store.SkillFS
}

func NewSkillCombinationHandler(skillRepo *store.SkillRepo, comboRepo *store.SkillCombinationRepo, skillFS *store.SkillFS) *SkillCombinationHandler {
	return &SkillCombinationHandler{
		skillRepo:           skillRepo,
		skillCombinationRepo: comboRepo,
		skillFS:             skillFS,
	}
}

// ListCombinations 获取组合列表
// GET /api/skill-combinations
func (h *SkillCombinationHandler) ListCombinations(c *gin.Context) {
	combos, err := h.skillCombinationRepo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if combos == nil {
		combos = []store.SkillCombination{}
	}
	c.JSON(http.StatusOK, combos)
}

// GetCombination 获取组合详情
// GET /api/skill-combinations/:id
func (h *SkillCombinationHandler) GetCombination(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	combo, err := h.skillCombinationRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "组合不存在"})
		return
	}

	skills, err := h.skillCombinationRepo.GetSkills(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"combination": combo,
		"skills":      skills,
	})
}

// CreateCombination 创建组合
// POST /api/skill-combinations
func (h *SkillCombinationHandler) CreateCombination(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "组合名称不能为空"})
		return
	}

	id, err := h.skillCombinationRepo.Create(req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name, "description": req.Description})
}

// UpdateCombination 更新组合
// PUT /api/skill-combinations/:id
func (h *SkillCombinationHandler) UpdateCombination(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "组合名称不能为空"})
		return
	}

	if err := h.skillCombinationRepo.Update(id, req.Name, req.Description); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
}

// DeleteCombination 删除组合
// DELETE /api/skill-combinations/:id
func (h *SkillCombinationHandler) DeleteCombination(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	if err := h.skillCombinationRepo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// AddSkillToCombination 添加技能到组合
// POST /api/skill-combinations/:id/skills
func (h *SkillCombinationHandler) AddSkillToCombination(c *gin.Context) {
	combinationID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的组合 ID"})
		return
	}

	var req struct {
		SkillID int64 `json:"skill_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SkillID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 skill_id"})
		return
	}

	if err := h.skillCombinationRepo.AddSkill(combinationID, req.SkillID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "添加成功"})
}

// RemoveSkillFromCombination 从组合移除技能
// DELETE /api/skill-combinations/:id/skills/:skillId
func (h *SkillCombinationHandler) RemoveSkillFromCombination(c *gin.Context) {
	combinationID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的组合 ID"})
		return
	}

	skillID, err := strconv.ParseInt(c.Param("skillId"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的技能 ID"})
		return
	}

	if err := h.skillCombinationRepo.RemoveSkill(combinationID, skillID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "移除成功"})
}

// GetCombinationSkills 获取组合下所有技能（含文件内容）
// GET /api/skill-combinations/:id/skills
func (h *SkillCombinationHandler) GetCombinationSkills(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	skills, err := h.skillCombinationRepo.GetSkills(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 为每个技能加载文件内容
	type skillWithFiles struct {
		store.Skill
		Files []store.FileEntry `json:"files"`
	}

	result := make([]skillWithFiles, 0, len(skills))
	for _, s := range skills {
		files, _ := h.skillFS.ReadAllFiles(s.ID, s.Name)
		result = append(result, skillWithFiles{Skill: s, Files: files})
	}

	c.JSON(http.StatusOK, result)
}
