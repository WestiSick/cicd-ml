package http

import "net/http"

// GET /api/system/state
//
// Returns the current bootstrap_done flag and active strategy. The
// frontend's App.tsx polls this endpoint to gate the /setup screen.
func (s *Server) getSystemState(w http.ResponseWriter, r *http.Request) {
	state, err := s.db.GetSystemState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "system_state_failed",
			"Could not load system state", "Try again in a few seconds.")
		return
	}
	writeJSON(w, http.StatusOK, state)
}
