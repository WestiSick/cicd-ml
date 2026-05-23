package bgjobs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// HTTPBroadcaster posts bg_job updates to the api-gateway's internal
// broadcast endpoint, which re-publishes them on the in-process WebSocket
// hub. Used by the standalone collector + simulator binaries (which run
// in their own containers and therefore can't share the gateway's hub
// in-process).
//
// Best-effort: failures are logged and dropped. The frontend already
// polls bg_jobs every 3s as a fallback, so a missed WebSocket push just
// delays the UI update by ≤3s.
//
// Endpoint contract:
//
//	POST {GatewayBase}/api/internal/broadcast
//	body: {"channel": "bg-jobs", "type": "bg_job.updated", "data": {...}}
//
// No auth — the endpoint is intended for the docker-internal network
// only. The compose file does not expose /api/internal/* externally;
// production with a reverse proxy should not forward this path.
type HTTPBroadcaster struct {
	// GatewayBase is the URL the worker uses to reach the gateway.
	// Typical value: `http://api:8080`. Set via env in the worker binary.
	GatewayBase string

	// HTTP is the client used for the POST. 2-second timeout is plenty
	// — the gateway's broadcast handler does only a hub publish.
	HTTP *http.Client
}

func NewHTTPBroadcaster(gatewayBase string) HTTPBroadcaster {
	return HTTPBroadcaster{
		GatewayBase: gatewayBase,
		HTTP:        &http.Client{Timeout: 2 * time.Second},
	}
}

func (b HTTPBroadcaster) Publish(ctx context.Context, channel, eventType string, payload any) {
	if b.GatewayBase == "" || b.HTTP == nil {
		return
	}
	body := map[string]any{
		"channel": channel,
		"type":    eventType,
		"data":    payload,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		log.Warn().Err(err).Msg("broadcaster: marshal")
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.GatewayBase+"/api/internal/broadcast", bytes.NewReader(raw))
	if err != nil {
		log.Warn().Err(err).Msg("broadcaster: new request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	if err != nil {
		// Common: gateway briefly unavailable during restart. Don't spam
		// at warn level — debug is enough.
		log.Debug().Err(err).Str("base", b.GatewayBase).Msg("broadcaster: post (gateway likely transient)")
		return
	}
	resp.Body.Close()
}
