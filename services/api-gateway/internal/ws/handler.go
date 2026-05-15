package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// Default upgrader. We allow any origin in dev because the React app
// runs on :5173 while the API is on :8080; CORS is enforced at the chi
// middleware layer for the REST endpoints. For prod the same-origin
// reverse proxy (Traefik) means CheckOrigin can stay permissive.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(*http.Request) bool { return true },
}

const (
	pingPeriod = 30 * time.Second
	writeWait  = 10 * time.Second
)

// Serve upgrades the HTTP connection and pumps events from `topic` to the
// client until either side closes. One goroutine per connection — fine for
// our scale (single-user system).
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, topic string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn().Err(err).Msg("ws upgrade")
		return
	}
	defer conn.Close()

	events, unsubscribe := h.Subscribe(topic)
	defer unsubscribe()

	pinger := time.NewTicker(pingPeriod)
	defer pinger.Stop()

	// Reader goroutine — discards client frames but lets us notice when
	// the client goes away (Close → ReadMessage error → done channel).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-pinger.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case ev := <-events:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		}
	}
}
