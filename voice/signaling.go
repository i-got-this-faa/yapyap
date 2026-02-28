package voice

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	DefaultSignalVersion = "v1"

	EventVoiceJoin              = "voice.join"
	EventVoiceJoined            = "voice.joined"
	EventVoiceLeave             = "voice.leave"
	EventVoiceLeft              = "voice.left"
	EventVoiceRoomState         = "voice.room_state"
	EventVoiceParticipantJoined = "voice.participant_joined"
	EventVoiceParticipantLeft   = "voice.participant_left"
	EventVoiceParticipantUpdate = "voice.participant_updated"
	EventVoiceError             = "voice.error"

	EventWebRTCOffer      = "webrtc.offer"
	EventWebRTCAnswer     = "webrtc.answer"
	EventWebRTCCandidate  = "webrtc.ice_candidate"
	EventWebRTCEndOfCands = "webrtc.end_of_candidates"
	EventVoiceMute        = "voice.mute"
)

var (
	ErrInvalidMessage = errors.New("invalid signaling message")
	ErrInvalidVersion = errors.New("unsupported signaling version")
	ErrMissingType    = errors.New("missing signaling message type")
)

// SignalMessage is the v1 signaling envelope used on /ws/rtc.
type SignalMessage struct {
	Version   string          `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	ChannelID uint64          `json:"channel_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// ParticipantState is sent to clients as room state metadata.
type ParticipantState struct {
	UserID   uint64 `json:"user_id"`
	Muted    bool   `json:"muted"`
	Speaking bool   `json:"speaking"`
}

// RoomState is emitted in voice.joined and voice.room_state.
type RoomState struct {
	ChannelID    uint64             `json:"channel_id"`
	Participants []ParticipantState `json:"participants"`
}

// SDPPayload is the payload for webrtc.offer / webrtc.answer.
type SDPPayload struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// ICECandidatePayload is the payload for trickle ICE candidate exchange.
type ICECandidatePayload struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdp_mid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdp_mline_index,omitempty"`
}

// MutePayload allows clients to toggle local muted state in room metadata.
type MutePayload struct {
	Muted bool `json:"muted"`
}

// ErrorPayload is emitted in voice.error.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ParseSignalMessage(data []byte, expectedVersion string) (SignalMessage, error) {
	var msg SignalMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return SignalMessage{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if msg.Type == "" {
		return SignalMessage{}, ErrMissingType
	}

	version := msg.Version
	if version == "" {
		version = DefaultSignalVersion
	}
	if expectedVersion == "" {
		expectedVersion = DefaultSignalVersion
	}
	if version != expectedVersion {
		return SignalMessage{}, ErrInvalidVersion
	}
	msg.Version = version

	return msg, nil
}

func NewEvent(eventType string, channelID uint64, requestID string, payload any) SignalMessage {
	return SignalMessage{
		Version:   DefaultSignalVersion,
		Type:      eventType,
		RequestID: requestID,
		ChannelID: channelID,
		Payload:   mustMarshalPayload(payload),
	}
}

func mustMarshalPayload(payload any) json.RawMessage {
	if payload == nil {
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return b
}
