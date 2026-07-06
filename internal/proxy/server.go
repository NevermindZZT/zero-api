package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/never/zero-api/internal/upstream"
)

// Server MITM 代理服务器
type Server struct {
	config        Config
	certMgr       *CertManager
	router        *RequestRouter
	adapter       *ProxyAdapter
	proxyAuthUser string
	proxyAuthPass string
}

// Config 代理配置
type Config struct {
	Host                  string
	Port                  int
	InterceptDomains      []string
	SmartInterceptDomains []string
	MitmAll               bool
	ProxyAuthUser         string
	ProxyAuthPass         string
}

func NewServer(cfg Config, certMgr *CertManager, router *RequestRouter, adapter *ProxyAdapter) *Server {
	return &Server{
		config:        cfg,
		certMgr:       certMgr,
		router:        router,
		adapter:       adapter,
		proxyAuthUser: cfg.ProxyAuthUser,
		proxyAuthPass: cfg.ProxyAuthPass,
	}
}

// UpdateAuth 热更新代理认证信息
func (s *Server) UpdateAuth(user, pass string) {
	s.proxyAuthUser = user
	s.proxyAuthPass = pass
}

// checkAuth 统一认证检查（HTTP 和 HTTPS 共用）
func (s *Server) checkAuth(r *http.Request) bool {
	if s.proxyAuthUser == "" {
		return true
	}
	auth := r.Header.Get("Proxy-Authorization")
	return checkProxyBasicAuth(auth, s.proxyAuthUser, s.proxyAuthPass)
}

func (s *Server) requireAuth(w http.ResponseWriter) {
	w.Header().Set("Proxy-Authenticate", "Basic realm=\"zero-api proxy\"")
	http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
}

// Start 启动代理服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	log.Printf("[代理] MITM 代理启动于 %s", addr)
	log.Printf("[代理] 拦截域名: %v", s.config.InterceptDomains)

	server := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("[代理] 收到请求: %s %s (来源: %s)", r.Method, r.URL.String(), r.RemoteAddr)
			if r.Method == http.MethodConnect {
				s.handleConnect(w, r)
			} else {
				s.handleHTTP(w, r)
			}
		}),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	return server.ListenAndServe()
}

// handleConnect 处理 HTTPS CONNECT 隧道请求
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	hostname := extractHostname(r.Host)
	clientAddr := r.RemoteAddr

	// ═══ 第一步：记录 CONNECT 请求 ═══
	log.Printf("[CONNECT] %s:%s (来源: %s)", hostname, r.URL.Port(), clientAddr)

	// ═══ 第二步：认证检查 ═══
	if !s.checkAuth(r) {
		log.Printf("[代理] 认证失败: %s (需要代理认证 %s:***)", clientAddr, s.proxyAuthUser)
		s.requireAuth(w)
		return
	}
	log.Printf("[代理] 认证通过: %s", clientAddr)

	// ═══ 第三步：判断是否 MITM 还是隧道 ═══
	if !s.router.ShouldMITM(hostname) {
		log.Printf("➡️  隧道透传: %s:%s", hostname, r.URL.Port())
		s.tunnelDirect(w, r)
		return
	}

	log.Printf("🔒 拦截 HTTPS 连接: %s:%s", hostname, r.URL.Port())

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("[代理] Hijack 失败: %v", err)
		return
	}
	defer clientConn.Close()

	// ═══ 第四步：发送 200 并建立 TLS ═══
	log.Printf("[MITM] 发送 200 Connection Established 并准备 TLS 握手...")
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	tlsCert, err := s.certMgr.GenerateCertForDomain(hostname)
	if err != nil {
		log.Printf("[MITM] 生成证书失败: %v", err)
		return
	}

	tlsConn := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
		NextProtos:   []string{"http/1.1"},
	})
	tlsConn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("[MITM] TLS 握手失败: %v", err)
		return
	}
	tlsConn.SetDeadline(time.Time{})
	cs := tlsConn.ConnectionState()
	log.Printf("[MITM] TLS 握手成功: %s (加密: %s, ALPN: %s, 版本: 0x%04X)",
		hostname, tls.CipherSuiteName(cs.CipherSuite), cs.NegotiatedProtocol, cs.Version)

	// ═══ 第五步：循环处理请求（HTTP Keep-Alive）═══
	reader := bufio.NewReader(tlsConn)
	for {
		// 读取一个 HTTP 请求
		req, err := http.ReadRequest(reader)
		if err != nil {
			log.Printf("[MITM] 读取请求结束: %v", err)
			break
		}

		bodyBytes, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			log.Printf("[MITM] 读取请求体失败: %v", err)
			break
		}

		headers := make(map[string]string)
		for k, v := range req.Header {
			headers[strings.ToLower(k)] = strings.Join(v, ", ")
		}

		log.Printf("")
		log.Printf("=== [MITM] %s https://%s%s ===", req.Method, hostname, req.URL.Path)
		if ct, ok := headers["content-type"]; ok {
			log.Printf("  Content-Type: %s", ct)
		}
		if cl, ok := headers["content-length"]; ok {
			log.Printf("  Content-Length: %s", cl)
		}
		if auth, ok := headers["authorization"]; ok {
			short := auth
			if len(auth) > 40 {
				short = auth[:20] + "..." + auth[len(auth)-20:]
			}
			log.Printf("  Authorization: %s", short)
		}
		if len(bodyBytes) > 0 {
			log.Printf("  Body: %d 字节", len(bodyBytes))
			if len(bodyBytes) < 500 {
				log.Printf("  Body内容: %s", string(bodyBytes))
			}
		}

		// 检查客户端是否要求关闭连接
		wantClose := strings.EqualFold(headers["connection"], "close")

		// ═══ 处理请求 ═══
		// 模型列表请求
		if req.Method == "GET" && isModelsPath(req.URL.Path) {
			log.Printf("[MITM] → 模型列表请求")
			status, respHeaders, respBody, err := s.adapter.HandleModelsRequest(headers)
			if err != nil {
				log.Printf("[MITM] ✗ 模型列表请求失败: %v", err)
				if status > 0 {
					writeHTTPResponse(tlsConn, status, err.Error())
				} else {
					writeHTTPResponse(tlsConn, 502, "Bad Gateway")
				}
				if wantClose {
					break
				}
				continue
			}
			log.Printf("  → 响应: %d (%d 字节, %d 个模型)", status, len(respBody), countModels(respBody))
			writeHTTPResponseWithHeaders(tlsConn, status, respHeaders, respBody)
			if wantClose {
				break
			}
			continue
		}

		// 非 LLM 请求透传
		if !s.router.ShouldIntercept(hostname) && !IsLLMRequest(req.Method, req.URL.Path, headers, bodyBytes) {
			log.Printf("➡️  [透传] 非 LLM 请求: %s %s", req.Method, req.URL.Path)
			s.forwardDirect(tlsConn, req, bodyBytes)
			break
		}

		// 检测是否为流式请求
		isStream := isStreamingRequest(bodyBytes)

		if isStream {
			log.Printf("🎯 [MITM] 拦截流式 LLM 请求 (模型: %s)", extractModel(bodyBytes))
			if err := s.adapter.HandleLLMStreamRequest(headers, bodyBytes, tlsConn); err != nil {
				log.Printf("[MITM] ✗ 流式 LLM 转发失败: %v", err)
			}
			break // 流式响应结束后关闭连接
		}

		// 非流式 LLM 请求处理
		log.Printf("🎯 [MITM] 拦截 LLM 请求 (模型: %s)", extractModel(bodyBytes))
		status, respHeaders, respBody, err := s.adapter.HandleLLMRequest(req.Method, req.URL.Path, headers, bodyBytes)
		if err != nil {
			log.Printf("[MITM] ✗ LLM 转发失败: %v", err)
			if status > 0 {
				writeHTTPResponse(tlsConn, status, err.Error())
			} else {
				writeHTTPResponse(tlsConn, 502, "Bad Gateway")
			}
			if wantClose {
				break
			}
			continue
		}

		log.Printf("  → 响应: %d (%d 字节)", status, len(respBody))
		if respHeaders != nil {
			if ct, ok := respHeaders["Content-Type"]; ok {
				log.Printf("  Content-Type: %s", ct)
			}
		}
		writeHTTPResponseWithHeaders(tlsConn, status, respHeaders, respBody)
		if wantClose {
			break
		}
	}
}

// handleHTTP 处理普通 HTTP 请求
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		s.requireAuth(w)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	headers := make(map[string]string)
	for k, v := range r.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}

	if IsLLMRequest(r.Method, r.URL.Path, headers, bodyBytes) {
		log.Printf("[代理] HTTP LLM 请求: %s (模型: %s)", r.URL.Path, extractModel(bodyBytes))
		status, respHeaders, respBody, err := s.adapter.HandleLLMRequest(r.Method, r.URL.Path, headers, bodyBytes)
		if err != nil {
			if status > 0 {
				http.Error(w, err.Error(), status)
			} else {
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
			}
			return
		}
		for k, v := range respHeaders {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write(respBody)
		return
	}

	s.forwardHTTP(w, r, bodyBytes)
}

// tunnelDirect 直接透传 CONNECT 隧道
// ★ 必须处理 Hijack 返回的 bufio.ReadWriter 中的缓冲数据（类似 ModelProxy 的 head）
func (s *Server) tunnelDirect(w http.ResponseWriter, r *http.Request) {
	log.Printf("[隧道] 正在连接 %s ...", r.Host)
	destConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		log.Printf("[隧道] 连接目标 %s 失败: %v", r.Host, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	log.Printf("[隧道] 连接 %s 成功", r.Host)
	defer destConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, rw, err := hijacker.Hijack()
	if err != nil {
		log.Printf("[隧道] Hijack 失败: %v", err)
		return
	}
	defer clientConn.Close()

	log.Printf("[隧道] 发送 200 Connection Established")
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// ★ 转发 Hijack 缓冲数据（类似 ModelProxy 的 head）
	headBuf := make([]byte, rw.Reader.Buffered())
	if len(headBuf) > 0 {
		rw.Reader.Read(headBuf)
		destConn.Write(headBuf)
		log.Printf("[隧道] 转发 %d 字节缓冲数据（head）到 %s", len(headBuf), r.Host)
	}

	log.Printf("[隧道] 开始双向数据传输: %s", r.Host)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		written, _ := io.Copy(destConn, clientConn)
		log.Printf("[隧道] 客户端→目标 传输完成: %d 字节", written)
	}()
	go func() {
		defer wg.Done()
		written, _ := io.Copy(clientConn, destConn)
		log.Printf("[隧道] 目标→客户端 传输完成: %d 字节", written)
	}()
	wg.Wait()
	log.Printf("[隧道] 连接关闭: %s", r.Host)
}

// forwardDirect 直接转发解密后的请求到上游（请求-响应模式）
// 用于非 LLM 请求的透传：读取完整上游响应后写回客户端并关闭
func (s *Server) forwardDirect(tlsConn net.Conn, req *http.Request, body []byte) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	destConn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		log.Printf("[代理] 连接上游 %s 失败: %v", host, err)
		writeHTTPResponse(tlsConn, 502, "Bad Gateway")
		tlsConn.Close()
		return
	}
	defer destConn.Close()

	tlsDest := tls.Client(destConn, &tls.Config{
		ServerName: extractHostname(host),
	})
	if err := tlsDest.Handshake(); err != nil {
		log.Printf("[代理] 上游 TLS 握手失败: %v", err)
		writeHTTPResponse(tlsConn, 502, "Bad Gateway")
		tlsConn.Close()
		return
	}

	// 发送请求到上游
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err := req.Write(tlsDest); err != nil {
		log.Printf("[代理] 发送请求到上游失败: %v", err)
		writeHTTPResponse(tlsConn, 502, "Bad Gateway")
		tlsConn.Close()
		return
	}

	// 读取完整的上游响应
	resp, err := http.ReadResponse(bufio.NewReader(tlsDest), req)
	if err != nil {
		log.Printf("[代理] 读取上游响应失败: %v", err)
		writeHTTPResponse(tlsConn, 502, "Bad Gateway")
		tlsConn.Close()
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[代理] 读取上游响应体失败: %v", err)
		writeHTTPResponse(tlsConn, 502, "Bad Gateway")
		tlsConn.Close()
		return
	}

	// 将响应写回客户端
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode)))
	for k, vals := range resp.Header {
		for _, v := range vals {
			kl := strings.ToLower(k)
			if kl == "transfer-encoding" || kl == "proxy-connection" {
				continue
			}
			sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
	}
	sb.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(respBody)))
	sb.WriteString("Connection: close\r\n")
	sb.WriteString("\r\n")
	tlsConn.Write([]byte(sb.String()))
	tlsConn.Write(respBody)

	// ★ 发送 TLS close_notify
	tlsConn.Close()
}

// forwardHTTP 透传普通 HTTP 请求
func (s *Server) forwardHTTP(w http.ResponseWriter, r *http.Request, body []byte) {
	client := upstream.NewHTTPClient()
	client.Timeout = 60 * time.Second
	upstreamReq, err := http.NewRequest(r.Method, r.URL.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	upstreamReq.Header = r.Header

	resp, err := client.Do(upstreamReq)
	if err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

func countModels(body []byte) int {
	var resp map[string]interface{}
	if json.Unmarshal(body, &resp) == nil {
		if data, ok := resp["data"].([]interface{}); ok {
			return len(data)
		}
	}
	return 0
}

func extractHostname(host string) string {
	host = strings.ToLower(host)
	if idx := strings.Index(host, ":"); idx != -1 {
		return host[:idx]
	}
	return host
}

func extractModel(body []byte) string {
	var parsed struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		return parsed.Model
	}
	return ""
}

func writeHTTPResponse(conn net.Conn, statusCode int, statusText string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		statusCode, http.StatusText(statusCode))
	conn.Write([]byte(resp))
}

func writeHTTPResponseWithHeaders(conn net.Conn, statusCode int, headers map[string]string, body []byte) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode)))
	for k, v := range headers {
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	sb.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	sb.WriteString("\r\n")
	conn.Write([]byte(sb.String()))
	conn.Write(body)
}

func bufioReader(conn net.Conn) *bufio.Reader {
	return bufio.NewReader(conn)
}

// isModelsPath 判断是否为模型列表请求路径
func isModelsPath(path string) bool {
	return path == "/v1/models" || path == "/models" || path == "/api/v1/models"
}

// isStreamingRequest 判断请求体是否要求流式响应
func isStreamingRequest(body []byte) bool {
	var parsed struct {
		Stream interface{} `json:"stream"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Stream != nil {
		switch v := parsed.Stream.(type) {
		case bool:
			return v
		case string:
			return v == "true"
		}
	}
	return false
}

// checkProxyBasicAuth 简单 Basic 认证验证
func checkProxyBasicAuth(authHeader, user, pass string) bool {
	if user == "" {
		return true // 未配置认证
	}
	if authHeader == "" || !strings.HasPrefix(authHeader, "Basic ") {
		return false
	}
	// 有些客户端会在 base64 后追加多余空白或换行
	encoded := strings.TrimSpace(authHeader[6:])
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return false
	}
	return parts[0] == user && parts[1] == pass
}
