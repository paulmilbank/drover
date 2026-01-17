// Package events provides real-time event streaming for task lifecycle events
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// Bus manages event streaming and subscription
type Bus struct {
	mu         sync.RWMutex
	subscribers map[chan *Event]string
	closed     atomic.Bool
}

// NewBus creates a new event bus
func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[chan *Event]string),
	}
}

// Subscribe creates a new subscription channel for events
func (b *Bus) Subscribe(name string) chan *Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan *Event, 100)
	b.subscribers[ch] = name
	return ch
}

// Unsubscribe removes a subscription channel
func (b *Bus) Unsubscribe(ch chan *Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.subscribers, ch)
}

// Publish emits an event to all subscribers
func (b *Bus) Publish(ctx context.Context, event *Event) error {
	if b.closed.Load() {
		return fmt.Errorf("event bus is closed")
	}

	// Generate event ID if not set
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subscribers {
		select {
		case ch <- event:
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Channel is full, skip this subscriber
			// This prevents blocking on slow consumers
		}
	}

	return nil
}

// Close shuts down the event bus
func (b *Bus) Close() error {
	b.closed.Store(true)

	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subscribers {
		close(ch)
		delete(b.subscribers, ch)
	}

	return nil
}

// SubscriberCount returns the number of active subscribers
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Streamer handles streaming events to clients
type Streamer struct {
	bus    *Bus
	filter EventFilter
	ch     chan *Event
	closed atomic.Bool
}

// NewStreamer creates a new event streamer with the given filter
func NewStreamer(bus *Bus, filter EventFilter) *Streamer {
	return &Streamer{
		bus:    bus,
		filter: filter,
	}
}

// Start begins streaming events to the returned channel
func (s *Streamer) Start(ctx context.Context) (<-chan *Event, error) {
	if s.closed.Load() {
		return nil, fmt.Errorf("streamer is closed")
	}

	ch := s.bus.Subscribe("streamer")
	s.ch = ch

	out := make(chan *Event, 100)

	go func() {
		defer close(out)
		defer s.bus.Unsubscribe(ch)

		for {
			select {
			case event, ok := <-ch:
				if !ok {
					return
				}
				if s.matchesFilter(event) {
					select {
					case out <- event:
					case <-ctx.Done():
						return
					default:
						// Output channel is full, skip
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

// Stop stops the streamer
func (s *Streamer) Stop() error {
	s.closed.Store(true)
	return nil
}

// matchesFilter checks if an event matches the streamer's filter
func (s *Streamer) matchesFilter(event *Event) bool {
	// Check type filter
	if len(s.filter.Types) > 0 {
		typeMatch := false
		for _, t := range s.filter.Types {
			if event.Type == t {
				typeMatch = true
				break
			}
		}
		if !typeMatch {
			return false
		}
	}

	// Check epic filter
	if s.filter.EpicID != "" && event.EpicID != s.filter.EpicID {
		return false
	}

	// Check task filter
	if s.filter.TaskID != "" && event.TaskID != s.filter.TaskID {
		return false
	}

	// Check since filter
	if s.filter.Since > 0 && event.Timestamp < s.filter.Since {
		return false
	}

	// Check until filter
	if s.filter.Until > 0 && event.Timestamp > s.filter.Until {
		return false
	}

	return true
}

// FormatEvent formats an event for JSONL output
func FormatEvent(event *Event) ([]byte, error) {
	return json.Marshal(event)
}

// FormatEventCompact formats an event in a compact human-readable format
func FormatEventCompact(event *Event) string {
	timestamp := event.Timestamp
	return fmt.Sprintf("[%d] %s task=%s epic=%s", timestamp, event.Type, event.TaskID, event.EpicID)
}
