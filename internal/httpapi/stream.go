package httpapi

import (
	"fmt"
	"net/http"
	"time"
)

// serveSSE keeps one browser connected and writes every event on topic to it
// as a Server-Sent Event. The browser's EventSource reconnects by itself.
func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, topic string) {
	rc := http.NewResponseController(w)
	// The server has short read/write timeouts for normal requests; a stream must outlive them.
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // tell nginx not to buffer
	w.WriteHeader(http.StatusOK)

	ch, unsubscribe := s.hub.Subscribe(topic)
	defer unsubscribe()

	if _, err := fmt.Fprint(w, "retry: 5000\n: connected\n\n"); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data); err != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
