package voice

import (
	"sync"
	"testing"
)

func TestManagerConcurrentJoinLeave(t *testing.T) {
	mgr := NewManager(Config{
		SignalVersion:          "v1",
		MaxParticipantsPerRoom: 8,
	}, nil)

	userIDs := []uint64{1, 2, 3, 4, 5, 6, 7, 8}
	for _, uid := range userIDs {
		if _, err := mgr.Join(uid, 200); err != nil {
			t.Fatalf("initial join failed for %d: %v", uid, err)
		}
	}

	var wg sync.WaitGroup
	for _, uid := range userIDs {
		wg.Add(1)
		go func(userID uint64) {
			defer wg.Done()
			if err := mgr.Leave(userID); err != nil {
				t.Errorf("leave failed for %d: %v", userID, err)
			}
		}(uid)
	}
	wg.Wait()

	for _, uid := range userIDs {
		if _, err := mgr.Join(uid, 200); err != nil {
			t.Fatalf("rejoin failed for %d: %v", uid, err)
		}
	}
}
