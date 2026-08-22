package cpa

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GitHubRelease CLIProxyAPI GitHub release 信息
type GitHubRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	PublishedAt string         `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

// ReleaseAsset release 资产
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

const latestReleaseURL = "https://api.github.com/repos/router-for-me/CLIProxyAPI/releases/latest"

// FetchLatestRelease 获取最新 release 信息
// proxyURL 可选出站代理（http/https/socks5，支持 user:pass@ 认证），空=直连
func FetchLatestRelease(proxyURL string) (*GitHubRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second, Transport: buildTransport(proxyURL)}
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zero-api-cpa-manager")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}
	var rel GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 release 失败: %w", err)
	}
	return &rel, nil
}

// findAsset 查找当前平台对应的资产文件。
func findAsset(assets []ReleaseAsset) *ReleaseAsset {
	platforms := platformNames()
	for i := range assets {
		name := normalizeAssetName(assets[i].Name)
		for _, platform := range platforms {
			if strings.Contains(name, "_"+platform+"_") || strings.HasSuffix(name, "_"+platform) {
				return &assets[i]
			}
		}
	}
	return nil
}

// platformNames 返回完整的 OS/架构组合，避免 linux_aarch64 被 linux 匹配。
func platformNames() []string {
	osName := strings.ToLower(runtime.GOOS)
	arch := strings.ToLower(runtime.GOARCH)
	variants := []string{osName + "_" + arch}
	if arch == "amd64" {
		variants = append(variants, osName+"_x86_64")
	}
	if arch == "arm64" {
		variants = append(variants, osName+"_aarch64")
	}
	return variants
}

func normalizeAssetName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	return name
}

// InstallBinary 下载并安装 CLIProxyAPI 二进制
// 返回安装的版本号
func (m *Manager) InstallBinary(force bool) (string, error) {
	rel, err := FetchLatestRelease(m.proxyURL)
	if err != nil {
		return "", err
	}

	asset := findAsset(rel.Assets)
	if asset == nil {
		return "", fmt.Errorf("未找到当前平台 (%s/%s) 的 release 资产", runtime.GOOS, runtime.GOARCH)
	}

	if m.BinExists() && !force {
		// 已存在，跳过（除非强制）
		log.Printf("[CPA] 二进制已存在，跳过安装 (tag=%s)", rel.TagName)
		return rel.TagName, nil
	}

	log.Printf("[CPA] 下载 CLIProxyAPI %s (%s, %d MB)...", rel.TagName, asset.Name, asset.Size/1024/1024)
	if err := m.EnsureDirs(); err != nil {
		return "", err
	}

	// 下载到临时文件
	tmpPath := m.binPath + ".tmp"
	if err := downloadFile(asset.BrowserDownloadURL, tmpPath, asset.Size, m.proxyURL); err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}

	// 先写入临时安装文件，成功后再替换线上二进制，避免留下半截文件。
	installPath := m.binPath + ".install"
	defer os.Remove(installPath)
	if err := extractBinary(tmpPath, installPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("解压失败: %w", err)
	}
	os.Remove(tmpPath)
	if err := os.Chmod(installPath, 0755); err != nil {
		return "", fmt.Errorf("设置二进制权限失败: %w", err)
	}
	if err := os.Rename(installPath, m.binPath); err != nil {
		return "", fmt.Errorf("替换二进制失败: %w", err)
	}

	log.Printf("[CPA] CLIProxyAPI 安装完成: %s", m.binPath)
	return rel.TagName, nil
}

// downloadFile 下载文件到指定路径（带进度日志）
// proxyURL 可选出站代理（http/https/socks5，支持 user:pass@ 认证），空=直连
// 失败自动重试 3 次（网络错误/5xx/超时），重试时支持 HTTP Range 断点续传。
func downloadFile(url, dst string, expectedSize int64, proxyURL string) error {
	const maxRetries = 3
	partialPath := dst + ".part"

	// 兼容旧版本留下的临时文件：旧 Windows 版本使用 CLIProxyAPI.tmp，
	// 新版本使用 CLIProxyAPI.exe.tmp.part，优先迁移旧文件继续下载。
	if err := migrateLegacyTempFile(partialPath); err != nil {
		log.Printf("[CPA] 迁移旧下载临时文件失败: %v", err)
	}

	// 兼容当前版本留下的 dst.tmp：将已有残留转为可续传文件。
	if _, err := os.Stat(dst); err == nil {
		if _, partialErr := os.Stat(partialPath); os.IsNotExist(partialErr) {
			if err := os.Rename(dst, partialPath); err != nil {
				_ = os.Remove(dst)
			}
		} else {
			_ = os.Remove(dst)
		}
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("[CPA] 下载重试 %d/%d...", attempt, maxRetries)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		lastErr = downloadOnce(url, partialPath, expectedSize, proxyURL)
		if lastErr == nil {
			if err := os.Rename(partialPath, dst); err != nil {
				lastErr = fmt.Errorf("保存下载文件失败: %w", err)
				break
			}
			return nil
		}
		log.Printf("[CPA] 下载失败(第 %d 次): %v", attempt, lastErr)
	}

	// 最终失败时不留下半文件，避免下一次被误判为已下载。
	_ = os.Remove(dst)
	_ = os.Remove(partialPath)
	return lastErr
}

// migrateLegacyTempFile 将旧版本使用的 CLIProxyAPI.tmp 迁移为当前临时下载路径。
func migrateLegacyTempFile(currentPath string) error {
	legacyPath := filepath.Join(filepath.Dir(currentPath), "CLIProxyAPI.tmp")
	if legacyPath == currentPath {
		return nil
	}
	if _, err := os.Stat(currentPath); err == nil {
		return nil
	}
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		return nil
	}
	return os.Rename(legacyPath, currentPath)
}

func downloadOnce(url, dst string, expectedSize int64, proxyURL string) error {
	client := &http.Client{Timeout: 10 * time.Minute, Transport: buildTransport(proxyURL)}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "zero-api-cpa-manager")

	var offset int64
	if info, statErr := os.Stat(dst); statErr == nil {
		offset = info.Size()
		if expectedSize > 0 && offset == expectedSize {
			return nil
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	appendMode := offset > 0 && resp.StatusCode == http.StatusPartialContent
	flags := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}
	out, err := os.OpenFile(dst, flags, 0600)
	if err != nil {
		return err
	}

	written, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	written += offset
	if expectedSize > 0 && written != expectedSize {
		return fmt.Errorf("下载大小不匹配: got %d want %d", written, expectedSize)
	}
	return nil
}

// buildTransport 构建 HTTP Transport，支持 http/https/socks5 代理（含账号密码认证）
func buildTransport(proxyURL string) *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// 与默认 Transport 一致的连接池参数
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if proxyURL != "" {
		if proxyURL == "direct" || proxyURL == "none" {
			transport.Proxy = nil
		} else if proxy, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(proxy)
		}
	}
	return transport
}

// extractBinary 从下载的归档中提取 CLIProxyAPI 二进制
// 支持 .tar.gz / .zip / 裸二进制
func extractBinary(src, dst string) error {
	// 尝试 zip。文件头已经表明是 ZIP 但归档损坏时，不能继续按裸二进制复制。
	if zr, err := zip.OpenReader(src); err == nil {
		defer zr.Close()
		return extractFromZip(zr, dst)
	} else if hasZipSignature(src) {
		return fmt.Errorf("ZIP 归档损坏: %w", err)
	}

	// 尝试 tar.gz
	if f, err := os.Open(src); err == nil {
		defer f.Close()
		gzr, err := gzip.NewReader(f)
		if err == nil {
			defer gzr.Close()
			tr := tar.NewReader(gzr)
			for {
				hdr, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
				name := filepath.Base(hdr.Name)
				if isCPAExecutable(name, hdr.Mode, hdr.Typeflag == tar.TypeReg) {
					return writeFileFromReader(dst, tr, hdr.Size)
				}
			}
			return fmt.Errorf("tar.gz 中未找到 CLIProxyAPI 可执行文件")
		} else if hasGzipSignature(src) {
			return fmt.Errorf("gzip 归档损坏: %w", err)
		}
	}

	// 裸二进制（直接复制）
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFileFromReader(dst, in, -1)
}

func hasZipSignature(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var sig [4]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil {
		return false
	}
	return sig[0] == 'P' && sig[1] == 'K' &&
		((sig[2] == 0x03 && sig[3] == 0x04) ||
			(sig[2] == 0x05 && sig[3] == 0x06) ||
			(sig[2] == 0x07 && sig[3] == 0x08))
}

func hasGzipSignature(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var sig [2]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil {
		return false
	}
	return sig[0] == 0x1f && sig[1] == 0x8b
}

// extractFromZip 从 zip 提取
func extractFromZip(zr *zip.ReadCloser, dst string) error {
	for _, f := range zr.File {
		info := f.FileInfo()
		if isCPAExecutable(filepath.Base(f.Name), int64(info.Mode()), !info.IsDir()) {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return writeFileFromReader(dst, rc, int64(f.UncompressedSize64))
		}
	}
	return fmt.Errorf("zip 中未找到 CLIProxyAPI 可执行文件")
}

// isCPAExecutable 匹配官方 release 的可执行文件名。
// 当前官方包使用 cli-proxy-api，旧版本可能使用 CLIProxyAPI。
func isCPAExecutable(name string, mode int64, regular bool) bool {
	if !regular {
		return false
	}
	base := strings.ToLower(filepath.Base(name))
	if base != "cli-proxy-api" && base != "cli-proxy-api.exe" && base != "cliproxyapi" && base != "cliproxyapi.exe" {
		return false
	}
	// Windows ZIP 不携带 Unix 执行权限位，.exe 文件名本身即可确认其类型。
	if strings.HasSuffix(base, ".exe") {
		return true
	}
	return mode&0111 != 0
}

// writeFileFromReader 从 reader 写文件（size<0 表示未知）
func writeFileFromReader(dst string, r io.Reader, size int64) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if size >= 0 {
		if _, err := io.CopyN(out, r, size); err != nil {
			return err
		}
		return nil
	}
	_, err = io.Copy(out, r)
	return err
}

// CheckUpdate 检查是否有新版本（对比本地二进制版本）
func (m *Manager) CheckUpdate() (latest string, hasUpdate bool, err error) {
	rel, err := FetchLatestRelease(m.proxyURL)
	if err != nil {
		return "", false, err
	}
	local := m.BinVersion()
	latest = rel.TagName
	// 简单比较：本地版本为空或不同则视为有更新
	if local == "" {
		return latest, true, nil
	}
	return latest, !strings.Contains(local, strings.TrimPrefix(rel.TagName, "v")), nil
}

func validateBinary(path string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("CLIProxyAPI 不是有效的 Linux ELF 可执行文件: %w", err)
	}
	defer f.Close()
	want := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		want = elf.EM_AARCH64
	}
	if f.Machine != want {
		return fmt.Errorf("CLIProxyAPI 二进制架构不匹配: 文件=%s, 当前系统=%s/%s，请强制重新下载正确平台版本", f.Machine, runtime.GOOS, runtime.GOARCH)
	}
	// 官方 release 使用 glibc 动态链接。Alpine/musl 没有该加载器，
	// exec 会返回误导性的 "no such file or directory"。
	if runtime.GOARCH == "amd64" {
		if _, err := os.Stat("/lib64/ld-linux-x86-64.so.2"); err != nil {
			if _, fallbackErr := os.Stat("/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2"); fallbackErr != nil {
				return fmt.Errorf("CLIProxyAPI 需要 glibc 动态加载器，但当前容器未提供 /lib64/ld-linux-x86-64.so.2；请使用 Debian/Ubuntu 镜像，不要使用 Alpine/musl")
			}
		}
	}
	if runtime.GOARCH == "arm64" {
		if _, err := os.Stat("/lib/ld-linux-aarch64.so.1"); err != nil {
			return fmt.Errorf("CLIProxyAPI 需要 glibc 动态加载器，但当前容器未提供 /lib/ld-linux-aarch64.so.1；请使用 Debian/Ubuntu 镜像，不要使用 Alpine/musl")
		}
	}
	return nil
}
