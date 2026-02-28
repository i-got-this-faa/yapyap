package voice

import (
	"encoding/json"
	"testing"
)

func TestParseSignalMessage_DefaultsVersion(t *testing.T) {
	raw := []byte(`{"type":"voice.join","channel_id":12}`)

	msg, err := ParseSignalMessage(raw, DefaultSignalVersion)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if msg.Version != DefaultSignalVersion {
		t.Fatalf("expected version %q, got %q", DefaultSignalVersion, msg.Version)
	}
	if msg.Type != EventVoiceJoin {
		t.Fatalf("expected type %q, got %q", EventVoiceJoin, msg.Type)
	}
}

func TestParseSignalMessage_InvalidVersion(t *testing.T) {
	raw := []byte(`{"version":"v0","type":"voice.join","channel_id":12}`)

	_, err := ParseSignalMessage(raw, DefaultSignalVersion)
	if err == nil {
		t.Fatal("expected invalid version error")
	}
	if err != ErrInvalidVersion {
		t.Fatalf("expected ErrInvalidVersion, got %v", err)
	}
}

func TestParseSignalMessage_MissingType(t *testing.T) {
	raw := []byte(`{"version":"v1","channel_id":12}`)

	_, err := ParseSignalMessage(raw, DefaultSignalVersion)
	if err == nil {
		t.Fatal("expected missing type error")
	}
	if err != ErrMissingType {
		t.Fatalf("expected ErrMissingType, got %v", err)
	}
}

func TestNewEvent_MarshalsPayload(t *testing.T) {
	msg := NewEvent(EventVoiceMute, 44, "req-1", MutePayload{Muted: true})
	if msg.Type != EventVoiceMute {
		t.Fatalf("unexpected type: %s", msg.Type)
	}
	var payload MutePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if !payload.Muted {
		t.Fatal("expected muted true")
	}
}
