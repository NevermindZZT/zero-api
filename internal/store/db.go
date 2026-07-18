package store

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

// DB 封装 SQLite 数据库连接
type DB struct {
	*sql.DB
}

// Open 打开并初始化数据库
func Open(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 启用 WAL 模式和设置 busy timeout
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=10000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("设置 PRAGMA 失败: %w", err)
		}
	}

	// 限制最大连接数，减少 WAL 模式下的写冲突
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	d := &DB{db}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	return d, nil
}

// migrate 自动建表
func (d *DB) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'openai',
			base_url TEXT NOT NULL,
			api_key TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS models (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL,
			model_id TEXT NOT NULL,
			display_name TEXT DEFAULT '',
			context_window INTEGER DEFAULT 0,
			max_output_tokens INTEGER DEFAULT 0,
			supports_vision INTEGER DEFAULT 0,
			supports_thinking INTEGER DEFAULT 0,
			supports_tools INTEGER DEFAULT 0,
			pricing_input REAL DEFAULT 0,
			pricing_output REAL DEFAULT 0,
			pricing_cache_read REAL DEFAULT 0,
			pricing_cache_write REAL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
			UNIQUE(channel_id, model_id)
		)`,
		`CREATE TABLE IF NOT EXISTS usage_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER,
			model_id INTEGER,
			api_key_id INTEGER,
			request_model TEXT NOT NULL,
			prompt_tokens INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			cache_hit_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			latency_ms INTEGER DEFAULT 0,
			cost REAL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE SET NULL,
			FOREIGN KEY (model_id) REFERENCES models(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proxy_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			intercept_domains TEXT DEFAULT '[]',
			smart_intercept_domains TEXT DEFAULT '[]',
			default_channel_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_created_at ON usage_records(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_channel ON usage_records(channel_id)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_model ON usage_records(model_id)`,
		`CREATE INDEX IF NOT EXISTS idx_models_channel ON models(channel_id)`,

		// 技能管理
		`CREATE TABLE IF NOT EXISTS skills (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			type TEXT NOT NULL DEFAULT 'manual',
			source_url TEXT DEFAULT '',
			base_path TEXT DEFAULT '',
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS skill_tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			skill_id INTEGER NOT NULL,
			tag TEXT NOT NULL,
			FOREIGN KEY (skill_id) REFERENCES skills(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			skill_id INTEGER NOT NULL,
			file_path TEXT NOT NULL,
			file_size INTEGER DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			UNIQUE(skill_id, file_path)
		)`,
		`CREATE TABLE IF NOT EXISTS skill_combinations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS skill_combination_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			combination_id INTEGER NOT NULL,
			skill_id INTEGER NOT NULL,
			sort_order INTEGER DEFAULT 0,
			FOREIGN KEY (combination_id) REFERENCES skill_combinations(id) ON DELETE CASCADE,
			FOREIGN KEY (skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			UNIQUE(combination_id, skill_id)
		)`,

		// 预聚合表：按 (date, api_key_id, request_model) 维度聚合用量统计
		`CREATE TABLE IF NOT EXISTS usage_daily (
			date TEXT NOT NULL,
			api_key_id INTEGER,
			request_model TEXT NOT NULL DEFAULT '',
			prompt_tokens INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			cache_hit_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			requests INTEGER DEFAULT 0,
			cost REAL DEFAULT 0,
			PRIMARY KEY (date, api_key_id, request_model)
		)`,
	}

	// 迁移：添加 cache_hit_tokens 列（如果不存在）
	d.Exec(`ALTER TABLE usage_records ADD COLUMN cache_hit_tokens INTEGER DEFAULT 0`)
	// 迁移：添加 api_key_id 列（如果不存在）
	d.Exec(`ALTER TABLE usage_records ADD COLUMN api_key_id INTEGER DEFAULT NULL`)
	// 迁移：清理旧记录的 api_key_id=0（迁移默认值遗留下的无效数据，0 不对应任何有效密钥）
	d.Exec(`UPDATE usage_records SET api_key_id = NULL WHERE api_key_id = 0`)
	// 迁移：添加缓存读取/写入价格列（如果不存在）
	d.Exec(`ALTER TABLE models ADD COLUMN pricing_cache_read REAL DEFAULT 0`)
	d.Exec(`ALTER TABLE models ADD COLUMN pricing_cache_write REAL DEFAULT 0`)
	// 迁移：添加模型映射字段（如果不存在）
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN model_mappings TEXT DEFAULT '{}'`)
	// 迁移：添加全 MITM 和代理认证字段
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN mitm_all INTEGER DEFAULT 0`)
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN proxy_username TEXT DEFAULT ''`)
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN proxy_password TEXT DEFAULT ''`)
	// 迁移：添加 user_modified 字段到 models 表
	d.Exec(`ALTER TABLE models ADD COLUMN user_modified INTEGER DEFAULT 0`)
	// 迁移：添加 priority 字段到 channels 表（0=最高优先级，越大优先级越低）
	d.Exec(`ALTER TABLE channels ADD COLUMN priority INTEGER DEFAULT 99`)
	// 迁移：添加出站代理字段到 channels 表（每个渠道独立的代理开关）
	d.Exec(`ALTER TABLE channels ADD COLUMN use_proxy INTEGER DEFAULT 0`)
	// 迁移：添加全局出站代理配置到 proxy_config 表
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN forward_proxy_url TEXT DEFAULT ''`)
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN forward_proxy_user TEXT DEFAULT ''`)
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN forward_proxy_pass TEXT DEFAULT ''`)
	// 迁移：添加渠道熔断相关字段
	d.Exec(`ALTER TABLE channels ADD COLUMN failover_enabled INTEGER DEFAULT 1`)
	d.Exec(`ALTER TABLE channels ADD COLUMN test_model TEXT DEFAULT ''`)
	// 迁移：添加系统配置字段到 proxy_config（熔断探针 + 请求超时）
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN probe_api_key TEXT DEFAULT ''`)
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN request_timeout_seconds INTEGER DEFAULT 60`)
	// 迁移：添加全局熔断开关（默认启用）
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN failover_enabled INTEGER DEFAULT 1`)
	// 索引：加速 API Key 验证查询
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_api_keys_key ON api_keys(key)`)
	// 索引：加速按 API Key 过滤的统计查询
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_usage_api_key ON usage_records(api_key_id)`)
	// 迁移：添加 github_token 字段到 proxy_config（用于 MCP GitHub 导入认证）
	d.Exec(`ALTER TABLE proxy_config ADD COLUMN github_token TEXT DEFAULT ''`)
	// 迁移：添加 commit_sha 字段到 skills 表（用于 GitHub 更新追踪）
	d.Exec(`ALTER TABLE skills ADD COLUMN commit_sha TEXT DEFAULT ''`)
	// 索引：Skill 标签查询
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_skill_tags_skill ON skill_tags(skill_id)`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_skill_tags_tag ON skill_tags(tag)`)
	// 索引：Skill 文件查询
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_skill_files_skill ON skill_files(skill_id)`)
	// 索引：组合关联查询
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_combo_items_combo ON skill_combination_items(combination_id)`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_combo_items_skill ON skill_combination_items(skill_id)`)

	for _, q := range queries {
		if _, err := d.Exec(q); err != nil {
			log.Printf("[DB] 迁移查询失败: %v\n%s", err, q)
			return fmt.Errorf("迁移失败: %w", err)
		}
	}

	// 迁移：修复 usage_daily 日期为 0001-01-01 的错误数据（CreatedAt 为零值导致的 BUG）
	d.Exec(`DELETE FROM usage_daily WHERE date = '0001-01-01'`)

	// 从 usage_records 回填/修复 usage_daily（幂等 UPSERT，可安全重复执行）
	d.backfillUsageDaily()

	// 确保有默认代理配置
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM proxy_config").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := d.Exec(`INSERT INTO proxy_config (intercept_domains, smart_intercept_domains) VALUES ('[]', '[]')`)
		if err != nil {
			return fmt.Errorf("创建默认代理配置失败: %w", err)
		}
	}

	log.Println("[DB] 数据库迁移完成")
	return nil
}

// backfillUsageDaily 将已有 usage_records 数据回填到 usage_daily 预聚合表
// 幂等操作：INSERT OR IGNORE 确保不会重复插入已有行
func (d *DB) backfillUsageDaily() {
	var recordCount int64
	err := d.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&recordCount)
	if err != nil {
		log.Printf("[DB] 检查 usage_records 数据失败: %v", err)
		return
	}
	if recordCount == 0 {
		log.Println("[DB] usage_records 无数据，跳过回填")
		return
	}

	log.Printf("[DB] 开始回填 usage_daily，源数据 %d 条记录...", recordCount)
	result, err := d.Exec(`
		INSERT INTO usage_daily (date, api_key_id, request_model, prompt_tokens, completion_tokens, cache_hit_tokens, total_tokens, requests, cost)
		SELECT date(created_at), api_key_id, request_model,
		       SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_hit_tokens), SUM(total_tokens),
		       COUNT(*), SUM(cost)
		FROM usage_records
		GROUP BY date(created_at), api_key_id, request_model
		ON CONFLICT(date, api_key_id, request_model) DO UPDATE SET
			prompt_tokens = excluded.prompt_tokens,
			completion_tokens = excluded.completion_tokens,
			cache_hit_tokens = excluded.cache_hit_tokens,
			total_tokens = excluded.total_tokens,
			requests = excluded.requests,
			cost = excluded.cost`)
	if err != nil {
		log.Printf("[DB] 回填 usage_daily 失败: %v", err)
		return
	}
	affected, _ := result.RowsAffected()
	log.Printf("[DB] usage_daily 回填完成，写入 %d 行聚合数据", affected)
}
