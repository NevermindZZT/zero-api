package handler

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type DatabaseHandler struct {
	db       *store.DB
	dbPath   string
}

func NewDatabaseHandler(db *store.DB, dbPath string) *DatabaseHandler {
	return &DatabaseHandler{db: db, dbPath: dbPath}
}

// Backup 导出数据库文件
func (h *DatabaseHandler) Backup(c *gin.Context) {
	// 先 checkpoint WAL，确保数据全部写入主数据库文件
	if _, err := h.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("[备份] WAL checkpoint 失败: %v", err)
	}

	// 获取文件信息
	info, err := os.Stat(h.dbPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "数据库文件不存在"})
		return
	}

	// 生成文件名
	filename := fmt.Sprintf("zero-api-backup-%s.db", time.Now().Format("20060102-150405"))

	// 设置响应头
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", fmt.Sprintf("%d", info.Size()))

	// 发送文件
	c.File(h.dbPath)
}

// Restore 从上传的文件恢复数据库
func (h *DatabaseHandler) Restore(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择数据库文件"})
		return
	}

	// 检查文件大小（最大 500MB）
	if file.Size > 500*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件大小不能超过 500MB"})
		return
	}

	// 读取上传文件的头部，验证是否为 SQLite 文件
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "打开上传文件失败"})
		return
	}
	header := make([]byte, 16)
	n, _ := io.ReadFull(src, header)
	src.Close()

	if n < 16 || string(header[:16]) != "SQLite format 3\x00" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不是有效的 SQLite 数据库文件"})
		return
	}

	// 保存上传文件到临时位置
	tmpPath := h.dbPath + ".restore.tmp"
	if err := c.SaveUploadedFile(file, tmpPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存临时文件失败"})
		return
	}

	// 验证临时文件可以作为 SQLite 打开
	tmpDB, err := store.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("数据库文件验证失败: %v", err)})
		return
	}
	tmpDB.Close()

	// checkpoint 当前数据库，确保 WAL 数据写入主文件
	h.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)

	// 关闭当前数据库连接
	h.db.Close()
	log.Printf("[恢复] 已关闭当前数据库连接")

	// 备份旧数据库
	backupPath := h.dbPath + ".old"
	os.Remove(backupPath) // 忽略错误
	if err := os.Rename(h.dbPath, backupPath); err != nil {
		log.Printf("[恢复] 备份旧数据库失败: %v", err)
		// 尝试恢复
		os.Rename(tmpPath, h.dbPath)
		h.reopenDB()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "备份旧数据库失败"})
		return
	}

	// 移动新数据库到目标位置
	if err := os.Rename(tmpPath, h.dbPath); err != nil {
		log.Printf("[恢复] 替换数据库文件失败: %v", err)
		// 尝试恢复
		os.Rename(backupPath, h.dbPath)
		h.reopenDB()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "替换数据库文件失败"})
		return
	}

	// 清理 WAL 和 SHM 文件（旧数据库的可能残留）
	os.Remove(h.dbPath + "-wal")
	os.Remove(h.dbPath + "-shm")

	// 重新打开数据库
	if err := h.reopenDB(); err != nil {
		// 尝试恢复旧数据库
		log.Printf("[恢复] 重新打开数据库失败: %v，尝试恢复旧数据库", err)
		os.Remove(h.dbPath)
		os.Rename(backupPath, h.dbPath)
		h.reopenDB()
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("恢复后数据库打开失败: %v", err)})
		return
	}

	log.Printf("[恢复] 数据库恢复成功，旧数据库备份于: %s", backupPath)
	c.JSON(http.StatusOK, gin.H{
		"message": "数据库恢复成功",
		"backup":  filepath.Base(backupPath),
	})
}

// reopenDB 重新打开数据库连接
func (h *DatabaseHandler) reopenDB() error {
	newDB, err := store.Open(h.dbPath)
	if err != nil {
		return err
	}
	*h.db = *newDB
	return nil
}
