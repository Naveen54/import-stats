package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// LiveUpdate is the small notification payload sent over the live-update SSE
// stream. Clients re-fetch /api/report instead of receiving the full report.
type LiveUpdate struct {
	Changed []string  `json:"changed,omitempty"`
	Added   []string  `json:"added,omitempty"`
	Removed []string  `json:"removed,omitempty"`
	At      time.Time `json:"at"`
}

// Subscriber subscribes one client to live analysis-update notifications.
type Subscriber func() (<-chan LiveUpdate, func())

// Broadcaster fans live updates out to multiple clients without allowing a
// slow client to block publishers or other clients.
type Broadcaster struct {
	mu      sync.Mutex
	next    int
	buffer  int
	clients map[int]chan LiveUpdate
}

// NewBroadcaster creates a live-update broadcaster. buffer values below one use
// a single-slot client buffer so a slow browser receives the newest update.
func NewBroadcaster(buffer int) *Broadcaster {
	if buffer < 1 {
		buffer = 1
	}
	return &Broadcaster{buffer: buffer, clients: make(map[int]chan LiveUpdate)}
}

// Subscriber returns a subscription function suitable for LiveHandler.
func (b *Broadcaster) Subscriber() Subscriber {
	return func() (<-chan LiveUpdate, func()) {
		ch := make(chan LiveUpdate, b.buffer)
		b.mu.Lock()
		id := b.next
		b.next++
		b.clients[id] = ch
		b.mu.Unlock()

		var once sync.Once
		unsubscribe := func() {
			once.Do(func() {
				b.mu.Lock()
				if current, ok := b.clients[id]; ok {
					delete(b.clients, id)
					close(current)
				}
				b.mu.Unlock()
			})
		}
		return ch, unsubscribe
	}
}

// Publish broadcasts an update, dropping stale queued updates for slow clients.
func (b *Broadcaster) Publish(update LiveUpdate) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.clients {
		select {
		case ch <- update:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- update:
			default:
			}
		}
	}
}

var liveHeartbeatInterval = 15 * time.Second

// LiveHandler streams analysis updates to the dashboard over SSE.
func LiveHandler(sub Subscriber) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		if sub == nil {
			http.NotFound(w, r)
			return
		}

		updates, unsubscribe := sub()
		defer unsubscribe()

		header := w.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")

		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		heartbeat := time.NewTicker(liveHeartbeatInterval)
		defer heartbeat.Stop()
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case update, ok := <-updates:
				if !ok {
					return
				}
				if err := writeSSE(w, "update", update); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}

func writeSSE(w http.ResponseWriter, name string, payload LiveUpdate) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}
