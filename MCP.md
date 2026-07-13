# MCP 技能管理服务

zero-api 内置了 [MCP (Model Context Protocol)](https://modelcontextprotocol.io/) 服务器，提供个人技能（Skill）管理能力。AI Agent 可以通过 MCP 协议发现、安装和使用技能。

> **关于 Skill**：技能是 AI Agent 的指令包，每个 skill 是一个包含 `SKILL.md`（YAML frontmatter + 指令内容）的文件夹，可附带 `scripts/`、`references/` 等辅助资源。详见下方目录结构说明。

---

## 连接信息

MCP 服务与主 API 共享端口（默认 `8080`），通过路径 `/mcp` 区分。

| 项目 | 值 |
|------|-----|
| 连接 URL | `http://host:8080/mcp` |
| 协议 | MCP Streamable HTTP |
| 认证 | 可选 Token（配置文件 `mcp.token` 中设置） |

---

## Agent 配置

> **关键说明**：是否需要配置 `headers` 取决于你是否在配置文件中设置了 `mcp.token`。
> 没设 token 就不需要 headers，设了 token **所有** MCP 客户端都必须带上。

### 场景一：未启用 Token 认证（`mcp.token: ""`）

这是默认配置，Agent 无需任何认证即可连接。

**Claude Desktop** (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "zero-api-skills": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

**Cursor** (MCP 配置):

```json
{
  "mcpServers": {
    "zero-api-skills": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

**GitHub Copilot** (VS Code 设置 `github.copilot.mcp`):

```json
{
  "github.copilot.mcp": {
    "inputs": [],
    "servers": {
      "zero-api-skills": {
        "type": "http",
        "url": "http://localhost:8080/mcp",
        "headers": {}
      }
    }
  }
}
```

### 场景二：启用了 Token 认证（`mcp.token: "your-token"`）

当配置文件中设置了 token 后，所有 MCP 请求都必须携带 `Authorization: Bearer <token>` header，否则返回 401。

**Claude Desktop** (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "zero-api-skills": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer your-token-here"
      }
    }
  }
}
```

**Cursor** (MCP 配置):

```json
{
  "mcpServers": {
    "zero-api-skills": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer your-token-here"
      }
    }
  }
}
```

**GitHub Copilot** (VS Code 设置 `github.copilot.mcp`):

```json
{
  "github.copilot.mcp": {
    "inputs": [],
    "servers": {
      "zero-api-skills": {
        "type": "http",
        "url": "http://localhost:8080/mcp",
        "headers": {
          "Authorization": "Bearer your-token-here"
        }
      }
    }
  }
}
```

### 配置说明

| 配置项 | 含义 |
|--------|------|
| `url` | MCP 服务地址，固定为 `http://你的IP:8080/mcp` |
| `headers` | **仅当 `mcp.token` 不为空时需要**，填入 `Bearer <token>` |
| token 取值 | 查看配置文件中的 `mcp.token` 字段，或在 MCP 设置页面查看 |

### curl 测试命令

**无 Token：**

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

**有 Token：**

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token-here" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

---

## 从 GitHub 仓库导入

zero-api 支持两种从 GitHub 导入 skill 的方式：

### 导入单个技能

导入 GitHub 上的某个 skill 目录（包含 `SKILL.md`）：

```bash
curl -X POST http://localhost:8080/api/skills/import-github \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <login-token>" \
  -d '{"source_url": "https://github.com/user/repo/tree/main/skills/my-skill"}'
```

### 批量导入仓库（推荐）

扫描整个 GitHub 仓库，自动发现所有含 `SKILL.md` 的子目录，每个子目录作为一个 skill 导入，并自动：

- 记录仓库地址
- 添加标签 `repo:owner/repo`
- 创建以仓库名命名的技能组合，包含所有导入的技能

```bash
# 扫描仓库根目录下所有 skill
curl -X POST http://localhost:8080/api/skills/import-repo \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <login-token>" \
  -d '{"repo_url": "https://github.com/owner/repo"}'

# 扫描仓库子目录（如 skills/ 下）
curl -X POST http://localhost:8080/api/skills/import-repo \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <login-token>" \
  -d '{"repo_url": "https://github.com/owner/repo", "path": "skills"}'
```

### 更新仓库中的技能

对已从仓库导入的技能重新下载更新文件内容：

```bash
curl -X POST http://localhost:8080/api/skills/sync-repo \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <login-token>" \
  -d '{"repo_url": "https://github.com/owner/repo"}'
```

### 标签说明

从仓库批量导入的技能会自动添加 `repo:owner/repo` 格式的标签，可在前端按标签筛选，也方便后续通过标签查找同一仓库的所有技能。

---

## MCP 工具列表

| # | 工具名 | 作用 | 能力分组 |
|---|--------|------|----------|
| 1 | `list_skill_combinations` | 列出所有技能组合 | 发现 |
| 2 | `get_skill_combination` | 获取组合详情及所含技能摘要 | 发现 |
| 3 | `list_skills` | 搜索和筛选技能（按关键词/标签） | 发现 |
| 4 | `search_skills` | 关键词搜索技能 | 发现 |
| 5 | `get_skill` | 获取技能详情 + 文件路径列表 | 浏览 |
| 6 | `get_skill_file` | 获取技能中指定文件的完整内容 | 使用 |
| 7 | `install_combination` | **一键安装组合** — 返回全部文件内容 | 安装 |
| 8 | `use_skill` | **直接使用技能** — 返回全部文件内容 | 使用 |

### 工具输入/输出

#### 1. `list_skill_combinations`

```
输入: 无
输出: [{ id, name, description, skill_count }]
```

#### 2. `get_skill_combination`

```
输入: { combination_id: number }
输出: { id, name, description, skills: [{ id, name, description, tags }] }
```

#### 3. `list_skills`

```
输入: { q?: string, tag?: string }
输出: [{ id, name, description, type, tags, file_count }]
```

#### 4. `search_skills`

```
输入: { q: string }
输出: [{ id, name, description, tags }]
```

#### 5. `get_skill`

```
输入: { skill_id: number }
输出: { id, name, description, type, tags, files: [{ path, size }] }
```

#### 6. `get_skill_file`

```
输入: { skill_id: number, file_path: string }
输出: { skill_name, file_path, content }
```

#### 7. `install_combination`

```
输入: { combination_id: number }
输出: {
  combination_name,
  skills: [{
    name, description,
    files: [{ path, content }]
  }]
}
```

#### 8. `use_skill`

```
输入: { skill_id: number }
输出: {
  name, description, type, tags,
  files: [{ path, content }]
}
```

---

## 测试命令

### 列出所有工具

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

### 调用工具

```bash
# 列出所有技能组合
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_skill_combinations","arguments":{}}}'

# 获取单个技能完整内容
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_skill","arguments":{"skill_id":1}}}'

# 一键安装组合
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"install_combination","arguments":{"combination_id":1}}}'
```

---

## 使用流程

### 给 Agent 的提示词示例

当你在 Agent 的指令中描述这个 MCP 服务时，可以用：

```
## Skill 管理 MCP 服务

你可以通过 MCP 工具管理技能，技能是存储在 zero-api 中的指令包。

首先用 `list_skill_combinations` 查看可用的技能组合，
然后用 `get_skill_combination` 查看组合详情，
用 `install_combination` 一次性安装组合下所有技能的全部文件内容到当前上下文。

你也可以直接用 `search_skills` 搜索技能，
用 `use_skill` 获取单个技能的内容按需使用。
```

### 典型工作流

1. **Agent 发现可用技能**
   ```
   Agent 调用 list_skill_combinations → 返回 ["代码审查", "单元测试"]
   ```

2. **Agent 安装一个组合**
   ```
   Agent 调用 install_combination(combination_id=1)
   → 返回组合下所有技能的 SKILL.md + 辅助文件内容
   → Agent 将这些指令加载到上下文
   ```

3. **Agent 按需使用单个技能**
   ```
   Agent 调用 search_skills(q="review")
   → 找到 code-review 技能
   → 调用 use_skill(skill_id=1)
   → 获取完整文件内容
   ```

---

## Skill 目录结构

每个 skill 是一个包含 `SKILL.md` 的文件夹，结构如下：

```
my-skill/
├── SKILL.md          ← 入口文件（YAML frontmatter + 指令内容）
├── scripts/          ← 可选：辅助脚本
├── references/       ← 可选：参考文档
└── examples/         ← 可选：示例输出
```

`SKILL.md` 格式：

```yaml
---
name: my-skill              # 必填：技能唯一标识
description: "..."           # 必填：技能描述
allowed-tools: ["*"]         # 可选：允许的 MCP 工具
user-invocable: true         # 可选：是否可通过 /skill-name 调用
---
# 技能名称

这里是完整的 skill 指令内容，Agent 会按此执行。
可以包含多段 Markdown、代码块等。
```

---

## 配置文件

```yaml
mcp:
  enabled: true        # 是否启用 MCP 服务
  token: ""            # MCP 访问 Token（留空不校验）
  skills_dir: "data/skills"  # 技能文件存储目录
```
