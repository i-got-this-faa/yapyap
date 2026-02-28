package voice

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/pion/webrtc/v4"
)

var (
	ErrUserAlreadyInRoom = errors.New("user already in a voice room")
	ErrRoomFull          = errors.New("voice room is full")
	ErrNotInRoom         = errors.New("user is not in a voice room")
	ErrChannelMismatch   = errors.New("channel does not match active voice session")
	ErrInvalidSDP        = errors.New("invalid SDP payload")
	ErrInvalidCandidate  = errors.New("invalid ICE candidate payload")
)

type outboundSender func(userID uint64, msg SignalMessage)

type Manager struct {
	cfg    Config
	sender outboundSender

	mu       sync.RWMutex
	rooms    map[uint64]*room
	sessions map[uint64]*participant
}

type room struct {
	channelID       uint64
	participants    map[uint64]*participant
	publisherTracks map[uint64]*webrtc.TrackLocalStaticRTP
}

type participant struct {
	UserID    uint64
	ChannelID uint64

	muted atomic.Bool

	mu      sync.Mutex
	pc      *webrtc.PeerConnection
	senders map[uint64]*webrtc.RTPSender // publisher user ID -> sender
}

func NewManager(cfg Config, sender outboundSender) *Manager {
	normalized := cfg.Normalized()
	return &Manager{
		cfg:      normalized,
		sender:   sender,
		rooms:    make(map[uint64]*room),
		sessions: make(map[uint64]*participant),
	}
}

func (m *Manager) Config() Config {
	return m.cfg
}

func (m *Manager) Join(userID, channelID uint64) (RoomState, error) {
	m.mu.Lock()
	if _, exists := m.sessions[userID]; exists {
		m.mu.Unlock()
		return RoomState{}, ErrUserAlreadyInRoom
	}

	r, ok := m.rooms[channelID]
	if !ok {
		r = &room{
			channelID:       channelID,
			participants:    make(map[uint64]*participant),
			publisherTracks: make(map[uint64]*webrtc.TrackLocalStaticRTP),
		}
		m.rooms[channelID] = r
	}

	if len(r.participants) >= m.cfg.MaxParticipantsPerRoom {
		m.mu.Unlock()
		return RoomState{}, ErrRoomFull
	}

	p := &participant{
		UserID:    userID,
		ChannelID: channelID,
		senders:   make(map[uint64]*webrtc.RTPSender),
	}
	r.participants[userID] = p
	m.sessions[userID] = p

	state := m.roomStateLocked(r)
	otherIDs := roomParticipantIDs(r, userID)
	m.mu.Unlock()

	joined := ParticipantState{
		UserID:   userID,
		Muted:    false,
		Speaking: false,
	}
	for _, otherID := range otherIDs {
		m.emit(otherID, EventVoiceParticipantJoined, channelID, "", joined)
	}

	return state, nil
}

func (m *Manager) Leave(userID uint64) error {
	var (
		channelID uint64
		p         *participant
		r         *room
		others    []*participant
	)

	m.mu.Lock()
	p = m.sessions[userID]
	if p == nil {
		m.mu.Unlock()
		return ErrNotInRoom
	}
	channelID = p.ChannelID
	r = m.rooms[channelID]

	delete(m.sessions, userID)
	if r != nil {
		delete(r.participants, userID)
		delete(r.publisherTracks, userID)
		for _, other := range r.participants {
			others = append(others, other)
		}
		if len(r.participants) == 0 {
			delete(m.rooms, channelID)
		}
	}
	m.mu.Unlock()

	p.mu.Lock()
	if p.pc != nil {
		_ = p.pc.Close()
		p.pc = nil
	}
	p.mu.Unlock()

	for _, other := range others {
		removed := m.removePublisherFromParticipant(other, userID)
		if removed {
			m.renegotiate(other)
		}
	}

	left := ParticipantState{
		UserID:   userID,
		Muted:    p.muted.Load(),
		Speaking: false,
	}
	for _, other := range others {
		m.emit(other.UserID, EventVoiceParticipantLeft, channelID, "", left)
	}

	return nil
}

func (m *Manager) HandleOffer(userID, channelID uint64, offer SDPPayload) (SDPPayload, error) {
	if offer.SDP == "" {
		return SDPPayload{}, ErrInvalidSDP
	}

	p, _, err := m.getParticipant(userID, channelID)
	if err != nil {
		return SDPPayload{}, err
	}

	pc, err := m.ensurePeerConnection(p)
	if err != nil {
		return SDPPayload{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer.SDP,
	}); err != nil {
		return SDPPayload{}, fmt.Errorf("set remote offer: %w", err)
	}

	publisherTracks := m.snapshotPublisherTracks(p.ChannelID)
	for publisherID, track := range publisherTracks {
		if publisherID == userID {
			continue
		}
		if _, exists := p.senders[publisherID]; exists {
			continue
		}
		sender, addErr := pc.AddTrack(track)
		if addErr != nil {
			continue
		}
		p.senders[publisherID] = sender
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return SDPPayload{}, fmt.Errorf("create answer: %w", err)
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return SDPPayload{}, fmt.Errorf("set local answer: %w", err)
	}
	local := pc.LocalDescription()
	if local == nil {
		return SDPPayload{}, ErrInvalidSDP
	}
	return SDPPayload{
		Type: local.Type.String(),
		SDP:  local.SDP,
	}, nil
}

func (m *Manager) snapshotPublisherTracks(channelID uint64) map[uint64]*webrtc.TrackLocalStaticRTP {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r := m.rooms[channelID]
	if r == nil {
		return map[uint64]*webrtc.TrackLocalStaticRTP{}
	}

	out := make(map[uint64]*webrtc.TrackLocalStaticRTP, len(r.publisherTracks))
	for userID, track := range r.publisherTracks {
		out[userID] = track
	}
	return out
}

func (m *Manager) HandleAnswer(userID, channelID uint64, answer SDPPayload) error {
	if answer.SDP == "" {
		return ErrInvalidSDP
	}
	p, _, err := m.getParticipant(userID, channelID)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pc == nil {
		return ErrInvalidSDP
	}

	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answer.SDP,
	}); err != nil {
		return fmt.Errorf("set remote answer: %w", err)
	}
	return nil
}

func (m *Manager) HandleRemoteICECandidate(userID, channelID uint64, candidate ICECandidatePayload) error {
	if candidate.Candidate == "" {
		return ErrInvalidCandidate
	}
	p, _, err := m.getParticipant(userID, channelID)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pc == nil {
		return ErrNotInRoom
	}

	if err := p.pc.AddICECandidate(webrtc.ICECandidateInit{
		Candidate:     candidate.Candidate,
		SDPMid:        candidate.SDPMid,
		SDPMLineIndex: candidate.SDPMLineIndex,
	}); err != nil {
		return fmt.Errorf("add ICE candidate: %w", err)
	}
	return nil
}

func (m *Manager) SetMuted(userID, channelID uint64, muted bool) error {
	p, _, err := m.getParticipant(userID, channelID)
	if err != nil {
		return err
	}
	p.muted.Store(muted)
	m.broadcastParticipantUpdate(p)
	return nil
}

func (m *Manager) RoomState(userID uint64) (RoomState, error) {
	m.mu.RLock()
	p := m.sessions[userID]
	if p == nil {
		m.mu.RUnlock()
		return RoomState{}, ErrNotInRoom
	}
	r := m.rooms[p.ChannelID]
	if r == nil {
		m.mu.RUnlock()
		return RoomState{}, ErrNotInRoom
	}
	state := m.roomStateLocked(r)
	m.mu.RUnlock()
	return state, nil
}

func (m *Manager) getParticipant(userID, channelID uint64) (*participant, *room, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p := m.sessions[userID]
	if p == nil {
		return nil, nil, ErrNotInRoom
	}
	if channelID != 0 && p.ChannelID != channelID {
		return nil, nil, ErrChannelMismatch
	}
	r := m.rooms[p.ChannelID]
	if r == nil {
		return nil, nil, ErrNotInRoom
	}
	return p, r, nil
}

func (m *Manager) ensurePeerConnection(p *participant) (*webrtc.PeerConnection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pc != nil {
		return p.pc, nil
	}

	iceServers := make([]webrtc.ICEServer, 0, len(m.cfg.ICEServers))
	for _, ice := range m.cfg.ICEServers {
		iceServers = append(iceServers, webrtc.ICEServer{
			URLs:       ice.URLs,
			Username:   ice.Username,
			Credential: ice.Credential,
		})
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: iceServers,
	})
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			m.emit(p.UserID, EventWebRTCEndOfCands, p.ChannelID, "", map[string]string{"status": "complete"})
			return
		}
		candidate := c.ToJSON()
		m.emit(p.UserID, EventWebRTCCandidate, p.ChannelID, "", ICECandidatePayload{
			Candidate:     candidate.Candidate,
			SDPMid:        candidate.SDPMid,
			SDPMLineIndex: candidate.SDPMLineIndex,
		})
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateDisconnected {
			_ = m.Leave(p.UserID)
		}
	})

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track == nil || track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		go m.handleIncomingTrack(p, track)
	})

	p.pc = pc
	return pc, nil
}

func (m *Manager) handleIncomingTrack(publisher *participant, remoteTrack *webrtc.TrackRemote) {
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		remoteTrack.Codec().RTPCodecCapability,
		fmt.Sprintf("audio-%d", publisher.UserID),
		fmt.Sprintf("voice-%d", publisher.ChannelID),
	)
	if err != nil {
		return
	}

	var subscribers []*participant

	m.mu.Lock()
	r := m.rooms[publisher.ChannelID]
	if r == nil {
		m.mu.Unlock()
		return
	}
	r.publisherTracks[publisher.UserID] = localTrack
	for _, sub := range r.participants {
		if sub.UserID != publisher.UserID {
			subscribers = append(subscribers, sub)
		}
	}
	m.mu.Unlock()

	for _, sub := range subscribers {
		added := m.attachPublisherTrack(sub, publisher.UserID, localTrack)
		if added {
			m.renegotiate(sub)
		}
	}

	for {
		packet, _, readErr := remoteTrack.ReadRTP()
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return
			}
			return
		}
		if publisher.muted.Load() {
			continue
		}
		if writeErr := localTrack.WriteRTP(packet); writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
			return
		}
	}
}

func (m *Manager) attachPublisherTrack(subscriber *participant, publisherID uint64, track *webrtc.TrackLocalStaticRTP) bool {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()

	if subscriber.pc == nil {
		return false
	}
	if _, exists := subscriber.senders[publisherID]; exists {
		return false
	}
	sender, err := subscriber.pc.AddTrack(track)
	if err != nil {
		return false
	}
	subscriber.senders[publisherID] = sender
	return true
}

func (m *Manager) removePublisherFromParticipant(subscriber *participant, publisherID uint64) bool {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()

	if subscriber.pc == nil {
		return false
	}
	sender, exists := subscriber.senders[publisherID]
	if !exists {
		return false
	}
	delete(subscriber.senders, publisherID)
	if err := subscriber.pc.RemoveTrack(sender); err != nil {
		return false
	}
	return true
}

func (m *Manager) renegotiate(p *participant) {
	p.mu.Lock()
	if p.pc == nil || p.pc.RemoteDescription() == nil {
		p.mu.Unlock()
		return
	}

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		p.mu.Unlock()
		return
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.mu.Unlock()
		return
	}
	local := p.pc.LocalDescription()
	p.mu.Unlock()

	if local == nil {
		return
	}
	m.emit(p.UserID, EventWebRTCOffer, p.ChannelID, "", SDPPayload{
		Type: local.Type.String(),
		SDP:  local.SDP,
	})
}

func (m *Manager) broadcastParticipantUpdate(p *participant) {
	m.mu.RLock()
	r := m.rooms[p.ChannelID]
	if r == nil {
		m.mu.RUnlock()
		return
	}
	recipientIDs := roomParticipantIDs(r, 0)
	update := ParticipantState{
		UserID:   p.UserID,
		Muted:    p.muted.Load(),
		Speaking: false,
	}
	m.mu.RUnlock()

	for _, recipientID := range recipientIDs {
		m.emit(recipientID, EventVoiceParticipantUpdate, p.ChannelID, "", update)
	}
}

func (m *Manager) roomStateLocked(r *room) RoomState {
	participants := make([]ParticipantState, 0, len(r.participants))
	userIDs := make([]uint64, 0, len(r.participants))
	for userID := range r.participants {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })

	for _, userID := range userIDs {
		p := r.participants[userID]
		participants = append(participants, ParticipantState{
			UserID:   p.UserID,
			Muted:    p.muted.Load(),
			Speaking: false,
		})
	}

	return RoomState{
		ChannelID:    r.channelID,
		Participants: participants,
	}
}

func roomParticipantIDs(r *room, exceptUserID uint64) []uint64 {
	out := make([]uint64, 0, len(r.participants))
	for uid := range r.participants {
		if exceptUserID != 0 && uid == exceptUserID {
			continue
		}
		out = append(out, uid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m *Manager) emit(userID uint64, eventType string, channelID uint64, requestID string, payload any) {
	if m.sender == nil {
		return
	}
	msg := NewEvent(eventType, channelID, requestID, payload)
	msg.Version = m.cfg.SignalVersion
	m.sender(userID, msg)
}
