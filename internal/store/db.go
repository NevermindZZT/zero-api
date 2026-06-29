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
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("设置 PRAGMA 失败: %w", err)
		}
	}

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

	for _, q := range queries {
		if _, err := d.Exec(q); err != nil {
			log.Printf("[DB] 迁移查询失败: %v\n%s", err, q)
			return fmt.Errorf("迁移失败: %w", err)
		}
	}

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
