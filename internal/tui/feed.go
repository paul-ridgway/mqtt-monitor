package tui

import (
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Feed collects events from the MQTT client and delivers them to the UI in
// batches, so a busy broker does not flood the update loop.
type Feed struct {
	mu      sync.Mutex
	pending []any
	wake    chan struct{}
}

// NewFeed returns an empty feed.
func NewFeed() *Feed {
	return &Feed{wake: make(chan struct{}, 1)}
}

// Emit queues an event; it is safe to call from any goroutine.
func (f *Feed) Emit(ev any) {
	f.mu.Lock()
	f.pending = append(f.pending, ev)
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *Feed) drain() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.pending
	f.pending = nil
	return out
}

type batchMsg []any

// next waits for events, then gives a short window for more to arrive.
func (f *Feed) next() tea.Cmd {
	return func() tea.Msg {
		<-f.wake
		time.Sleep(30 * time.Millisecond)
		return batchMsg(f.drain())
	}
}
