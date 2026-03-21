package sync

import (
	gosync "sync"
)

// SyncEvent represents a structured sync log entry streamed via SSE.
type SyncEvent struct {
	Type   string `json:"type"`   // "sync_start", "book_matched", "book_unmatched", "progress_synced", "sync_complete", "error"
	Title  string `json:"title"`  // book title, if applicable
	Detail string `json:"detail"` // human-readable detail
}

const maxSubscribers = 10

// EventBus broadcasts sync events to multiple SSE subscribers.
type EventBus struct {
	mu          gosync.RWMutex
	subscribers map[chan SyncEvent]struct{}
	history     []SyncEvent // ring buffer of recent events
	maxHistory  int
}

// NewEventBus creates an EventBus that retains the last maxHistory events
// for new subscribers.
func NewEventBus(maxHistory int) *EventBus {
	return &EventBus{
		subscribers: make(map[chan SyncEvent]struct{}),
		maxHistory:  maxHistory,
	}
}

// Subscribe returns a channel that receives events. The channel is
// pre-filled with recent history. Caller must call Unsubscribe when done.
// Returns nil if the maximum subscriber count has been reached.
func (eb *EventBus) Subscribe() chan SyncEvent {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if len(eb.subscribers) >= maxSubscribers {
		return nil
	}

	ch := make(chan SyncEvent, 64)

	// Send history to new subscriber.
	for _, evt := range eb.history {
		select {
		case ch <- evt:
		default:
		}
	}

	eb.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (eb *EventBus) Unsubscribe(ch chan SyncEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.subscribers, ch)
	close(ch)
}

// Publish sends an event to all subscribers and appends to history.
// Non-blocking; drops if a subscriber's buffer is full.
func (eb *EventBus) Publish(e SyncEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	// Append to history, trim to maxHistory.
	eb.history = append(eb.history, e)
	if len(eb.history) > eb.maxHistory {
		eb.history = eb.history[len(eb.history)-eb.maxHistory:]
	}

	for ch := range eb.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

// ClearHistory resets the event history. Called at the start of a new sync.
func (eb *EventBus) ClearHistory() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.history = eb.history[:0]
}
