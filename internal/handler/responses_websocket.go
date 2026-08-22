package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/never/zero-api/internal/router"
	"github.com/never/zero-api/internal/store"
)

var responsesWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// ResponsesWebSocket performs an end-to-end Responses WebSocket proxy.
// The request and response frames are not converted or replayed locally.
func (h *ProxyHandler) ResponsesWebSocket(c *gin.Context) {
	clientConn, err := responsesWebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	messageType, body, err := clientConn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage {
		return
	}

	// Apply only the existing model-routing rules. The Responses payload itself
	// remains native; a virtual-model rule may replace only its top-level model.
	var requestModel struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &requestModel) != nil || strings.TrimSpace(requestModel.Model) == "" {
		writeResponsesWebSocketError(clientConn, http.StatusBadRequest, "Responses WebSocket request missing model")
		return
	}
	if result := router.ApplyRules(h.routers, &router.Context{
		Protocol:   "responses",
		RawBody:    body,
		Model:      requestModel.Model,
		ClientAuth: c.GetHeader("Authorization"),
	}); result != nil && result.NewBody != nil {
		body = result.NewBody
		_ = json.Unmarshal(body, &requestModel)
	}

	apiKey, err := h.resolveAndValidateAPIKey(c)
	if err != nil {
		writeResponsesWebSocketError(clientConn, http.StatusUnauthorized, err.Error())
		return
	}
	if err := h.checkAPIKeyModelAccess(apiKey, requestModel.Model); err != nil {
		writeResponsesWebSocketError(clientConn, http.StatusForbidden, err.Error())
		return
	}

	matchedModel, channel, err := h.findResponsesModel(requestModel.Model)
	if err != nil {
		writeResponsesWebSocketError(clientConn, http.StatusNotFound, err.Error())
		return
	}

	upstreamURL, err := responsesWebSocketURL(matchedModel.ProtocolURL("responses", channel.BaseURL))
	if err != nil {
		writeResponsesWebSocketError(clientConn, http.StatusBadGateway, err.Error())
		return
	}
	upstreamHeaders := responsesWebSocketHeaders(c.Request.Header)
	if channel.APIKey != "" {
		upstreamHeaders.Set("Authorization", "Bearer "+channel.APIKey)
	}
	upstreamConn, response, err := websocket.DefaultDialer.Dial(upstreamURL, upstreamHeaders)
	if err != nil {
		status := http.StatusBadGateway
		if response != nil && response.StatusCode >= 400 {
			status = response.StatusCode
		}
		writeResponsesWebSocketError(clientConn, status, "上游 Responses WebSocket 连接失败")
		return
	}
	defer upstreamConn.Close()

	if err := upstreamConn.WriteMessage(websocket.TextMessage, body); err != nil {
		return
	}
	proxyResponsesWebSocketFrames(clientConn, upstreamConn)
}

func (h *ProxyHandler) findResponsesModel(modelID string) (*store.Model, *store.Channel, error) {
	models, err := h.modelRepo.List(0)
	if err != nil {
		return nil, nil, fmt.Errorf("查询模型失败")
	}
	for i := range models {
		model := &models[i]
		if model.ModelID != modelID || model.Status != "active" {
			continue
		}
		channel, err := h.channelRepo.GetByID(model.ChannelID)
		if err != nil || channel.Status != "active" {
			continue
		}
		if model.SupportsProtocol("responses", channel.Type) {
			return model, channel, nil
		}
	}
	return nil, nil, fmt.Errorf("模型 %s 未找到或不支持 Responses", modelID)
}

func responsesWebSocketURL(raw string) (string, error) {
	if strings.HasPrefix(raw, "https://") {
		return "wss://" + strings.TrimPrefix(raw, "https://"), nil
	}
	if strings.HasPrefix(raw, "http://") {
		return "ws://" + strings.TrimPrefix(raw, "http://"), nil
	}
	if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
		return raw, nil
	}
	return "", fmt.Errorf("invalid Responses WebSocket URL: %s", raw)
}

func responsesWebSocketHeaders(src http.Header) http.Header {
	dst := make(http.Header)
	for name, values := range src {
		switch strings.ToLower(name) {
		case "authorization", "accept", "origin", "user-agent", "openai-beta",
			"openai-organization", "openai-project", "openai-conversation-id",
			"session_id", "session-id", "x-codex-session-id", "x-codex-turn-metadata",
			"x-codex-window-id", "conversation_id", "originator", "x-stainless-lang",
			"x-stainless-package-version":
			for _, value := range values {
				dst.Add(name, value)
			}
		}
	}
	return dst
}

func proxyResponsesWebSocketFrames(client, upstream *websocket.Conn) {
	var writeMu sync.Mutex
	copyFrames := func(dst, src *websocket.Conn, done chan<- struct{}) {
		defer close(done)
		for {
			messageType, payload, err := src.ReadMessage()
			if err != nil {
				return
			}
			writeMu.Lock()
			err = dst.WriteMessage(messageType, payload)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
	clientDone := make(chan struct{})
	upstreamDone := make(chan struct{})
	go copyFrames(upstream, client, clientDone)
	go copyFrames(client, upstream, upstreamDone)
	select {
	case <-clientDone:
	case <-upstreamDone:
	}
}

func writeResponsesWebSocketError(conn *websocket.Conn, status int, message string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "invalid_request_error",
			"message": message,
			"status":  status,
		},
	})
	_ = conn.WriteMessage(websocket.TextMessage, payload)
}
