// Package cpa 管理 CLIProxyAPI sidecar 进程。
//
// 架构：CLIProxyAPI 作为独立二进制运行（内核可单独更新），
// zero-api 只负责配置生成、进程生命周期管理和管理面板 UI，
// 不修改 CLIProxyAPI 任何代码。
package cpa

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager CLIProxyAPI 进程管理器
type Manager struct {
	dataDir  string // sidecar 数据目录（二进制/config/auths）
	binPath  string // CLIProxyAPI 二进制路径
	port     int    // sidecar 监听端口
	host     string // sidecar 绑定地址
	proxyURL string // 出站代理（下载/更新二进制时使用）

	mu           sync.Mutex
	cmd          *exec.Cmd
	running      bool
	closing      bool
	authCmd      *exec.Cmd
	authProvider string
	authOutput   bytes.Buffer
	authStarted  time.Time
	stopCh       chan struct{}
	doneCh       chan struct{}
}

// NewManager 创建进程管理器
func NewManager(dataDir string, host string, port int) *Manager {
	return &Manager{
		dataDir: dataDir,
		binPath: filepath.Join(dataDir, "CLIProxyAPI"),
		host:    host,
		port:    port,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// BinPath 返回二进制路径
func (m *Manager) BinPath() string { return m.binPath }

// UpdateEndpoint 更新 sidecar 的健康检查端点。
func (m *Manager) UpdateEndpoint(host string, port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if host != "" {
		m.host = host
	}
	if port > 0 {
		m.port = port
	}
}

// SetProxyURL 设置出站代理（下载/更新二进制时使用）。
func (m *Manager) SetProxyURL(proxyURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyURL = proxyURL
}

// ConfigPath 返回配置路径
func (m *Manager) ConfigPath() string { return filepath.Join(m.dataDir, "config.yaml") }

// AuthDir 返回认证目录
func (m *Manager) AuthDir() string { return filepath.Join(m.dataDir, "auths") }

// LogPath 返回日志路径
func (m *Manager) LogPath() string { return filepath.Join(m.dataDir, "cpa.log") }

// EnsureDirs 确保目录存在
func (m *Manager) EnsureDirs() error {
	for _, d := range []string{m.dataDir, m.AuthDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", d, err)
		}
	}
	return nil
}

// IsRunning 判断进程是否运行
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// BinExists 判断二进制是否存在
func (m *Manager) BinExists() bool {
	_, err := os.Stat(m.binPath)
	return err == nil
}

// Start 启动 CLIProxyAPI 进程（异步，不阻塞）
// 失败时返回错误；成功启动后由监控 goroutine 处理崩溃重启
func (m *Manager) Start() error {
	m.mu.Lock()
	m.closing = false
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("CLIProxyAPI 已在运行")
	}
	if !m.BinExists() {
		m.mu.Unlock()
		return fmt.Errorf("CLIProxyAPI 二进制不存在: %s（请先下载或放置二进制）", m.binPath)
	}
	if err := m.EnsureDirs(); err != nil {
		m.mu.Unlock()
		return err
	}

	// 打开日志文件
	logFile, err := os.OpenFile(m.LogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	cmd := exec.Command(m.binPath, "-config", m.ConfigPath())
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		m.mu.Unlock()
		return fmt.Errorf("启动 CLIProxyAPI 失败: %w", err)
	}

	m.cmd = cmd
	m.running = true
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	m.mu.Unlock()

	log.Printf("[CPA] CLIProxyAPI 已启动 (pid=%d, 端口=%d)", cmd.Process.Pid, m.port)

	// 监控 goroutine：等待进程退出 → 决定崩溃重启还是正常停止
	go m.watch(cmd, logFile)

	return nil
}

// watch 监控进程退出
func (m *Manager) watch(cmd *exec.Cmd, logFile *os.File) {
	defer logFile.Close()
	_ = cmd.Wait()

	m.mu.Lock()
	isRunning := m.running
	stopCh := m.stopCh
	closing := m.closing
	m.running = false
	m.cmd = nil
	m.mu.Unlock()
	close(m.doneCh)

	if !isRunning || closing {
		log.Printf("[CPA] CLIProxyAPI 进程已停止")
		return
	}

	// 检查是否主动停止
	select {
	case <-stopCh:
		log.Printf("[CPA] CLIProxyAPI 已正常停止")
	default:
		log.Printf("[CPA] ⚠️ CLIProxyAPI 异常退出，5 秒后自动重启...")
		time.Sleep(5 * time.Second)
		if err := m.Start(); err != nil {
			log.Printf("[CPA] 自动重启失败: %v", err)
		}
	}
}

// Stop 停止进程（等待退出）
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	cmd := m.cmd
	m.closing = true
	close(m.stopCh)
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}

	// 等待退出（最多 10 秒）
	select {
	case <-m.doneCh:
	case <-time.After(10 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-m.doneCh
	}
	log.Printf("[CPA] CLIProxyAPI 已停止")
	return nil
}

// Restart 重启进程
func (m *Manager) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return m.Start()
}

// Health 健康检查（HTTP GET /healthz）
func (m *Manager) Health() (bool, string) {
	url := fmt.Sprintf("http://%s:%d/healthz", m.host, m.port)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false, fmt.Sprintf("健康检查失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return true, "健康"
	}
	return false, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

// Status 汇总状态
func (m *Manager) Status() map[string]interface{} {
	status := map[string]interface{}{
		"running":     m.IsRunning(),
		"bin_exists":  m.BinExists(),
		"bin_path":    m.binPath,
		"config_path": m.ConfigPath(),
		"auth_dir":    m.AuthDir(),
		"port":        m.port,
		"host":        m.host,
		"log_path":    m.LogPath(),
	}
	if m.IsRunning() {
		ok, msg := m.Health()
		status["healthy"] = ok
		status["health_msg"] = msg
		m.mu.Lock()
		if m.cmd != nil && m.cmd.Process != nil {
			status["pid"] = m.cmd.Process.Pid
		}
		m.mu.Unlock()
	}
	// 版本信息（从二进制读取，不阻塞）
	status["version"] = m.BinVersion()
	return status
}

// BinVersion 读取二进制版本（通过 --version 标志，3 秒超时）
func (m *Manager) BinVersion() string {
	if !m.BinExists() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, m.binPath, "--version").CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "CLIProxyAPI Version:") {
			return strings.TrimSpace(line)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var loginFlags = map[string]string{
	"codex":       "-codex-login",
	"claude":      "-claude-login",
	"grok":        "-xai-login",
	"kimi":        "-kimi-login",
	"antigravity": "-antigravity-login",
}

// StartLogin 启动一个 CLIProxyAPI OAuth 登录流程。
func (m *Manager) StartLogin(provider string, device bool, noBrowser bool) error {
	flag, ok := loginFlags[provider]
	if !ok {
		return fmt.Errorf("不支持的订阅渠道: %s", provider)
	}
	if provider == "codex" && device {
		flag = "-codex-device-login"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.authCmd != nil && m.authCmd.Process != nil {
		return fmt.Errorf("已有 %s 登录流程正在运行", m.authProvider)
	}
	if !m.BinExists() {
		return fmt.Errorf("CLIProxyAPI 二进制不存在，请先安装")
	}
	if err := validateBinary(m.binPath); err != nil {
		return err
	}
	if err := m.EnsureDirs(); err != nil {
		return err
	}
	cmd := exec.Command(m.binPath, "-config", m.ConfigPath(), flag)
	if noBrowser {
		cmd.Args = append(cmd.Args, "-no-browser")
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建登录输出管道失败: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s 登录失败: %w", provider, err)
	}
	m.authCmd = cmd
	m.authProvider = provider
	m.authOutput.Reset()
	m.authStarted = time.Now()

	go func() {
		buffer := make([]byte, 4096)
		for {
			n, readErr := pipe.Read(buffer)
			if n > 0 {
				m.mu.Lock()
				m.authOutput.Write(buffer[:n])
				m.mu.Unlock()
			}
			if readErr != nil {
				break
			}
		}
		_ = cmd.Wait()
		m.mu.Lock()
		if m.authCmd == cmd {
			m.authCmd = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

// StopLogin 取消当前 OAuth 登录流程。
func (m *Manager) StopLogin() error {
	m.mu.Lock()
	cmd := m.authCmd
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("取消登录失败: %w", err)
	}
	return nil
}

// AuthStatus 返回登录进程和认证文件状态，不读取认证文件内容。
func (m *Manager) AuthStatus() map[string]interface{} {
	m.mu.Lock()
	authRunning := m.authCmd != nil && m.authCmd.Process != nil
	provider := m.authProvider
	output := m.authOutput.String()
	started := m.authStarted
	m.mu.Unlock()
	if len(output) > 12000 {
		output = output[len(output)-12000:]
	}
	entries, _ := os.ReadDir(m.AuthDir())
	authFiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			authFiles = append(authFiles, entry.Name())
		}
	}
	status := map[string]interface{}{
		"running":    authRunning,
		"provider":   provider,
		"output":     output,
		"auth_dir":   m.AuthDir(),
		"auth_files": authFiles,
	}
	if !started.IsZero() {
		status["started_at"] = started
	}
	return status
}
