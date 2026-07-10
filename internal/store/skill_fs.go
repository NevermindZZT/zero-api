package store

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const SKILL_MD_FILE = "SKILL.md"

// FileEntry 文件条目（含内容）
type FileEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int64  `json:"size,omitempty"`
}

// SkillFS 管理技能文件在磁盘上的读写
// 目录结构: <rootDir>/{skillID}-{sanitized_name}/
type SkillFS struct {
	rootDir string
}

func NewSkillFS(rootDir string) *SkillFS {
	os.MkdirAll(rootDir, 0755)
	return &SkillFS{rootDir: rootDir}
}

// SkillDir 返回技能目录路径
func (fs *SkillFS) SkillDir(skillID int64, skillName string) string {
	dirName := fmt.Sprintf("%d-%s", skillID, sanitizeDirName(skillName))
	return filepath.Join(fs.rootDir, dirName)
}

// EnsureDir 确保技能目录存在，返回 basePath
func (fs *SkillFS) EnsureDir(skillID int64, skillName string) (string, error) {
	dir := fs.SkillDir(skillID, skillName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建技能目录失败: %w", err)
	}
	// 返回相对路径（相对于 rootDir）
	rel, _ := filepath.Rel(fs.rootDir, dir)
	return rel, nil
}

// WriteFile 写入文件，自动创建子目录
func (fs *SkillFS) WriteFile(skillID int64, skillName string, filePath string, content []byte) error {
	fullPath := filepath.Join(fs.SkillDir(skillID, skillName), filePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("创建文件子目录失败: %w", err)
	}
	return os.WriteFile(fullPath, content, 0644)
}

// ReadFile 读取文件内容
func (fs *SkillFS) ReadFile(skillID int64, skillName string, filePath string) ([]byte, error) {
	fullPath := filepath.Join(fs.SkillDir(skillID, skillName), filePath)
	// 安全检查：确保文件在技能目录内
	absDir, _ := filepath.Abs(fs.SkillDir(skillID, skillName))
	absFile, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absFile, absDir) {
		return nil, fmt.Errorf("路径越界: %s", filePath)
	}
	return os.ReadFile(fullPath)
}

// DeleteSkillDir 删除整个技能目录
func (fs *SkillFS) DeleteSkillDir(skillID int64, skillName string) error {
	dir := fs.SkillDir(skillID, skillName)
	return os.RemoveAll(dir)
}

// DeleteFile 删除单个文件
func (fs *SkillFS) DeleteFile(skillID int64, skillName string, filePath string) error {
	fullPath := filepath.Join(fs.SkillDir(skillID, skillName), filePath)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	// 清理空目录
	cleanupEmptyDirs(filepath.Dir(fullPath), fs.SkillDir(skillID, skillName))
	return nil
}

// ListFiles 遍历目录树，返回文件路径列表
func (fs *SkillFS) ListFiles(skillID int64, skillName string) ([]FileEntry, error) {
	dir := fs.SkillDir(skillID, skillName)
	var entries []FileEntry

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		entries = append(entries, FileEntry{
			Path: rel,
			Size: info.Size(),
		})
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return entries, nil
	}
	return entries, err
}

// ReadAllFiles 读取目录下所有文件内容
func (fs *SkillFS) ReadAllFiles(skillID int64, skillName string) ([]FileEntry, error) {
	dir := fs.SkillDir(skillID, skillName)
	var entries []FileEntry

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取文件 %s 失败: %w", rel, err)
		}
		entries = append(entries, FileEntry{
			Path:    rel,
			Content: string(data),
			Size:    info.Size(),
		})
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return entries, nil
	}
	return entries, err
}

// FileSize 获取单个文件大小
func (fs *SkillFS) FileSize(skillID int64, skillName string, filePath string) (int64, error) {
	fullPath := filepath.Join(fs.SkillDir(skillID, skillName), filePath)
	info, err := os.Stat(fullPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// sanitizeDirName 清理目录名中的特殊字符
func sanitizeDirName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", "\x00", "", "..", "_",
		":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	name = replacer.Replace(name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "skill"
	}
	// 限制长度
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// cleanupEmptyDirs 递归清理空目录（直到根目录）
func cleanupEmptyDirs(dir, rootDir string) {
	current := dir
	for strings.HasPrefix(current, rootDir) && current != rootDir {
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) > 0 {
			return
		}
		os.Remove(current)
		current = filepath.Dir(current)
	}
}

// SKILLMDInfo SKILL.md 中解析出的元信息
type SKILLMDInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// ParseSKILLMD 解析 SKILL.md 内容，提取技能元信息
//
// SKILL.md 是 AI Agent Skill 的标准入口文件，格式为：
//   ---
//   name: my-skill              # 必填，技能标识符，小写字母+连字符，最长64字符
//   description: "描述..."      # 必填，技能描述，最长1024字符
//   argument-hint: "[target]"   # 可选，参数提示
//   allowed-tools: ["*"]        # 可选，允许的工具
//   user-invocable: true        # 可选，是否可通过 /skill-name 调用
//   ---
//   # 这里是完整的 skill 指令内容...
//   可以包含多行 Markdown 指令
//
// Skill 目录中的其他文件（scripts/、references/、examples/ 等）是辅助资源
func ParseSKILLMD(content string) *SKILLMDInfo {
	info := &SKILLMDInfo{Content: content}
	lines := strings.Split(content, "\n")

	// 解析 YAML frontmatter (---\n...\n---)
	if len(lines) >= 2 && strings.TrimSpace(lines[0]) == "---" {
		endIdx := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				endIdx = i
				break
			}
		}
		if endIdx > 0 {
			fmLines := lines[1:endIdx]
			for _, fl := range fmLines {
				fl = strings.TrimSpace(fl)
				if strings.HasPrefix(fl, "name:") {
					info.Name = strings.TrimSpace(strings.TrimPrefix(fl, "name:"))
					info.Name = strings.Trim(info.Name, "\"' ")
				}
				if strings.HasPrefix(fl, "description:") {
					info.Description = strings.TrimSpace(strings.TrimPrefix(fl, "description:"))
					info.Description = strings.Trim(info.Description, "\"' ")
				}
			}
			// frontmatter 之后的内容是完整的 skill 指令
			body := strings.TrimSpace(strings.Join(lines[endIdx+1:], "\n"))
			info.Content = body
		}
	}

	return info
}

// ExtractZip 解压 zip 文件到技能目录，返回文件条目列表
// zipFile 是 zip 文件的字节内容
// 自动检测并去除 zip 内的公共根目录前缀
func (fs *SkillFS) ExtractZip(skillID int64, skillName string, zipData []byte) ([]FileEntry, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("读取 zip 文件失败: %w", err)
	}

	dir := fs.SkillDir(skillID, skillName)

	// 第一遍：收集所有文件路径，检测公共前缀
	var rawNames []string
	for _, f := range reader.File {
		if !f.FileInfo().IsDir() {
			rawNames = append(rawNames, filepath.ToSlash(f.Name))
		}
	}
	if len(rawNames) == 0 {
		return nil, fmt.Errorf("zip 文件中没有可提取的文件")
	}

	prefix := commonZipPrefix(rawNames)

	// 第二遍：提取文件
	var entries []FileEntry
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		cleanPath := strings.TrimPrefix(filepath.ToSlash(file.Name), prefix)
		cleanPath = strings.TrimLeft(cleanPath, "/")
		if cleanPath == "" {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("读取 zip 条目 %s 失败: %w", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("读取 zip 条目 %s 内容失败: %w", file.Name, err)
		}

		fullPath := filepath.Join(dir, cleanPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return nil, fmt.Errorf("创建目录失败: %w", err)
		}
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			return nil, fmt.Errorf("写入文件 %s 失败: %w", cleanPath, err)
		}

		entries = append(entries, FileEntry{
			Path:    cleanPath,
			Content: string(content),
			Size:    int64(len(content)),
		})
	}

	return entries, nil
}

// commonZipPrefix 检测 zip 文件路径列表的公共目录前缀
// 例如 ["my-skill/main.md", "my-skill/rules/py.yaml"] → "my-skill/"
// 例如 ["main.md", "rules/py.yaml"] → ""
func commonZipPrefix(names []string) string {
	if len(names) < 2 {
		return ""
	}
	// 取第一个路径的第一段作为候选前缀
	first := strings.SplitN(names[0], "/", 2)
	if len(first) < 2 {
		return "" // 根目录下没有子目录，无公共前缀
	}
	candidate := first[0] + "/"
	for _, name := range names[1:] {
		if !strings.HasPrefix(name, candidate) {
			return ""
		}
	}
	return candidate
}
