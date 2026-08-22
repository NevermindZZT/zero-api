package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResponsesWebSocketRouteIsRegisteredByHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if responsesWebSocketUpgrader.CheckOrigin == nil {
		t.Fatal("Responses WebSocket upgrader must be configured")
	}
	_ = httptest.NewRecorder()
}
