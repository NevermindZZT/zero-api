package mcp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/never/zero-api/internal/store"
)

// MCPServer MCP 技能管理 — 挂载到主 API 端口共享 Gin 路由
type MCPServer struct {
	skillRepo           *store.SkillRepo
	skillCombinationRepo *store.SkillCombinationRepo
	skillFS             *store.SkillFS
	token               string
	httpServer          *server.StreamableHTTPServer
}

func NewMCPServer(skillRepo *store.SkillRepo, comboRepo *store.SkillCombinationRepo, skillFS *store.SkillFS, token string) *MCPServer {
	return &MCPServer{
		skillRepo:           skillRepo,
		skillCombinationRepo: comboRepo,
		skillFS:             skillFS,
		token:               token,
	}
}

// Build 构建 MCP 核心（MCPServer + StreamableHTTPServer），返回 http.Handler 供 Gin 挂载
func (s *MCPServer) Build() http.Handler {
	mcpServer := server.NewMCPServer(
		"zero-api MCP Skill Manager",
		"1.0.0",
		server.WithInstructions("AI Agent Skill Manager - manage and install personal skills"),
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	s.registerTools(mcpServer)

	s.httpServer = server.NewStreamableHTTPServer(mcpServer,
		server.WithEndpointPath(""),
		server.WithStateLess(true),
		server.WithHeartbeatInterval(30*time.Second),
		server.WithSessionIdleTTL(5*time.Minute),
		server.WithStreamableHTTPCORS(
			server.WithCORSAllowedOrigins("*"),
		),
	)

	log.Printf("[MCP] 已挂载到 /mcp 路径")
	return s.httpServer
}

// toolResult 包装 mcp.NewToolResultJSON，统一处理 error
func toolResult(v interface{}) *mcp.CallToolResult {
	r, err := mcp.NewToolResultJSON(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("序列化结果失败: %v", err))
	}
	return r
}

func (s *MCPServer) registerTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(mcp.NewTool("list_skill_combinations",
		mcp.WithDescription("列出所有技能组合，返回每个组合的 ID、名称、描述和包含的技能数量"),
	), s.handleListSkillCombinations)

	mcpServer.AddTool(mcp.NewTool("get_skill_combination",
		mcp.WithDescription("获取指定技能组合的详情及其包含的所有技能摘要信息"),
		mcp.WithNumber("combination_id", mcp.Required(), mcp.Description("技能组合的 ID")),
	), s.handleGetSkillCombination)

	mcpServer.AddTool(mcp.NewTool("list_skills",
		mcp.WithDescription("搜索和筛选技能，可按关键词和标签过滤，返回技能列表及文件数量"),
		mcp.WithString("q", mcp.Description("搜索关键词（匹配名称和描述）")),
		mcp.WithString("tag", mcp.Description("按标签筛选")),
	), s.handleListSkills)

	mcpServer.AddTool(mcp.NewTool("search_skills",
		mcp.WithDescription("通过关键词搜索技能，返回匹配的技能列表"),
		mcp.WithString("q", mcp.Required(), mcp.Description("搜索关键词")),
	), s.handleSearchSkills)

	mcpServer.AddTool(mcp.NewTool("get_skill",
		mcp.WithDescription("获取技能详情，包括技能元数据和文件路径列表（不含文件内容）"),
		mcp.WithNumber("skill_id", mcp.Required(), mcp.Description("技能的 ID")),
	), s.handleGetSkill)

	mcpServer.AddTool(mcp.NewTool("get_skill_file",
		mcp.WithDescription("获取技能中指定文件的完整内容"),
		mcp.WithNumber("skill_id", mcp.Required(), mcp.Description("技能的 ID")),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("文件路径（如 main.md、rules/python.yaml）")),
	), s.handleGetSkillFile)

	mcpServer.AddTool(mcp.NewTool("install_combination",
		mcp.WithDescription("一键安装技能组合：返回组合下所有技能的全部文件内容，供 AI Agent 一次性加载到上下文中使用"),
		mcp.WithNumber("combination_id", mcp.Required(), mcp.Description("技能组合的 ID")),
	), s.handleInstallCombination)

	mcpServer.AddTool(mcp.NewTool("use_skill",
		mcp.WithDescription("直接使用技能：返回指定技能的全部文件内容，AI Agent 可按需调用"),
		mcp.WithNumber("skill_id", mcp.Required(), mcp.Description("技能的 ID")),
	), s.handleUseSkill)
}

// ===== 辅助函数 =====

func getArgs(req mcp.CallToolRequest) map[string]any {
	if args, ok := req.Params.Arguments.(map[string]any); ok {
		return args
	}
	return nil
}

func getInt64(args map[string]any, key string) int64 {
	if v, ok := args[key]; ok {
		if f, ok := v.(float64); ok {
			return int64(f)
		}
	}
	return 0
}

func getString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ===== Tool Handlers =====

func (s *MCPServer) handleListSkillCombinations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	combos, err := s.skillCombinationRepo.List()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("获取组合列表失败: %v", err)), nil
	}

	type comboResult struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		SkillCount  int    `json:"skill_count"`
	}

	results := make([]comboResult, 0, len(combos))
	for _, c := range combos {
		results = append(results, comboResult{
			ID:          c.ID,
			Name:        c.Name,
			Description: c.Description,
			SkillCount:  c.SkillCount,
		})
	}

	return toolResult(results), nil
}

func (s *MCPServer) handleGetSkillCombination(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgs(req)
	comboID := getInt64(args, "combination_id")

	combo, err := s.skillCombinationRepo.GetByID(comboID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("组合不存在: %v", err)), nil
	}

	skills, err := s.skillCombinationRepo.GetSkills(comboID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("获取技能列表失败: %v", err)), nil
	}

	type skillSummary struct {
		ID          int64    `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}

	skillSummaries := make([]skillSummary, 0, len(skills))
	for _, sk := range skills {
		skillSummaries = append(skillSummaries, skillSummary{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Tags:        sk.Tags,
		})
	}

	return toolResult(map[string]interface{}{
		"id":          combo.ID,
		"name":        combo.Name,
		"description": combo.Description,
		"skills":      skillSummaries,
	}), nil
}

func (s *MCPServer) handleListSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgs(req)
	q := getString(args, "q")
	tag := getString(args, "tag")

	skills, err := s.skillRepo.List(q, tag)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("获取技能列表失败: %v", err)), nil
	}

	type skillResult struct {
		ID          int64    `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Type        string   `json:"type"`
		Tags        []string `json:"tags"`
		FileCount   int      `json:"file_count"`
	}

	results := make([]skillResult, 0, len(skills))
	for _, sk := range skills {
		files, _ := s.skillFS.ListFiles(sk.ID, sk.Name)
		results = append(results, skillResult{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Type:        sk.Type,
			Tags:        sk.Tags,
			FileCount:   len(files),
		})
	}

	return toolResult(results), nil
}

func (s *MCPServer) handleSearchSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgs(req)
	q := getString(args, "q")

	skills, err := s.skillRepo.List(q, "")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("搜索失败: %v", err)), nil
	}

	type skillResult struct {
		ID          int64    `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}

	results := make([]skillResult, 0, len(skills))
	for _, sk := range skills {
		results = append(results, skillResult{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Tags:        sk.Tags,
		})
	}

	return toolResult(results), nil
}

func (s *MCPServer) handleGetSkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgs(req)
	skillID := getInt64(args, "skill_id")

	skill, err := s.skillRepo.GetByID(skillID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("技能不存在: %v", err)), nil
	}

	files, _ := s.skillFS.ListFiles(skill.ID, skill.Name)

	type fileResult struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}

	fileResults := make([]fileResult, 0, len(files))
	for _, f := range files {
		fileResults = append(fileResults, fileResult{
			Path: f.Path,
			Size: f.Size,
		})
	}

	return toolResult(map[string]interface{}{
		"id":          skill.ID,
		"name":        skill.Name,
		"description": skill.Description,
		"type":        skill.Type,
		"tags":        skill.Tags,
		"files":       fileResults,
	}), nil
}

func (s *MCPServer) handleGetSkillFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgs(req)
	skillID := getInt64(args, "skill_id")
	filePath := getString(args, "file_path")

	skill, err := s.skillRepo.GetByID(skillID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("技能不存在: %v", err)), nil
	}

	content, err := s.skillFS.ReadFile(skill.ID, skill.Name, filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("文件不存在: %s", filePath)), nil
	}

	return toolResult(map[string]interface{}{
		"skill_name": skill.Name,
		"file_path":  filePath,
		"content":    string(content),
	}), nil
}

func (s *MCPServer) handleInstallCombination(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgs(req)
	comboID := getInt64(args, "combination_id")

	combo, err := s.skillCombinationRepo.GetByID(comboID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("组合不存在: %v", err)), nil
	}

	skills, err := s.skillCombinationRepo.GetSkills(comboID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("获取技能列表失败: %v", err)), nil
	}

	type fileContent struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	type skillContent struct {
		Name        string        `json:"name"`
		Description string        `json:"description"`
		Files       []fileContent `json:"files"`
	}

	skillContents := make([]skillContent, 0, len(skills))
	for _, sk := range skills {
		files, err := s.skillFS.ReadAllFiles(sk.ID, sk.Name)
		if err != nil {
			continue
		}
		fcs := make([]fileContent, 0, len(files))
		for _, f := range files {
			fcs = append(fcs, fileContent{Path: f.Path, Content: f.Content})
		}
		skillContents = append(skillContents, skillContent{
			Name:        sk.Name,
			Description: sk.Description,
			Files:       fcs,
		})
	}

	return toolResult(map[string]interface{}{
		"combination_name": combo.Name,
		"description":      combo.Description,
		"skills":           skillContents,
	}), nil
}

func (s *MCPServer) handleUseSkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := getArgs(req)
	skillID := getInt64(args, "skill_id")

	skill, err := s.skillRepo.GetByID(skillID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("技能不存在: %v", err)), nil
	}

	files, err := s.skillFS.ReadAllFiles(skill.ID, skill.Name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("读取技能文件失败: %v", err)), nil
	}

	type fileContent struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	fcs := make([]fileContent, 0, len(files))
	for _, f := range files {
		fcs = append(fcs, fileContent{Path: f.Path, Content: f.Content})
	}

	return toolResult(map[string]interface{}{
		"name":        skill.Name,
		"description": skill.Description,
		"type":        skill.Type,
		"tags":        skill.Tags,
		"files":       fcs,
	}), nil
}
