package yapyap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
	authHandlers "yapyap/handlers"
	models "yapyap/models"
	"yapyap/voice"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type VoiceICEServerConfig struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type VoiceRuntime struct {
	Enabled                bool
	SignalVersion          string
	MaxParticipantsPerRoom int
	ICEServers             []voice.ICEServerConfig
	Manager                *voice.Manager

	mu      sync.RWMutex
	clients map[uint64]*RTCSignalClient
}

type RTCSignalClient struct {
	Conn   *websocket.Conn
	UserID uint64
	Send   chan []byte
}

func NewVoiceRuntime(cfg *YapYapConfig) *VoiceRuntime {
	voiceConfig := voice.Config{
		SignalVersion:          cfg.VoiceSignalVersion,
		MaxParticipantsPerRoom: cfg.VoiceMaxParticipantsPerRoom,
	}
	for _, ice := range cfg.VoiceICEServers {
		voiceConfig.ICEServers = append(voiceConfig.ICEServers, voice.ICEServerConfig{
			URLs:       append([]string(nil), ice.URLs...),
			Username:   ice.Username,
			Credential: ice.Credential,
		})
	}
	voiceConfig = voiceConfig.Normalized()

	runtime := &VoiceRuntime{
		Enabled:                cfg.VoiceEnabled,
		SignalVersion:          voiceConfig.SignalVersion,
		MaxParticipantsPerRoom: voiceConfig.MaxParticipantsPerRoom,
		ICEServers:             voiceConfig.ICEServers,
		clients:                make(map[uint64]*RTCSignalClient),
	}
	runtime.Manager = voice.NewManager(voiceConfig, runtime.sendSignal)
	return runtime
}

func (v *VoiceRuntime) sendSignal(userID uint64, msg voice.SignalMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	v.mu.RLock()
	client := v.clients[userID]
	v.mu.RUnlock()
	if client == nil {
		return
	}

	if !safeEnqueue(client.Send, data) {
		v.unregisterClient(userID, client.Conn)
	}
}

func safeEnqueue(ch chan []byte, data []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	select {
	case ch <- data:
		return true
	default:
		return false
	}
}

func safeClose(ch chan []byte) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}

func (v *VoiceRuntime) registerClient(client *RTCSignalClient) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if existing := v.clients[client.UserID]; existing != nil {
		safeClose(existing.Send)
		_ = existing.Conn.Close()
	}
	v.clients[client.UserID] = client
}

func (v *VoiceRuntime) unregisterClient(userID uint64, conn *websocket.Conn) {
	v.mu.Lock()
	defer v.mu.Unlock()

	client := v.clients[userID]
	if client == nil {
		return
	}
	if conn != nil && client.Conn != conn {
		return
	}

	delete(v.clients, userID)
	safeClose(client.Send)
	_ = client.Conn.Close()
}

func (v *VoiceRuntime) closeAllClients() {
	v.mu.Lock()
	defer v.mu.Unlock()

	for userID, client := range v.clients {
		delete(v.clients, userID)
		safeClose(client.Send)
		_ = client.Conn.Close()
	}
}

func (s *YapYap) HandleVoiceConfig(c *gin.Context) {
	if s.Voice == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Voice runtime not initialized"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"voice_enabled":              s.Voice.Enabled,
		"protocol_version":           s.Voice.SignalVersion,
		"ws_endpoint":                "/ws/rtc",
		"room_max_participants":      s.Voice.MaxParticipantsPerRoom,
		"ice_servers":                s.Voice.ICEServers,
		"plain_ws_only":              true,
		"note":                       "Use trusted networks for plain ws deployments",
		"minimum_supported_protocol": "v1",
	})
}

func (s *YapYap) HandleRTCWebSocket(c *gin.Context) {
	if s.Voice == nil || !s.Voice.Enabled {
		http.Error(c.Writer, "Voice chat disabled", http.StatusServiceUnavailable)
		return
	}

	token := c.Request.URL.Query().Get("token")
	if token == "" {
		token = authHandlers.ExtractTokenFromHeader(c.Request)
	}
	if token == "" {
		http.Error(c.Writer, "Authentication token required", http.StatusUnauthorized)
		return
	}

	claims, err := authHandlers.ValidateJWT(token, s.JWTSecret)
	if err != nil {
		http.Error(c.Writer, "Invalid authentication token", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("RTC websocket upgrade failed: %v", err)
		return
	}

	rtcClient := &RTCSignalClient{
		Conn:   conn,
		UserID: uint64(claims.UserID),
		Send:   make(chan []byte, 256),
	}
	s.Voice.registerClient(rtcClient)

	go s.handleRTCClientWrite(rtcClient)

	defer func() {
		s.Voice.unregisterClient(rtcClient.UserID, rtcClient.Conn)
		_ = s.Voice.Manager.Leave(rtcClient.UserID)
	}()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		msgType, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			if websocket.IsUnexpectedCloseError(readErr, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("RTC websocket read error for user %d: %v", rtcClient.UserID, readErr)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}

		msg, parseErr := voice.ParseSignalMessage(raw, s.Voice.SignalVersion)
		if parseErr != nil {
			s.sendRTCError(rtcClient.UserID, msg.ChannelID, msg.RequestID, "invalid_message", parseErr.Error())
			continue
		}

		if err := s.dispatchRTCMessage(rtcClient.UserID, msg); err != nil {
			code, text := mapVoiceError(err)
			s.sendRTCError(rtcClient.UserID, msg.ChannelID, msg.RequestID, code, text)
		}
	}
}

func (s *YapYap) handleRTCClientWrite(client *RTCSignalClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		_ = client.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("RTC websocket write error for user %d: %v", client.UserID, err)
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("RTC websocket ping error for user %d: %v", client.UserID, err)
				return
			}
		}
	}
}

func (s *YapYap) dispatchRTCMessage(userID uint64, msg voice.SignalMessage) error {
	switch msg.Type {
	case voice.EventVoiceJoin:
		if err := s.validateVoiceChannelJoin(userID, msg.ChannelID); err != nil {
			return err
		}
		state, err := s.Voice.Manager.Join(userID, msg.ChannelID)
		if err != nil {
			return err
		}
		s.sendRTCMessage(userID, voice.NewEvent(voice.EventVoiceJoined, msg.ChannelID, msg.RequestID, state))
		return nil
	case voice.EventVoiceLeave:
		if err := s.Voice.Manager.Leave(userID); err != nil && !errors.Is(err, voice.ErrNotInRoom) {
			return err
		}
		s.sendRTCMessage(userID, voice.NewEvent(voice.EventVoiceLeft, msg.ChannelID, msg.RequestID, map[string]string{"status": "ok"}))
		return nil
	case voice.EventWebRTCOffer:
		var payload voice.SDPPayload
		if err := decodeSignalPayload(msg.Payload, &payload); err != nil {
			return err
		}
		answer, err := s.Voice.Manager.HandleOffer(userID, msg.ChannelID, payload)
		if err != nil {
			return err
		}
		s.sendRTCMessage(userID, voice.NewEvent(voice.EventWebRTCAnswer, msg.ChannelID, msg.RequestID, answer))
		return nil
	case voice.EventWebRTCAnswer:
		var payload voice.SDPPayload
		if err := decodeSignalPayload(msg.Payload, &payload); err != nil {
			return err
		}
		return s.Voice.Manager.HandleAnswer(userID, msg.ChannelID, payload)
	case voice.EventWebRTCCandidate:
		var payload voice.ICECandidatePayload
		if err := decodeSignalPayload(msg.Payload, &payload); err != nil {
			return err
		}
		return s.Voice.Manager.HandleRemoteICECandidate(userID, msg.ChannelID, payload)
	case voice.EventVoiceMute:
		var payload voice.MutePayload
		if err := decodeSignalPayload(msg.Payload, &payload); err != nil {
			return err
		}
		return s.Voice.Manager.SetMuted(userID, msg.ChannelID, payload.Muted)
	default:
		return fmt.Errorf("unsupported signaling type: %s", msg.Type)
	}
}

func decodeSignalPayload(data json.RawMessage, out any) error {
	if len(data) == 0 {
		return errors.New("payload required")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}
	return nil
}

func (s *YapYap) validateVoiceChannelJoin(userID, channelID uint64) error {
	if channelID == 0 {
		return errors.New("channel_id is required")
	}

	var channel models.Channel
	if err := s.DB.First(&channel, channelID).Error; err != nil {
		return errors.New("voice channel not found")
	}
	if channel.Type != models.ChannelTypeVoice {
		return errors.New("channel is not a voice channel")
	}

	if s.HasGlobalFlag(userID, models.PERM_ADMINISTRATOR) {
		return nil
	}
	if !s.HasChannelFlag(userID, channelID, models.PERM_CONNECT) {
		return errors.New("permission denied for voice channel")
	}
	return nil
}

func (s *YapYap) sendRTCMessage(userID uint64, message voice.SignalMessage) {
	if s.Voice == nil {
		return
	}
	message.Version = s.Voice.SignalVersion
	s.Voice.sendSignal(userID, message)
}

func (s *YapYap) sendRTCError(userID, channelID uint64, requestID, code, message string) {
	s.sendRTCMessage(userID, voice.NewEvent(voice.EventVoiceError, channelID, requestID, voice.ErrorPayload{
		Code:    code,
		Message: message,
	}))
}

func mapVoiceError(err error) (string, string) {
	switch {
	case errors.Is(err, voice.ErrUserAlreadyInRoom):
		return "already_in_room", err.Error()
	case errors.Is(err, voice.ErrRoomFull):
		return "room_full", err.Error()
	case errors.Is(err, voice.ErrNotInRoom):
		return "not_in_room", err.Error()
	case errors.Is(err, voice.ErrChannelMismatch):
		return "channel_mismatch", err.Error()
	case errors.Is(err, voice.ErrInvalidSDP):
		return "invalid_sdp", err.Error()
	case errors.Is(err, voice.ErrInvalidCandidate):
		return "invalid_candidate", err.Error()
	case errors.Is(err, voice.ErrInvalidMessage):
		return "invalid_message", err.Error()
	case errors.Is(err, voice.ErrInvalidVersion):
		return "invalid_version", err.Error()
	case errors.Is(err, voice.ErrMissingType):
		return "missing_type", err.Error()
	default:
		return "internal_error", err.Error()
	}
}
