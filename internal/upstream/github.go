package upstream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const skillMDFile = "SKILL.md"

// GitHubFileNode GitHub Contents API 返回的文件节点
type GitHubFileNode struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	DownloadURL string `json:"download_url"`
}

// GitHubRepoInfo 解析后的 GitHub 仓库信息
type GitHubRepoInfo struct {
	Owner  string
	Repo   string
	Branch string
	Path   string
	IsDir  bool
	IsBlob bool
}

// ImportFileEntry 导入的文件条目
type ImportFileEntry struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// DiscoveredSkill 从仓库中发现的一个技能
type DiscoveredSkill struct {
	DirName     string            `json:"dir_name"`
	Files       []ImportFileEntry `json:"files"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	SkillURL    string            `json:"skill_url"`
}

// GitHubClient GitHub 内容获取客户端
type GitHubClient struct {
	httpClient *http.Client
	token      string // 可选 GitHub Token，提升 API 速率限制到 5000/h
}

func NewGitHubClient() *GitHubClient {
	return &GitHubClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetToken 设置 GitHub Token，所有 API 调用将使用此 Token 认证
func (c *GitHubClient) SetToken(token string) {
	c.token = token
}

// newGitHubRequest 创建带认证和标准 header 的 GitHub API 请求
func (c *GitHubClient) newGitHubRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zero-api-mcp")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

// authHeader 返回 Authorization header
func (c *GitHubClient) authHeader() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "zero-api-mcp")
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
	return h
}

// ParseGitHubURL 解析 GitHub URL，支持多种格式
func (c *GitHubClient) ParseGitHubURL(rawURL string) (*GitHubRepoInfo, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("解析 URL 失败: %w", err)
	}

	info := &GitHubRepoInfo{}

	if u.Host == "raw.githubusercontent.com" {
		parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 4)
		if len(parts) < 4 {
			return nil, fmt.Errorf("raw.githubusercontent.com URL 格式无效: %s", rawURL)
		}
		info.Owner, info.Repo, info.Branch, info.Path = parts[0], parts[1], parts[2], parts[3]
		info.IsBlob = true
		return info, nil
	}

	if u.Host != "github.com" {
		return nil, fmt.Errorf("不支持的 URL 域名: %s", u.Host)
	}

	parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 5)
	if len(parts) < 2 {
		return nil, fmt.Errorf("GitHub URL 格式无效: %s", rawURL)
	}

	info.Owner, info.Repo = parts[0], parts[1]
	if len(parts) < 3 {
		info.Branch, info.Path, info.IsDir = "main", "", true
		return info, nil
	}

	switch parts[2] {
	case "tree":
		info.IsDir = true
	case "blob":
		info.IsBlob = true
	default:
		info.Branch, info.Path, info.IsDir = "main", strings.Join(parts[2:], "/"), true
		return info, nil
	}

	if len(parts) < 4 {
		info.Branch, info.Path = "main", ""
		return info, nil
	}

	info.Branch = parts[3]
	if len(parts) >= 5 {
		info.Path = parts[4]
	}
	return info, nil
}

// ListContents 递归列出 GitHub 目录下的所有文件
func (c *GitHubClient) ListContents(owner, repo, branch, dirPath string) ([]GitHubFileNode, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", owner, repo, dirPath, branch)
	req, err := c.newGitHubRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		if branch != "main" {
			return c.ListContents(owner, repo, "main", dirPath)
		}
		if branch != "master" {
			return c.ListContents(owner, repo, "master", dirPath)
		}
		return nil, fmt.Errorf("GitHub 路径不存在: %s/%s/%s", owner, repo, dirPath)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API 返回 %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)

	var nodes []GitHubFileNode
	if json.Unmarshal(body, &nodes) == nil {
		var all []GitHubFileNode
		for _, n := range nodes {
			if n.Type == "file" {
				all = append(all, n)
			} else if n.Type == "dir" {
				sub, err := c.ListContents(owner, repo, branch, n.Path)
				if err == nil {
					all = append(all, sub...)
				}
			}
		}
		return all, nil
	}

	var single GitHubFileNode
	if json.Unmarshal(body, &single) == nil && single.Type == "file" {
		return []GitHubFileNode{single}, nil
	}

	return nil, fmt.Errorf("无法解析 GitHub API 响应")
}

// FetchRawContent 从 raw.githubusercontent.com 下载文件内容
func (c *GitHubClient) FetchRawContent(owner, repo, branch, filePath string) ([]byte, error) {
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, filePath)
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("User-Agent", "zero-api-mcp")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		altURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, path.Clean(filePath))
		req2, _ := http.NewRequest("GET", altURL, nil)
		req2.Header.Set("User-Agent", "zero-api-mcp")
		resp2, err2 := c.httpClient.Do(req2)
		if err2 != nil {
			return nil, fmt.Errorf("下载文件失败: %w (原始错误: %v)", err2, err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != 200 {
			return nil, fmt.Errorf("下载文件返回 %d", resp2.StatusCode)
		}
		return io.ReadAll(resp2.Body)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("下载文件返回 %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ImportFromGitHub 从 GitHub 导入单个技能的全部文件
func (c *GitHubClient) ImportFromGitHub(sourceURL string) ([]ImportFileEntry, error) {
	info, err := c.ParseGitHubURL(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("解析 GitHub URL 失败: %w", err)
	}

	if info.IsBlob {
		data, err := c.FetchRawContent(info.Owner, info.Repo, info.Branch, info.Path)
		if err != nil {
			return nil, err
		}
		return []ImportFileEntry{{Path: path.Base(info.Path), Content: data}}, nil
	}

	files, err := c.ListContents(info.Owner, info.Repo, info.Branch, info.Path)
	if err != nil {
		blobInfo, parseErr := c.ParseGitHubURL(sourceURL)
		if parseErr == nil && blobInfo.IsBlob {
			data, dlErr := c.FetchRawContent(blobInfo.Owner, blobInfo.Repo, blobInfo.Branch, blobInfo.Path)
			if dlErr != nil {
				return nil, fmt.Errorf("下载失败: %w", dlErr)
			}
			return []ImportFileEntry{{Path: path.Base(blobInfo.Path), Content: data}}, nil
		}
		return nil, fmt.Errorf("列出 GitHub 目录失败: %w", err)
	}

	basePrefix := info.Path
	if basePrefix != "" {
		basePrefix += "/"
	}

	var entries []ImportFileEntry
	for _, f := range files {
		relPath := strings.TrimPrefix(f.Path, basePrefix)
		data, err := c.FetchRawContent(info.Owner, info.Repo, info.Branch, f.Path)
		if err != nil {
			continue
		}
		entries = append(entries, ImportFileEntry{Path: relPath, Content: data})
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("未找到可下载的文件")
	}
	return entries, nil
}

// SkillNameFromURL 从 GitHub URL 推断技能名称
func (c *GitHubClient) SkillNameFromURL(sourceURL string) string {
	info, err := c.ParseGitHubURL(sourceURL)
	if err != nil || info.Path == "" {
		if err == nil {
			return info.Repo
		}
		return "imported-skill"
	}
	name := path.Base(info.Path)
	if name == "." || name == "/" {
		return info.Repo
	}
	return name
}

// BuildRepoURL 构建仓库根 URL
func (c *GitHubClient) BuildRepoURL(owner, repo string) string {
	return fmt.Sprintf("https://github.com/%s/%s", owner, repo)
}

// GetLatestCommitSHA 获取指定分支最新 commit 的 SHA（1 次 API 调用）
// 用于轻量检查是否有更新，不下任何文件内容
func (c *GitHubClient) GetLatestCommitSHA(owner, repo, branch string) (string, error) {
	candidates := []string{branch}
	if branch != "main" {
		candidates = append(candidates, "main")
		_ = "master" // 不支持 master，只尝试 main
	}

	var lastErr error
	for _, b := range candidates {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?sha=%s&per_page=1", owner, repo, b)
		req, err := c.newGitHubRequest("GET", url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 404 {
			lastErr = fmt.Errorf("分支 %s 不存在", b)
			continue
		}
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		var commits []struct {
			SHA string `json:"sha"`
		}
		if err := json.Unmarshal(body, &commits); err != nil {
			lastErr = fmt.Errorf("解析 commits 响应失败: %w", err)
			continue
		}
		if len(commits) == 0 {
			lastErr = fmt.Errorf("分支 %s 没有 commit", b)
			continue
		}
		return commits[0].SHA, nil
	}
	return "", fmt.Errorf("获取最新 commit SHA 失败: %v", lastErr)
}

// ====== Git Trees API: 全量文件树发现（1 次 API 调用替代 N 次） ======

// gitTreeEntry GitHub Git Trees API 的单个条目
type gitTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"` // "blob" 或 "tree"
	Sha  string `json:"sha"`
	Size int    `json:"size,omitempty"`
}

type gitTreeResponse struct {
	Sha       string         `json:"sha"`
	URL       string         `json:"url"`
	Tree      []gitTreeEntry `json:"tree"`
	Truncated bool           `json:"truncated"`
}

// getDefaultBranchTree 获取仓库默认分支的完整文件树（递归）
// 使用 Commits API（`GET /repos/{owner}/{repo}/commits?sha={branch}&per_page=1`）
// 获取最新 commit 的 tree SHA，然后调用 git/trees API 获取全量文件树
func (c *GitHubClient) getDefaultBranchTree(owner, repo, branch string) ([]gitTreeEntry, error) {
	candidates := []string{branch}
	if branch != "main" {
		candidates = append(candidates, "main")
	}
	if branch != "master" && branch != "main" {
		candidates = append(candidates, "master")
	}
	// 去重
	seen := map[string]bool{}
	var unique []string
	for _, b := range candidates {
		if !seen[b] {
			seen[b] = true
			unique = append(unique, b)
		}
	}

	var lastErr error
	for _, b := range unique {
		treeSHA, err := c.getLatestTreeSHA(owner, repo, b)
		if err != nil {
			lastErr = err
			continue
		}
		return c.getTreeRecursive(owner, repo, treeSHA)
	}

	return nil, fmt.Errorf("获取仓库文件树失败: %v", lastErr)
}

// getLatestTreeSHA 通过 Commits API 获取指定分支最新 commit 的 tree SHA
// GET /repos/{owner}/{repo}/commits?sha={branch}&per_page=1
// 返回体: [{ sha: "...", commit: { tree: { sha: "..." } } }]
func (c *GitHubClient) getLatestTreeSHA(owner, repo, branch string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?sha=%s&per_page=1", owner, repo, branch)
	req, err := c.newGitHubRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 403 {
		// 可能被限速，检查限速信息
		var errResp map[string]interface{}
		if json.Unmarshal(body, &errResp) == nil {
			if msg, ok := errResp["message"].(string); ok {
				return "", fmt.Errorf("GitHub API 错误: %s", msg)
			}
		}
		return "", fmt.Errorf("GitHub API 返回 403（可能已达到 API 速率限制）")
	}
	if resp.StatusCode == 404 {
		return "", fmt.Errorf("分支 %s 不存在", branch)
	}
	if resp.StatusCode == 409 {
		return "", fmt.Errorf("仓库为空")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 解析 [{sha, commit: {tree: {sha}}}]
	var commits []struct {
		Commit struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &commits); err != nil {
		return "", fmt.Errorf("解析 commits 响应失败: %w", err)
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("分支 %s 没有 commit", branch)
	}

	treeSHA := commits[0].Commit.Tree.SHA
	if treeSHA == "" {
		return "", fmt.Errorf("无法从 commit 中提取 tree SHA")
	}
	return treeSHA, nil
}

// getTreeRecursive 递归获取 git tree
func (c *GitHubClient) getTreeRecursive(owner, repo, treeSHA string) ([]gitTreeEntry, error) {
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", owner, repo, treeSHA)
	req, err := c.newGitHubRequest("GET", treeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 git tree 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Git Tree API 返回 %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	var result gitTreeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析 git tree 失败: %w", err)
	}

	return result.Tree, nil
}

// DiscoverSkills 通过 Git Trees API 一次性发现仓库中所有包含 SKILL.md 的技能
// 只需 2 次 API 调用（branches + trees），替代原来的 N 次 contents API 调用
// dirPath 可选，限定扫描的子目录（如 "skills"）；为空则扫描整个仓库
func (c *GitHubClient) DiscoverSkills(owner, repo, branch, dirPath string) ([]DiscoveredSkill, error) {
	// 获取完整文件树
	entries, err := c.getDefaultBranchTree(owner, repo, branch)
	if err != nil {
		return nil, err
	}

	// 如果指定了扫描路径，过滤只保留该路径下的条目
	filtered := entries
	if dirPath != "" {
		prefix := dirPath + "/"
		var f []gitTreeEntry
		for _, e := range entries {
			if e.Path == dirPath || strings.HasPrefix(e.Path, prefix) {
				f = append(f, e)
			}
		}
		filtered = f
	}

	// 第一遍：找到所有包含 SKILL.md 的目录
	skillDirs := make(map[string]bool)
	for _, e := range filtered {
		if e.Type != "blob" {
			continue
		}
		if e.Path == skillMDFile || strings.HasSuffix(e.Path, "/"+skillMDFile) {
			dir := path.Dir(e.Path)
			if dir == "." {
				dir = ""
			}
			skillDirs[dir] = true
		}
	}

	// 第二遍：为每个 skill 目录收集文件（排除属于其他嵌套 skill 的文件）
	// 先构建完整路径集合，确保父目录不会被当作子技能的文件
	var discovered []DiscoveredSkill
	for dir := range skillDirs {
		prefix := ""
		if dir != "" {
			prefix = dir + "/"
		}

		var filePaths []string
		for _, e := range filtered {
			if e.Type != "blob" {
				continue
			}
			if prefix == "" || strings.HasPrefix(e.Path, prefix) {
				rel := strings.TrimPrefix(e.Path, prefix)
				if rel == "" {
					continue
				}
				// 检查这个文件是否属于另一个嵌套的 skill 目录
				if strings.Contains(rel, "/") {
					parts := strings.SplitN(rel, "/", 2)
					subDir := dir
					if subDir == "" {
						subDir = parts[0]
					} else {
						subDir = subDir + "/" + parts[0]
					}
					// 如果子目录本身也是一个 skill，跳过（由那个 skill 自己处理）
					if skillDirs[subDir] {
						continue
					}
				}
				filePaths = append(filePaths, e.Path)
			}
		}

		if len(filePaths) == 0 {
			continue
		}

		// 下载所有文件
		var importEntries []ImportFileEntry
		for _, fp := range filePaths {
			data, err := c.FetchRawContent(owner, repo, branch, fp)
			if err != nil {
				continue
			}
			relPath := fp
			if prefix != "" {
				relPath = strings.TrimPrefix(fp, prefix)
			}
			importEntries = append(importEntries, ImportFileEntry{
				Path:    relPath,
				Content: data,
			})
		}
		if len(importEntries) == 0 {
			continue
		}

		// 解析 SKILL.md
		dirName := dir
		if dirName == "" {
			dirName = repo
		} else {
			dirName = path.Base(dir)
		}

		skillName, skillDesc := dirName, ""
		for _, e := range importEntries {
			if e.Path == skillMDFile || strings.HasSuffix(e.Path, "/"+skillMDFile) {
				n, d := parseSkillMDFrontmatter(string(e.Content))
				if n != "" {
					skillName = n
				}
				if d != "" {
					skillDesc = d
				}
				break
			}
		}

		skillURL := fmt.Sprintf("https://github.com/%s/%s/tree/%s/%s", owner, repo, branch, dir)
		discovered = append(discovered, DiscoveredSkill{
			DirName: dirName, Files: importEntries,
			Name: skillName, Description: skillDesc, SkillURL: skillURL,
		})
	}

	if len(discovered) == 0 {
		return nil, fmt.Errorf("未在仓库中发现包含 SKILL.md 的技能目录")
	}

	return discovered, nil
}

// parseSkillMDFrontmatter 从 SKILL.md 中提取 name 和 description
func parseSkillMDFrontmatter(content string) (name, desc string) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx <= 0 {
		return "", ""
	}
	for _, fl := range lines[1:endIdx] {
		fl = strings.TrimSpace(fl)
		if strings.HasPrefix(fl, "name:") {
			name = strings.Trim(strings.TrimPrefix(fl, "name:"), "\"' ")
		}
		if strings.HasPrefix(fl, "description:") {
			desc = strings.Trim(strings.TrimPrefix(fl, "description:"), "\"' ")
		}
	}
	// 富文本描述：如果 description 是 "... 跨行内容"，不支持。只取简单值。
	return name, desc
}
