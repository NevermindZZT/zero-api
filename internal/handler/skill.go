package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

type SkillHandler struct {
	skillRepo           *store.SkillRepo
	skillCombinationRepo *store.SkillCombinationRepo
	proxyConfigRepo     *store.ProxyConfigRepo
	skillFS             *store.SkillFS
	ghClient            *upstream.GitHubClient
}

func NewSkillHandler(skillRepo *store.SkillRepo, skillCombinationRepo *store.SkillCombinationRepo, proxyConfigRepo *store.ProxyConfigRepo, skillFS *store.SkillFS) *SkillHandler {
	return &SkillHandler{
		skillRepo:           skillRepo,
		skillCombinationRepo: skillCombinationRepo,
		proxyConfigRepo:     proxyConfigRepo,
		skillFS:             skillFS,
		ghClient:            upstream.NewGitHubClient(),
	}
}

// ListSkills 获取技能列表
// GET /api/skills?q=&tag=
func (h *SkillHandler) ListSkills(c *gin.Context) {
	q := c.Query("q")
	tag := c.Query("tag")

	skills, err := h.skillRepo.List(q, tag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 为每个技能补充文件路径列表
	type skillWithFiles struct {
		store.Skill
		Files []store.FileEntry `json:"files"`
	}
	result := make([]skillWithFiles, 0, len(skills))
	for _, s := range skills {
		files, _ := h.skillFS.ListFiles(s.ID, s.Name)
		result = append(result, skillWithFiles{Skill: s, Files: files})
	}

	c.JSON(http.StatusOK, result)
}

// GetSkill 获取技能详情
// GET /api/skills/:id
func (h *SkillHandler) GetSkill(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	skill, err := h.skillRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "技能不存在"})
		return
	}

	files, _ := h.skillFS.ListFiles(skill.ID, skill.Name)

	type skillDetail struct {
		store.Skill
		Files []store.FileEntry `json:"files"`
	}

	c.JSON(http.StatusOK, skillDetail{Skill: *skill, Files: files})
}

// GetSkillFile 获取技能文件内容
// GET /api/skills/:id/files/*filePath
func (h *SkillHandler) GetSkillFile(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	filePath := strings.TrimPrefix(c.Param("filePath"), "/")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件路径不能为空"})
		return
	}

	skill, err := h.skillRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "技能不存在"})
		return
	}

	data, err := h.skillFS.ReadFile(skill.ID, skill.Name, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}

	// 推断 Content-Type
	ct := "text/plain"
	if strings.HasSuffix(filePath, ".md") {
		ct = "text/markdown"
	} else if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
		ct = "text/yaml"
	} else if strings.HasSuffix(filePath, ".json") {
		ct = "application/json"
	} else if strings.HasSuffix(filePath, ".py") || strings.HasSuffix(filePath, ".js") || strings.HasSuffix(filePath, ".ts") || strings.HasSuffix(filePath, ".go") {
		ct = "text/plain"
	}

	c.Data(http.StatusOK, ct, data)
}

type createSkillRequest struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Tags        []string            `json:"tags"`
	Files       []store.FileEntry   `json:"files"`
	Type        string              `json:"type"`
	SourceURL   string              `json:"source_url"`
}

// CreateSkill 创建技能
// POST /api/skills
func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var req createSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "技能名称不能为空"})
		return
	}

	if req.Type == "" {
		req.Type = "manual"
	}

	// 先创建 DB 记录，获取 ID
	skill := &store.Skill{
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		SourceURL:   req.SourceURL,
		Tags:        req.Tags,
		Enabled:     true,
	}

	id, err := h.skillRepo.Create(skill)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 创建磁盘目录
	basePath, err := h.skillFS.EnsureDir(id, req.Name)
	if err != nil {
		h.skillRepo.Delete(id)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能目录失败: " + err.Error()})
		return
	}

	// 更新 base_path
	skill.ID = id
	skill.BasePath = basePath
	h.skillRepo.Update(skill)

	// 写入文件
	for _, f := range req.Files {
		if f.Path == "" {
			continue
		}
		if err := h.skillFS.WriteFile(id, req.Name, f.Path, []byte(f.Content)); err != nil {
			log.Printf("[Skill] 写入文件失败 %s: %v", f.Path, err)
			continue
		}
		h.skillRepo.SaveFileIndex(id, f.Path, int64(len(f.Content)))
	}

	// 返回完整数据
	files, _ := h.skillFS.ListFiles(id, req.Name)
	c.JSON(http.StatusCreated, gin.H{
		"skill": skill,
		"files": files,
	})
}

type updateSkillRequest struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Tags        []string            `json:"tags"`
	Files       []store.FileEntry   `json:"files"`
	Type        string              `json:"type"`
	SourceURL   string              `json:"source_url"`
	Enabled     *bool               `json:"enabled"`
}

// UpdateSkill 更新技能
// PUT /api/skills/:id
func (h *SkillHandler) UpdateSkill(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	oldSkill, err := h.skillRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "技能不存在"})
		return
	}

	var req updateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	if req.Name == "" {
		req.Name = oldSkill.Name
	}
	if req.Type == "" {
		req.Type = oldSkill.Type
	}
	enabled := oldSkill.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	// 如果名称变了，需要移动目录
	oldName := oldSkill.Name
	newName := req.Name

	skill := &store.Skill{
		ID:          id,
		Name:        newName,
		Description: req.Description,
		Type:        req.Type,
		SourceURL:   req.SourceURL,
		Tags:        req.Tags,
		Enabled:     enabled,
	}

	if newName != oldName {
		// 删除旧目录，创建新目录
		h.skillFS.DeleteSkillDir(id, oldName)
	}

	basePath, err := h.skillFS.EnsureDir(id, newName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能目录失败: " + err.Error()})
		return
	}
	skill.BasePath = basePath

	if err := h.skillRepo.Update(skill); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 更新文件：清空旧索引，写入新文件
	if req.Files != nil {
		h.skillFS.DeleteSkillDir(id, newName)
		h.skillFS.EnsureDir(id, newName)
		h.skillRepo.DeleteAllFileIndexes(id)

		for _, f := range req.Files {
			if f.Path == "" {
				continue
			}
			h.skillFS.WriteFile(id, newName, f.Path, []byte(f.Content))
			h.skillRepo.SaveFileIndex(id, f.Path, int64(len(f.Content)))
		}
	}

	files, _ := h.skillFS.ListFiles(id, newName)
	c.JSON(http.StatusOK, gin.H{
		"skill": skill,
		"files": files,
	})
}

// DeleteSkill 删除技能
// DELETE /api/skills/:id
func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	skill, err := h.skillRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "技能不存在"})
		return
	}

	// 删除磁盘文件
	h.skillFS.DeleteSkillDir(skill.ID, skill.Name)

	// 删除 DB 记录
	if err := h.skillRepo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 刷新缓存
	store.InvalidateSkillCache()

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

type importGitHubRequest struct {
	SourceURL   string `json:"source_url"`
	GitHubToken string `json:"github_token,omitempty"`
}

// ImportFromGitHub 从 GitHub 导入技能
// POST /api/skills/import-github
func (h *SkillHandler) ImportFromGitHub(c *gin.Context) {
	var req importGitHubRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 source_url"})
		return
	}
	if req.SourceURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_url 不能为空"})
		return
	}

	// 检查是否已存在同名同源技能，存在则复用 ID（保留组合引用）
	var existingID int64
	var existingName string
	var isUpdate bool
	if all, _ := h.skillRepo.List("", ""); all != nil {
		for _, s := range all {
			if s.SourceURL == req.SourceURL {
				existingID = s.ID
				existingName = s.Name
				isUpdate = true
				break
			}
		}
	}

	log.Printf("[Skill] 从 GitHub 导入: %s", req.SourceURL)

	// 使用提供的 Token 或 DB 中的 GitHub Token
	token := req.GitHubToken
	if token == "" {
		if pc, err := h.proxyConfigRepo.Get(); err == nil && pc.GitHubToken != "" {
			token = pc.GitHubToken
		}
	}
	client := h.ghClient
	if token != "" {
		client = upstream.NewGitHubClient()
		client.SetToken(token)
	}

	// 从 GitHub 获取文件
	entries, err := client.ImportFromGitHub(req.SourceURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("从 GitHub 导入失败: %v", err)})
		return
	}

	// 从 entries 中找 SKILL.md 解析名称和描述
	skillName := ""
	skillDesc := ""
	hasSKILLMD := false
	for _, e := range entries {
		if e.Path == store.SKILL_MD_FILE || strings.HasSuffix(e.Path, "/"+store.SKILL_MD_FILE) {
			hasSKILLMD = true
			info := store.ParseSKILLMD(string(e.Content))
			if info.Name != "" {
				skillName = info.Name
			}
			if info.Description != "" {
				skillDesc = info.Description
			}
			break
		}
	}
	if skillName == "" {
		skillName = client.SkillNameFromURL(req.SourceURL)
	}
	if !hasSKILLMD {
		log.Printf("[Skill] 警告: 导入的仓库中没有 SKILL.md 文件，将使用目录名作为技能名称")
	}

	// 准备技能元数据
	// 尝试获取最新 commit SHA 用于版本追踪
	commitSHA := ""
	if repoInfo, err := client.ParseGitHubURL(req.SourceURL); err == nil {
		if sha, err := client.GetLatestCommitSHA(repoInfo.Owner, repoInfo.Repo, repoInfo.Branch); err == nil {
			commitSHA = sha
		}
	}

	skill := &store.Skill{
		Name:        skillName,
		Description: skillDesc,
		Type:        "github",
		SourceURL:   req.SourceURL,
		Tags:        []string{},
		Enabled:     true,
		CommitSHA:   commitSHA,
	}

	var id int64
	if isUpdate {
		id = existingID
		skill.ID = id
		// 清除旧磁盘文件和文件索引
		h.skillFS.DeleteSkillDir(id, existingName)
		h.skillRepo.DeleteAllFileIndexes(id)
	} else {
		newID, err := h.skillRepo.Create(skill)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能记录失败: " + err.Error()})
			return
		}
		id = newID
	}

	// 创建目录并写入文件
	basePath, err := h.skillFS.EnsureDir(id, skillName)
	if err != nil {
		if !isUpdate {
			h.skillRepo.Delete(id)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能目录失败: " + err.Error()})
		return
	}

	skill.ID = id
	skill.BasePath = basePath
	h.skillRepo.Update(skill)

	// 逐个写入文件并建索引
	for _, entry := range entries {
		if err := h.skillFS.WriteFile(id, skillName, entry.Path, entry.Content); err != nil {
			log.Printf("[Skill] 写入文件失败 %s: %v", entry.Path, err)
			continue
		}
		h.skillRepo.SaveFileIndex(id, entry.Path, int64(len(entry.Content)))
	}

	log.Printf("[Skill] 从 GitHub 导入成功: id=%d, name=%s, files=%d", id, skillName, len(entries))

	files, _ := h.skillFS.ListFiles(id, skillName)
	c.JSON(http.StatusCreated, gin.H{
		"skill":   skill,
		"files":   files,
		"message": fmt.Sprintf("成功导入 %d 个文件", len(entries)),
	})
}

// UploadSkill 通过 zip 上传技能文件夹
// POST /api/skills/upload
// Content-Type: multipart/form-data
// 字段: file=@skill.zip
func (h *SkillHandler) UploadSkill(c *gin.Context) {
	// 获取上传的 zip 文件
	zipFile, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传 zip 文件"})
		return
	}
	defer zipFile.Close()

	// 限制最大 50MB
	zipData, err := io.ReadAll(io.LimitReader(zipFile, 50<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取文件失败: " + err.Error()})
		return
	}

	log.Printf("[Skill] 收到 zip 上传: %s (%d bytes)", header.Filename, len(zipData))

	// 先解压到内存，解析 SKILL.md 获取元信息
	tempReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 zip 文件"})
		return
	}

	// 搜索 SKILL.md
	skillName := ""
	skillDesc := ""
	for _, f := range tempReader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		cleanPath := filepath.ToSlash(f.Name)
		if strings.HasSuffix(cleanPath, "/"+store.SKILL_MD_FILE) || cleanPath == store.SKILL_MD_FILE {
			rc, _ := f.Open()
			content, _ := io.ReadAll(rc)
			rc.Close()
			info := store.ParseSKILLMD(string(content))
			skillName = info.Name
			skillDesc = info.Description
			break
		}
	}

	if skillName == "" {
		// 从文件名推断技能名（去掉 .zip 扩展名）
		skillName = strings.TrimSuffix(header.Filename, ".zip")
		skillName = strings.TrimSuffix(skillName, ".tar")
		if skillName == "" {
			skillName = "uploaded-skill"
		}
	}

	// 查找同名已有技能，存在则复用 ID（保留组合引用）
	var existingID int64
	var isUpdate bool
	if all, _ := h.skillRepo.List("", ""); all != nil {
		for _, s := range all {
			if s.Name == skillName {
				existingID = s.ID
				isUpdate = true
				// 清除旧磁盘文件和文件索引（保留 DB 记录）
				h.skillFS.DeleteSkillDir(s.ID, s.Name)
				h.skillRepo.DeleteAllFileIndexes(s.ID)
				break
			}
		}
	}

	// 准备技能元数据
	skill := &store.Skill{
		Name:        skillName,
		Description: skillDesc,
		Type:        "manual",
		Tags:        []string{},
		Enabled:     true,
	}

	var id int64
	if isUpdate {
		id = existingID
		skill.ID = id
	} else {
		newID, err := h.skillRepo.Create(skill)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能记录失败: " + err.Error()})
			return
		}
		id = newID
	}

	// 用 ExtractZip 解压到磁盘
	entries, err := h.skillFS.ExtractZip(id, skillName, zipData)
	if err != nil {
		if !isUpdate {
			h.skillRepo.Delete(id)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解压文件失败: " + err.Error()})
		return
	}

	// 更新 base_path 和元数据
	basePath, _ := h.skillFS.EnsureDir(id, skillName)
	skill.ID = id
	skill.BasePath = basePath
	h.skillRepo.Update(skill)

	// 建立文件索引
	for _, entry := range entries {
		h.skillRepo.SaveFileIndex(id, entry.Path, entry.Size)
	}

	log.Printf("[Skill] zip 上传成功: id=%d, name=%s, files=%d", id, skillName, len(entries))

	files, _ := h.skillFS.ListFiles(id, skillName)
	c.JSON(http.StatusCreated, gin.H{
		"skill":   skill,
		"files":   files,
		"message": fmt.Sprintf("成功上传 %d 个文件", len(entries)),
	})
}

// UploadFolder 直接上传技能文件夹（不打包 zip）
// POST /api/skills/upload-folder
// Content-Type: multipart/form-data
// 字段: files[] = (多个文件), paths = JSON.stringify([{name, path, webkitRelativePath}])
func (h *SkillHandler) UploadFolder(c *gin.Context) {
	// 解析 multipart form（限制 50MB）
	if err := c.Request.ParseMultipartForm(50 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "解析上传数据失败: " + err.Error()})
		return
	}

	// 读取文件路径映射
	pathsJSON := c.PostForm("paths")
	if pathsJSON == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 paths 参数"})
		return
	}

	type filePathInfo struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	var paths []filePathInfo
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "paths 格式无效"})
		return
	}

	// 读取所有上传文件
	form := c.Request.MultipartForm
	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择要上传的文件"})
		return
	}

	// 确认 paths 和 files 数量一致
	if len(paths) != len(files) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件数量不匹配"})
		return
	}

	// 搜索 SKILL.md 获取元信息
	skillName := ""
	skillDesc := ""
	for i, f := range files {
		if paths[i].Path == store.SKILL_MD_FILE || strings.HasSuffix(paths[i].Path, "/"+store.SKILL_MD_FILE) {
			file, _ := f.Open()
			content, _ := io.ReadAll(file)
			file.Close()
			info := store.ParseSKILLMD(string(content))
			skillName = info.Name
			skillDesc = info.Description
			break
		}
	}

	// 从文件夹名推断技能名称
	if skillName == "" && len(paths) > 0 {
		// 取第一个文件路径的根目录名
		first := strings.SplitN(paths[0].Path, "/", 2)
		if len(first) > 1 {
			skillName = first[0]
		}
	}
	if skillName == "" {
		skillName = "uploaded-skill"
	}

	log.Printf("[Skill] 收到文件夹上传: %d 个文件, 名称=%s", len(files), skillName)

	// 查找同名已有技能，存在则复用 ID（保留组合引用）
	var existingID int64
	var isUpdate bool
	if all, _ := h.skillRepo.List("", ""); all != nil {
		for _, s := range all {
			if s.Name == skillName {
				existingID = s.ID
				isUpdate = true
				// 清除旧磁盘文件和文件索引（保留 DB 记录）
				h.skillFS.DeleteSkillDir(s.ID, s.Name)
				h.skillRepo.DeleteAllFileIndexes(s.ID)
				break
			}
		}
	}

	// 准备技能元数据
	skill := &store.Skill{
		Name:        skillName,
		Description: skillDesc,
		Type:        "manual",
		Tags:        []string{},
		Enabled:     true,
	}

	var id int64
	if isUpdate {
		id = existingID
		skill.ID = id
	} else {
		newID, err := h.skillRepo.Create(skill)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能记录失败: " + err.Error()})
			return
		}
		id = newID
	}

	// 确保目录存在
	basePath, err := h.skillFS.EnsureDir(id, skillName)
	if err != nil {
		if !isUpdate {
			h.skillRepo.Delete(id)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能目录失败: " + err.Error()})
		return
	}
	skill.ID = id
	skill.BasePath = basePath
	h.skillRepo.Update(skill)

	// 逐个写入文件
	writtenCount := 0
	for i, f := range files {
		relPath := paths[i].Path
		if relPath == "" {
			continue
		}

		file, err := f.Open()
		if err != nil {
			log.Printf("[Skill] 打开文件失败 %s: %v", relPath, err)
			continue
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			log.Printf("[Skill] 读取文件失败 %s: %v", relPath, err)
			continue
		}

		if err := h.skillFS.WriteFile(id, skillName, relPath, content); err != nil {
			log.Printf("[Skill] 写入文件失败 %s: %v", relPath, err)
			continue
		}
		h.skillRepo.SaveFileIndex(id, relPath, int64(len(content)))
		writtenCount++
	}

	log.Printf("[Skill] 文件夹上传成功: id=%d, name=%s, files=%d", id, skillName, writtenCount)

	diskFiles, _ := h.skillFS.ListFiles(id, skillName)
	c.JSON(http.StatusCreated, gin.H{
		"skill":   skill,
		"files":   diskFiles,
		"message": fmt.Sprintf("成功上传 %d 个文件", writtenCount),
	})
}

// ListSkillTags 获取所有标签
// GET /api/skills/tags
func (h *SkillHandler) ListSkillTags(c *gin.Context) {
	tags, err := h.skillRepo.ListTags()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tags == nil {
		tags = []string{}
	}
	c.JSON(http.StatusOK, tags)
}

type importRepoRequest struct {
	RepoURL     string `json:"repo_url"`
	Path        string `json:"path"`
	GitHubToken string `json:"github_token,omitempty"`
}

// ImportRepo 从 GitHub 仓库导入所有 skill
// POST /api/skills/import-repo
func (h *SkillHandler) ImportRepo(c *gin.Context) {
	var req importRepoRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RepoURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 repo_url"})
		return
	}

	info, err := h.ghClient.ParseGitHubURL(req.RepoURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的仓库 URL: " + err.Error()})
		return
	}

	// 优先使用请求中的 Token，否则从 DB 配置读取全局 GitHub Token
	token := req.GitHubToken
	if token == "" {
		if pc, err := h.proxyConfigRepo.Get(); err == nil && pc.GitHubToken != "" {
			token = pc.GitHubToken
		}
	}
	client := h.ghClient
	if token != "" {
		client = upstream.NewGitHubClient()
		client.SetToken(token)
	}

	branch := info.Branch
	dirPath := info.Path
	if req.Path != "" {
		dirPath = req.Path
	}

	log.Printf("[Skill] 从仓库扫描技能: %s/%s, 路径=%s", info.Owner, info.Repo, dirPath)

	discovered, err := client.DiscoverSkills(info.Owner, info.Repo, branch, dirPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "扫描仓库失败: " + err.Error()})
		return
	}

	if len(discovered) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "在指定路径下未找到包含 SKILL.md 的技能目录"})
		return
	}

	repoTag := "repo:" + info.Owner + "/" + info.Repo
	repoURL := client.BuildRepoURL(info.Owner, info.Repo)

	// 收集已有同源技能（按 source_url 索引），复用 ID 以保留组合引用
	existingBySource := make(map[string]store.Skill)
	if existingSkills, _ := h.skillRepo.List("", repoTag); existingSkills != nil {
		for _, s := range existingSkills {
			if s.SourceURL != "" {
				existingBySource[s.SourceURL] = s
			}
		}
	}

	// 检查是否已存在同名组合，有则复用 comboID
	comboID := int64(0)
	comboName := info.Repo
	if existingCombos, _ := h.skillCombinationRepo.List(); existingCombos != nil {
		for _, c := range existingCombos {
			if c.Name == comboName {
				comboID = c.ID
				break
			}
		}
	}

	// 多技能时若还没有组合则创建
	if len(discovered) > 1 && comboID == 0 {
		comboDesc := fmt.Sprintf("从 %s 导入的技能组合", repoURL)
		comboID, err = h.skillCombinationRepo.Create(comboName, comboDesc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能组合失败: " + err.Error()})
			return
		}
	}

	// 获取最新 commit SHA 用于版本追踪
	commitSHA, _ := client.GetLatestCommitSHA(info.Owner, info.Repo, branch)

	type importResult struct {
		SkillID int64  `json:"skill_id"`
		Name    string `json:"name"`
		Files   int    `json:"files"`
		Updated bool   `json:"updated"`
	}

	var results []importResult

	for _, sk := range discovered {
		existing, hasExisting := existingBySource[sk.SkillURL]

		skill := &store.Skill{
			Name:        sk.Name,
			Description: sk.Description,
			Type:        "github",
			SourceURL:   sk.SkillURL,
			Tags:        []string{repoTag},
			Enabled:     true,
			CommitSHA:   commitSHA,
		}

		var id int64
		var isUpdate bool

		if hasExisting {
			id = existing.ID
			isUpdate = true
			skill.ID = id
			// 清除旧磁盘文件和文件索引
			h.skillFS.DeleteSkillDir(id, existing.Name)
			h.skillRepo.DeleteAllFileIndexes(id)
		} else {
			newID, err := h.skillRepo.Create(skill)
			if err != nil {
				log.Printf("[Skill] 创建技能记录失败 %s: %v", sk.Name, err)
				continue
			}
			id = newID
		}

		basePath, err := h.skillFS.EnsureDir(id, sk.Name)
		if err != nil {
			if !isUpdate {
				h.skillRepo.Delete(id)
			}
			continue
		}
		skill.ID = id
		skill.BasePath = basePath
		h.skillRepo.Update(skill)

		fileCount := 0
		for _, entry := range sk.Files {
			if err := h.skillFS.WriteFile(id, sk.Name, entry.Path, entry.Content); err != nil {
				continue
			}
			h.skillRepo.SaveFileIndex(id, entry.Path, int64(len(entry.Content)))
			fileCount++
		}

		// 添加到组合（只在多技能时）
		if comboID > 0 {
			h.skillCombinationRepo.AddSkill(comboID, id)
		}

		results = append(results, importResult{
			SkillID: id,
			Name:    sk.Name,
			Files:   fileCount,
			Updated: isUpdate,
		})
	}

	log.Printf("[Skill] 仓库导入成功: %s, %d 个技能, 组合=%d", repoURL, len(results), comboID)

	if len(discovered) > 1 {
		c.JSON(http.StatusCreated, gin.H{
			"repo_url":    repoURL,
			"skills":      results,
			"combination": gin.H{"id": comboID, "name": comboName},
			"message":     fmt.Sprintf("成功导入 %d 个技能，并创建了组合「%s」", len(results), comboName),
		})
	} else {
		c.JSON(http.StatusCreated, gin.H{
			"repo_url": repoURL,
			"skills":   results,
			"message":  fmt.Sprintf("成功导入 %d 个技能", len(results)),
		})
	}
}

// SyncRepo 同步仓库中所有技能的更新
// POST /api/skills/sync-repo
func (h *SkillHandler) SyncRepo(c *gin.Context) {
	var req importRepoRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RepoURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 repo_url"})
		return
	}

	info, err := h.ghClient.ParseGitHubURL(req.RepoURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的仓库 URL: " + err.Error()})
		return
	}

	// 从 DB 读取 GitHub Token
	token := req.GitHubToken
	if token == "" {
		if pc, err := h.proxyConfigRepo.Get(); err == nil && pc.GitHubToken != "" {
			token = pc.GitHubToken
		}
	}
	client := h.ghClient
	if token != "" {
		client = upstream.NewGitHubClient()
		client.SetToken(token)
	}

	repoTag := "repo:" + info.Owner + "/" + info.Repo
	existingSkills, err := h.skillRepo.List("", repoTag)
	if err != nil || len(existingSkills) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到来自该仓库的技能，请先使用导入功能"})
		return
	}

	branch := info.Branch
	dirPath := info.Path
	if req.Path != "" {
		dirPath = req.Path
	}

	log.Printf("[Skill] 同步仓库: %s/%s, %d 个技能", info.Owner, info.Repo, len(existingSkills))

	// 重新扫描发现
	discovered, err := client.DiscoverSkills(info.Owner, info.Repo, branch, dirPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "扫描仓库失败: " + err.Error()})
		return
	}

	// 对每个已导入技能，按 source_url 匹配更新
	updated := 0
	for _, existing := range existingSkills {
		for _, sk := range discovered {
			if sk.SkillURL == existing.SourceURL {
				// 删除旧文件，写入新文件
				h.skillFS.DeleteSkillDir(existing.ID, existing.Name)
				h.skillFS.EnsureDir(existing.ID, existing.Name)
				h.skillRepo.DeleteAllFileIndexes(existing.ID)

				for _, entry := range sk.Files {
					h.skillFS.WriteFile(existing.ID, sk.Name, entry.Path, entry.Content)
					h.skillRepo.SaveFileIndex(existing.ID, entry.Path, int64(len(entry.Content)))
				}

				// 更新名称和描述
				existing.Name = sk.Name
				existing.Description = sk.Description
				h.skillRepo.Update(&existing)

				updated++
				break
			}
		}
	}

	log.Printf("[Skill] 仓库同步完成: %s, %d/%d 个技能已更新", client.BuildRepoURL(info.Owner, info.Repo), updated, len(existingSkills))

	// 同步后更新所有技能的 commit_sha
	if commitSHA, err := client.GetLatestCommitSHA(info.Owner, info.Repo, branch); err == nil {
		for _, existing := range existingSkills {
			existing.CommitSHA = commitSHA
			h.skillRepo.Update(&existing)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    fmt.Sprintf("同步完成，%d/%d 个技能已更新", updated, len(existingSkills)),
		"updated":    updated,
		"total":      len(existingSkills),
	})
}

// checkUpdateRepo 单个仓库的更新检查结果
type checkUpdateRepo struct {
	RepoURL   string              `json:"repo_url"`
	Owner     string              `json:"owner"`
	Repo      string              `json:"repo"`
	HasUpdate bool                `json:"has_update"`
	Branch    string              `json:"branch"`
	Skills    []checkUpdateSkill  `json:"skills"`
}

type checkUpdateSkill struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CommitSHA  string `json:"commit_sha"`
	HasUpdate  bool   `json:"has_update"`
}

// CheckSkillUpdates 检查所有 GitHub 技能的更新状态
// GET /api/skills/check-updates
func (h *SkillHandler) CheckSkillUpdates(c *gin.Context) {
	all, err := h.skillRepo.List("", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 收集所有 type=github 的技能，按 owner/repo 分组
	type repoKey struct {
		Owner  string
		Repo   string
		Branch string
	}
	type repoGroup struct {
		Key    repoKey
		Skills []store.Skill
	}
	groups := make(map[repoKey]*repoGroup)

	for _, s := range all {
		if s.Type != "github" || s.SourceURL == "" {
			continue
		}
		info, err := h.ghClient.ParseGitHubURL(s.SourceURL)
		if err != nil {
			continue
		}
		key := repoKey{Owner: info.Owner, Repo: info.Repo, Branch: info.Branch}
		if _, ok := groups[key]; !ok {
			groups[key] = &repoGroup{Key: key}
		}
		groups[key].Skills = append(groups[key].Skills, s)
	}

	var repos []checkUpdateRepo
	for _, group := range groups {
		latestSHA, err := h.ghClient.GetLatestCommitSHA(group.Key.Owner, group.Key.Repo, group.Key.Branch)
		hasError := err != nil

		repoHasUpdate := false
		repoURL := h.ghClient.BuildRepoURL(group.Key.Owner, group.Key.Repo)

		var skills []checkUpdateSkill
		for _, s := range group.Skills {
			skillHasUpdate := false
			if !hasError && s.CommitSHA != "" && latestSHA != "" && s.CommitSHA != latestSHA {
				skillHasUpdate = true
				repoHasUpdate = true
			}
			skills = append(skills, checkUpdateSkill{
				ID:        s.ID,
				Name:      s.Name,
				CommitSHA: s.CommitSHA,
				HasUpdate: skillHasUpdate,
			})
		}
		_ = hasError // 忽略 API 错误，只把技能标记为无更新

		repos = append(repos, checkUpdateRepo{
			RepoURL:   repoURL,
			Owner:     group.Key.Owner,
			Repo:      group.Key.Repo,
			HasUpdate: repoHasUpdate,
			Branch:    group.Key.Branch,
			Skills:    skills,
		})
	}

	sort.Slice(repos, func(i, j int) bool {
		return repos[i].RepoURL < repos[j].RepoURL
	})

	c.JSON(http.StatusOK, gin.H{"repos": repos})
}
