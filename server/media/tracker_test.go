package media

import (
	"testing"
	"time"
)

func TestTracker(t *testing.T) {
	tracker := Tracker{SeekToleranceMs: 2000}
	start := time.Now()
	at := func(seconds float64) time.Time {
		return start.Add(time.Duration(seconds * float64(time.Second)))
	}
	observe := func(seconds float64, itemKey string, state string, posMs int64) (bool, bool) {
		return tracker.Observe(Observation{SessionKey: "s", ItemKey: itemKey, State: state, PositionMs: posMs}, at(seconds))
	}
	send := func(seconds float64) {
		tracker.Sent(&Activity{State: tracker.state, ElapsedDurationMs: tracker.EstimatedPositionMs(at(seconds))}, at(seconds))
	}

	if changed, shouldSend := observe(0, "a", "playing", 1000); !changed || !shouldSend {
		t.Fatal("expected first observation to be sent")
	}
	send(0)
	// Stale position report, estimated position keeps advancing
	if _, shouldSend := observe(2, "a", "playing", 1000); shouldSend {
		t.Error("expected no update without changes")
	}
	if pos := tracker.EstimatedPositionMs(at(2)); pos != 3000 {
		t.Errorf("expected estimated position 3000, got %d", pos)
	}
	// New report consistent with the estimate
	if _, shouldSend := observe(4, "a", "playing", 5000); shouldSend {
		t.Error("expected no update for a consistent position report")
	}
	// Heartbeat
	if _, shouldSend := observe(10, "a", "playing", 5000); !shouldSend {
		t.Error("expected heartbeat update")
	}
	send(10)
	// Seek
	if _, shouldSend := observe(12, "a", "playing", 60000); !shouldSend {
		t.Error("expected update on seek")
	}
	send(12)
	// Pause
	if _, shouldSend := observe(13, "a", "paused", 61000); !shouldSend {
		t.Error("expected update on state change")
	}
	send(13)
	if pos := tracker.EstimatedPositionMs(at(20)); pos != 61000 {
		t.Errorf("expected paused position to stay at 61000, got %d", pos)
	}
	// Item change
	if changed, shouldSend := observe(14, "b", "playing", 0); !changed || !shouldSend {
		t.Error("expected update on item change")
	}
	if tracker.Last() != nil {
		t.Error("expected last activity to be reset on item change")
	}
	send(14)
	stopped := tracker.Stop(at(15))
	if stopped == nil || stopped.State != "stopped" {
		t.Errorf("expected stopped activity, got %#v", stopped)
	}
	if tracker.Stop(at(16)) != nil {
		t.Error("expected nothing to stop after reset")
	}
}
