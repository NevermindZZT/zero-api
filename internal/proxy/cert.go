package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CertManager 管理根 CA 和动态域名证书
type CertManager struct {
	certDir      string
	caKey        *rsa.PrivateKey
	caCert       *x509.Certificate
	caCertPEM    []byte
	certCache    map[string]*tls.Certificate
	mu           sync.RWMutex
}

func NewCertManager(certDir string) *CertManager {
	return &CertManager{
		certDir:   certDir,
		certCache: make(map[string]*tls.Certificate),
	}
}

// Init 初始化根 CA（如果不存在则生成）
func (cm *CertManager) Init() error {
	os.MkdirAll(cm.certDir, 0755)

	keyPath := filepath.Join(cm.certDir, "root-ca-key.pem")
	certPath := filepath.Join(cm.certDir, "root-ca-cert.pem")
	crtPath := filepath.Join(cm.certDir, "root-ca-cert.crt")

	// 尝试加载已有 CA
	if _, err := os.Stat(keyPath); err == nil {
		if _, err := os.Stat(certPath); err == nil {
			return cm.loadCA(keyPath, certPath, crtPath)
		}
	}

	// 生成新的根 CA
	return cm.generateCA(keyPath, certPath, crtPath)
}

func (cm *CertManager) loadCA(keyPath, certPath, crtPath string) error {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("读取 CA 私钥失败: %w", err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("读取 CA 证书失败: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("解析 CA 私钥 PEM 失败")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err == nil {
		var ok bool
		cm.caKey, ok = parsedKey.(*rsa.PrivateKey)
		if !ok {
			return fmt.Errorf("CA 私钥不是 RSA 密钥")
		}
	} else {
		cm.caKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err != nil {
			return fmt.Errorf("解析 CA 私钥失败: %w", err)
		}
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return fmt.Errorf("解析 CA 证书 PEM 失败")
	}
	cm.caCert, err = x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return fmt.Errorf("解析 CA 证书失败: %w", err)
	}

	cm.caCertPEM = certPEM

	// 确保 .crt 文件存在（Windows 需要 .crt 扩展名安装）
	if _, err := os.Stat(crtPath); os.IsNotExist(err) {
		os.WriteFile(crtPath, certPEM, 0644)
	}

	return nil
}

func (cm *CertManager) generateCA(keyPath, certPath, crtPath string) error {
	// 生成 RSA 密钥对
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("生成 RSA 密钥失败: %w", err)
	}

	// 生成序列号
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("生成序列号失败: %w", err)
	}

	// 构建 CA 证书模板
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "zero-api Root CA",
			Organization: []string{"zero-api"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// 自签名
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("创建 CA 证书失败: %w", err)
	}

	// 解析证书
	caCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("解析 CA 证书失败: %w", err)
	}

	// 编码并保存
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	os.WriteFile(keyPath, keyPEM, 0644)
	os.WriteFile(certPath, certPEM, 0644)
	os.WriteFile(crtPath, certPEM, 0644)

	cm.caKey = key
	cm.caCert = caCert
	cm.caCertPEM = certPEM

	return nil
}

// GetRootCAPath 返回根 CA 证书路径
func (cm *CertManager) GetRootCAPath() string {
	return filepath.Join(cm.certDir, "root-ca-cert.pem")
}

// GetRootCACrtPath 返回 .crt 格式的根 CA 证书路径
func (cm *CertManager) GetRootCACrtPath() string {
	return filepath.Join(cm.certDir, "root-ca-cert.crt")
}

// GenerateCertForDomain 为指定域名生成 TLS 证书
func (cm *CertManager) GenerateCertForDomain(domain string) (*tls.Certificate, error) {
	cm.mu.RLock()
	if cached, ok := cm.certCache[domain]; ok {
		cm.mu.RUnlock()
		return cached, nil
	}
	cm.mu.RUnlock()

	// 生成域名密钥
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("生成域名密钥失败: %w", err)
	}

	// 生成序列号
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("生成序列号失败: %w", err)
	}

	// 计算 SubjectKeyIdentifier（使用 SPKI 的 SHA-1 哈希）
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("编码公钥失败: %w", err)
	}
	ski := sha1.Sum(pubDER)

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   domain,
			Organization: []string{"zero-api"},
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		SubjectKeyId:   ski[:],
		AuthorityKeyId: cm.caCert.SubjectKeyId,
		DNSNames:       []string{domain},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, cm.caCert, &key.PublicKey, cm.caKey)
	if err != nil {
		return nil, fmt.Errorf("创建域名证书失败: %w", err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER, cm.caCert.Raw},
		PrivateKey:  key,
	}

	cm.mu.Lock()
	cm.certCache[domain] = tlsCert
	cm.mu.Unlock()

	return tlsCert, nil
}
