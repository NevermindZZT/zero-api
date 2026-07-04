package handler

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/store"
)

// breakerState 渠道熔断状态
type breakerState int

const (
	breakerNormal   breakerState = iota // 正常
	breakerCooldown                     // 冷却中：完全跳过
	breakerProbing                      // 需探测：冷却到期，需测试通过才能恢复
)

// channelEntry 单个渠道的熔断记录
type channelEntry struct {
	state   breakerState
	expiry  time.Time // 冷却到期时间
	retries int       // 连续失败次数
}

// CircuitBreaker 渠道熔断器
//
// 工作流程：
//  1. 请求失败 → 冷却 5 分钟（cooldown 状态）
//  2. 冷却到期 → 转为 probing 状态
//  3. 命中 probing 渠道 → 先发轻量探测请求验证健康
//  4a. 探测通过 → 恢复正常（normal 状态），继续当前请求
//  4b. 探测失败 → 重新冷却
//
// 连续失败次数递增冷却时长：5min → 10min → 20min → 40min（上限 40min）
type CircuitBreaker struct {
	mu       sync.RWMutex
	entries  map[int64]*channelEntry
	baseWait time.Duration
	maxWait  time.Duration
	client   *http.Client
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		entries:  make(map[int64]*channelEntry),
		baseWait: 5 * time.Minute,
		maxWait:  40 * time.Minute,
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{},
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

// RecordFailure 记录渠道失败，进入冷却（指数退避）
func (cb *CircuitBreaker) RecordFailure(channelID int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	entry, ok := cb.entries[channelID]
	if !ok {
		entry = &channelEntry{}
		cb.entries[channelID] = entry
	}

	entry.retries++
	// 指数退避：baseWait * 2^(retries-1)，上限 maxWait
	wait := cb.baseWait
	for i := 1; i < entry.retries && wait < cb.maxWait; i++ {
		wait *= 2
	}
	if wait > cb.maxWait {
		wait = cb.maxWait
	}

	entry.state = breakerCooldown
	entry.expiry = time.Now().Add(wait)
	log.Printf("[熔断] 渠道 %d 请求失败，冷却 %v (连续失败 %d 次)", channelID, wait, entry.retries)
}

// RecordSuccess 记录渠道成功，立即恢复正常
func (cb *CircuitBreaker) RecordSuccess(channelID int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	delete(cb.entries, channelID)
	log.Printf("[熔断] 渠道 %d 请求成功，已恢复正常", channelID)
}

// MayProceed 检查渠道是否允许请求
// 返回值：allow=true 可继续，probe=true 需先探测
func (cb *CircuitBreaker) MayProceed(channelID int64) (allow bool, probe bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	entry, ok := cb.entries[channelID]
	if !ok {
		return true, false
	}

	switch entry.state {
	case breakerCooldown:
		if time.Now().Before(entry.expiry) {
			return false, false // 冷却中，跳过
		}
		return false, true // 冷却到期，需要探测
	case breakerProbing:
		return false, true // 需要探测
	default:
		return true, false
	}
}

// EnterProbing 将渠道转为探测状态
func (cb *CircuitBreaker) EnterProbing(channelID int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	entry, ok := cb.entries[channelID]
	if !ok {
		entry = &channelEntry{}
		cb.entries[channelID] = entry
	}
	entry.state = breakerProbing
}

// ProbeChannel 探测渠道健康状态
// 发真实 chat/completions 请求，验证端到端可用
func (cb *CircuitBreaker) ProbeChannel(ch *store.Channel, testModel, probeAPIKey, proxyURL, proxyUser, proxyPass string, timeout time.Duration) error {
	payload := map[string]interface{}{
		"model": testModel,
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
		"max_tokens": 1,
	}
	body, _ := json.Marshal(payload)

	adapt := adapter.NewAdapter(ch.Type)
	upstreamURL := adapt.GetChatURL(ch.BaseURL)

	req, err := http.NewRequest("POST", upstreamURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造探测请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+probeAPIKey)

	// 构建带可选代理的客户端
	var transport *http.Transport
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err == nil {
			if proxyUser != "" {
				u.User = url.UserPassword(proxyUser, proxyPass)
			}
			transport = &http.Transport{
				Proxy:               http.ProxyURL(u),
				TLSClientConfig:     &tls.Config{},
				TLSHandshakeTimeout: 10 * time.Second,
			}
		}
	}
	if transport == nil {
		transport = &http.Transport{
			TLSClientConfig:     &tls.Config{},
			TLSHandshakeTimeout: 10 * time.Second,
		}
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("探测请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("探测返回服务端错误: %d", resp.StatusCode)
	}
	return nil
}

// ProbeAndRecover 探测渠道并恢复
// 成功 → 恢复正常；失败 → 重新冷却
func (cb *CircuitBreaker) ProbeAndRecover(channelID int64, ch *store.Channel, testModel, probeAPIKey, proxyURL, proxyUser, proxyPass string, timeout time.Duration) error {
	err := cb.ProbeChannel(ch, testModel, probeAPIKey, proxyURL, proxyUser, proxyPass, timeout)
	if err == nil {
		cb.RecordSuccess(channelID)
		log.Printf("[熔断] 渠道 %s (%d) 探测通过，恢复正常", ch.Name, channelID)
		return nil
	}

	log.Printf("[熔断] 渠道 %s (%d) 探测失败: %v，重新冷却", ch.Name, channelID, err)
	cb.RecordFailure(channelID)
	return err
}
