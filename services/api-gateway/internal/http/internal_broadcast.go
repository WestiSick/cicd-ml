package http

import (
	"encoding/json"
	"net/http"
)

// POST /api/internal/broadcast
//
// Re-publishes the supplied payload on the in-process WebSocket hub so
// every connected /ws client gets it. Called by the standalone
// collector + simulator binaries via bgjobs.HTTPBroadcaster — they
// can't touch this gateway's hub directly because they run in their own
// containers.
//
// Body:
//
//	{
//	  "channel": "bg-jobs",
//	  "type":    "bg_job.updated",
//	  "data":    {...arbitrary JSON, forwarded verbatim...}
//	}
//
// Security note: the route is on /api/internal/ which the prod
// reverse-proxy MUST NOT expose externally — the Traefik rules in
// docker-compose.prod.yml route only /api, /ws, /webhooks, not
// /api/internal. Within the docker network this is fine; in any
// deployment that exposes /api/internal/ a real auth layer should be
// added.
func (s *Server) internalBroadcast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string          `json:"channel"`
		Type    string          `json:"type"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Could not decode broadcast envelope", "")
		return
	}
	if body.Channel == "" || body.Type == "" {
		writeError(w, http.StatusBadRequest, "missing_fields",
			"channel and type are required", "")
		return
	}
	// Pass the raw bytes through — the Hub already JSON-encodes its
	// publish call, so wrapping in a generic any is fine.
	var parsed any
	if len(body.Data) > 0 {
		_ = json.Unmarshal(body.Data, &parsed)
	}
	s.hub.PublishJSON(body.Channel, body.Type, parsed)
	w.WriteHeader(http.StatusNoContent)
}
