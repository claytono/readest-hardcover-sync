package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventBus_SubscribePublish(t *testing.T) {
	eb := NewEventBus(10)
	ch := eb.Subscribe()

	evt := SyncEvent{Type: "test", Title: "hello"}
	eb.Publish(evt)

	select {
	case got := <-ch:
		assert.Equal(t, evt, got)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}

	eb.Unsubscribe(ch)
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	eb := NewEventBus(10)
	ch1 := eb.Subscribe()
	ch2 := eb.Subscribe()

	evt := SyncEvent{Type: "test"}
	eb.Publish(evt)

	got1 := <-ch1
	got2 := <-ch2
	assert.Equal(t, evt, got1)
	assert.Equal(t, evt, got2)

	eb.Unsubscribe(ch1)
	eb.Unsubscribe(ch2)
}

func TestEventBus_NonBlocking(t *testing.T) {
	eb := NewEventBus(10)
	ch := eb.Subscribe()

	// Fill the buffer (capacity 64).
	for i := 0; i < 100; i++ {
		eb.Publish(SyncEvent{Type: "flood"})
	}

	// Should not panic or block.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			goto done
		}
	}
done:
	assert.LessOrEqual(t, drained, 64)
	eb.Unsubscribe(ch)
}

func TestEventBus_History(t *testing.T) {
	eb := NewEventBus(5)

	for i := 0; i < 10; i++ {
		eb.Publish(SyncEvent{Type: "evt", Detail: string(rune('a' + i))})
	}

	// New subscriber should get the last 5 events.
	ch := eb.Subscribe()
	var events []SyncEvent
	for i := 0; i < 5; i++ {
		select {
		case e := <-ch:
			events = append(events, e)
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	}
	require.Len(t, events, 5)
	assert.Equal(t, "f", events[0].Detail)
	assert.Equal(t, "j", events[4].Detail)

	eb.Unsubscribe(ch)
}

func TestEventBus_SubscriberLimit(t *testing.T) {
	eb := NewEventBus(10)

	var channels []chan SyncEvent
	for i := 0; i < maxSubscribers; i++ {
		ch := eb.Subscribe()
		require.NotNil(t, ch, "subscriber %d should succeed", i)
		channels = append(channels, ch)
	}

	// Next subscribe should fail.
	ch := eb.Subscribe()
	assert.Nil(t, ch, "should return nil when max subscribers reached")

	// Unsubscribe one, then subscribe should succeed again.
	eb.Unsubscribe(channels[0])
	ch = eb.Subscribe()
	assert.NotNil(t, ch, "should succeed after unsubscribing one")
	eb.Unsubscribe(ch)

	for _, c := range channels[1:] {
		eb.Unsubscribe(c)
	}
}

func TestEventBus_ClearHistory(t *testing.T) {
	eb := NewEventBus(10)
	eb.Publish(SyncEvent{Type: "old"})
	eb.ClearHistory()

	ch := eb.Subscribe()
	// Should not receive the cleared event.
	select {
	case <-ch:
		t.Fatal("should not receive events after ClearHistory")
	default:
		// Good — nothing in buffer.
	}

	eb.Unsubscribe(ch)
}
