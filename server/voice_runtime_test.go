package yapyap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandleVoiceConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &YapYapConfig{
		VoiceEnabled:                true,
		VoiceSignalVersion:          "v1",
		VoiceMaxParticipantsPerRoom: 8,
		VoiceICEServers: []VoiceICEServerConfig{
			{
				URLs: []string{"stun:coturn:3478"},
			},
		},
	}
	s := &YapYap{
		Voice: NewVoiceRuntime(cfg),
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/voice/config", nil)

	s.HandleVoiceConfig(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response["protocol_version"] != "v1" {
		t.Fatalf("expected protocol_version v1, got %v", response["protocol_version"])
	}
	if response["ws_endpoint"] != "/ws/rtc" {
		t.Fatalf("expected ws_endpoint /ws/rtc, got %v", response["ws_endpoint"])
	}
}
