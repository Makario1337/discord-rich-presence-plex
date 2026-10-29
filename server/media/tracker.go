package media

import "time"

const (
	heartbeatInterval = 10 * time.Second
	seekThresholdMs   = 5000
)

// Tracker is used by polling-based sources to decide when to send activity updates: on item/state changes, on seeks, and periodically as a heartbeat (to keep the idle timeout from clearing the activity, while staying within Discord's rate limit)
type Tracker struct {
	// Extra tolerance for seek detection, usually the poll interval, since positions are observed with that much delay
	SeekToleranceMs int64

	sessionKey string
	itemKey    string
	state      string
	// Last position reported by the server, and the local time at which it was first observed
	reportedPosMs int64
	reportedAt    time.Time
	rate          float64
	// Last activity sent
	last       *Activity
	lastSentAt time.Time
}

type Observation struct {
	SessionKey string
	ItemKey    string
	State      string // playing, paused
	PositionMs int64
	Rate       float64 // Playback rate, 0 is treated as 1
}

// Observe records the current state of the tracked session and reports whether the item changed (so that cached metadata can be discarded) and whether an update should be sent
func (t *Tracker) Observe(o Observation, now time.Time) (itemChanged bool, send bool) {
	itemChanged = o.SessionKey != t.sessionKey || o.ItemKey != t.itemKey
	if itemChanged {
		*t = Tracker{SeekToleranceMs: t.SeekToleranceMs, sessionKey: o.SessionKey, itemKey: o.ItemKey}
	}
	rate := o.Rate
	if rate <= 0 {
		rate = 1
	}
	stateChanged := o.State != t.state
	newReport := itemChanged || stateChanged || o.PositionMs != t.reportedPosMs
	t.state = o.State
	t.rate = rate
	if newReport {
		t.reportedPosMs = o.PositionMs
		t.reportedAt = now
	}
	seeked := !itemChanged && !stateChanged && newReport && t.last != nil &&
		abs(o.PositionMs-t.predictedSentPosMs(now)) > seekThresholdMs+t.SeekToleranceMs
	heartbeat := now.Sub(t.lastSentAt) >= heartbeatInterval
	return itemChanged, itemChanged || stateChanged || seeked || heartbeat
}

func (t *Tracker) EstimatedPositionMs(now time.Time) int64 {
	if t.state != "playing" {
		return t.reportedPosMs
	}
	return t.reportedPosMs + int64(float64(now.Sub(t.reportedAt).Milliseconds())*t.rate)
}

func (t *Tracker) predictedSentPosMs(at time.Time) int64 {
	if t.last.State != "playing" {
		return t.last.ElapsedDurationMs
	}
	return t.last.ElapsedDurationMs + int64(float64(at.Sub(t.lastSentAt).Milliseconds())*t.rate)
}

// Last returns the last activity sent for the current item, or nil
func (t *Tracker) Last() *Activity {
	return t.last
}

// Sent records that the given activity was sent and returns a copy of it to pass to the callback
func (t *Tracker) Sent(activity *Activity, now time.Time) *Activity {
	t.last = activity
	t.lastSentAt = now
	sent := *activity
	return &sent
}

// Skipped records that the current item was ignored, so that it's not rebuilt until the next heartbeat
func (t *Tracker) Skipped(now time.Time) {
	t.lastSentAt = now
}

// Stop resets the tracker and returns a stopped copy of the last activity sent, or nil if there's nothing to stop
func (t *Tracker) Stop(now time.Time) *Activity {
	var stopped *Activity
	if t.last != nil && t.last.State != "stopped" {
		copied := *t.last
		copied.State = "stopped"
		copied.ElapsedDurationMs = t.EstimatedPositionMs(now)
		stopped = &copied
	}
	*t = Tracker{SeekToleranceMs: t.SeekToleranceMs}
	return stopped
}

// SessionKey returns the key of the session currently being tracked
func (t *Tracker) SessionKey() string {
	return t.sessionKey
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
