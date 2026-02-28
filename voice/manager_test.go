package voice

import (
	"sync"
	"testing"
)

type outboundEvent struct {
	userID uint64
	msg    SignalMessage
}

type eventSink struct {
	mu     sync.Mutex
	events []outboundEvent
}

func (s *eventSink) push(userID uint64, msg SignalMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, outboundEvent{userID: userID, msg: msg})
}

func (s *eventSink) countByType(eventType string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, evt := range s.events {
		if evt.msg.Type == eventType {
			total++
		}
	}
	return total
}

func TestManagerJoinLeaveAndRoomState(t *testing.T) {
	sink := &eventSink{}
	mgr := NewManager(Config{
		SignalVersion:          "v1",
		MaxParticipantsPerRoom: 8,
	}, sink.push)

	state, err := mgr.Join(101, 20)
	if err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if state.ChannelID != 20 {
		t.Fatalf("expected channel 20, got %d", state.ChannelID)
	}
	if len(state.Participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(state.Participants))
	}

	state, err = mgr.Join(102, 20)
	if err != nil {
		t.Fatalf("second join failed: %v", err)
	}
	if len(state.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(state.Participants))
	}
	if sink.countByType(EventVoiceParticipantJoined) != 1 {
		t.Fatalf("expected 1 participant_joined event, got %d", sink.countByType(EventVoiceParticipantJoined))
	}

	if err := mgr.Leave(102); err != nil {
		t.Fatalf("leave failed: %v", err)
	}
	if sink.countByType(EventVoiceParticipantLeft) != 1 {
		t.Fatalf("expected 1 participant_left event, got %d", sink.countByType(EventVoiceParticipantLeft))
	}
}

func TestManagerRejectsMultipleRoomsPerUser(t *testing.T) {
	mgr := NewManager(Config{
		SignalVersion:          "v1",
		MaxParticipantsPerRoom: 8,
	}, nil)

	if _, err := mgr.Join(1001, 10); err != nil {
		t.Fatalf("first join failed: %v", err)
	}
	if _, err := mgr.Join(1001, 11); err == nil {
		t.Fatal("expected second join to fail")
	} else if err != ErrUserAlreadyInRoom {
		t.Fatalf("expected ErrUserAlreadyInRoom, got %v", err)
	}
}

func TestManagerRoomCapacityLimit(t *testing.T) {
	mgr := NewManager(Config{
		SignalVersion:          "v1",
		MaxParticipantsPerRoom: 2,
	}, nil)

	if _, err := mgr.Join(1, 99); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := mgr.Join(2, 99); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := mgr.Join(3, 99); err == nil {
		t.Fatal("expected room to be full")
	} else if err != ErrRoomFull {
		t.Fatalf("expected ErrRoomFull, got %v", err)
	}
}

func TestManagerMuteBroadcast(t *testing.T) {
	sink := &eventSink{}
	mgr := NewManager(Config{
		SignalVersion:          "v1",
		MaxParticipantsPerRoom: 8,
	}, sink.push)

	if _, err := mgr.Join(7, 77); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := mgr.Join(8, 77); err != nil {
		t.Fatalf("join failed: %v", err)
	}

	if err := mgr.SetMuted(7, 77, true); err != nil {
		t.Fatalf("set muted failed: %v", err)
	}
	if sink.countByType(EventVoiceParticipantUpdate) == 0 {
		t.Fatal("expected participant update event")
	}
}
