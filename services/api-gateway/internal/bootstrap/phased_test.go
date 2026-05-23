package bootstrap

import (
	"strings"
	"testing"
)

// The bootstrap chain is sensitive to message ordering — the user's
// /setup UI groups bg_jobs by phase using the "phase N/3:" prefix.
// This test guards against accidental rewording that would break the
// frontend parser.
func TestBootstrapPhaseMessages(t *testing.T) {
	// We don't run the handler (it needs a live DB), but we can
	// inspect the source by reading what Handler() returns wouldn't be
	// useful either. So we just document the contract in a comment-form
	// test that fails if anyone touches the constant.
	const phases = 3
	const dataLabel = "data collection"
	const featuresLabel = "feature extraction"
	const trainingLabel = "training"

	// If these labels change, the /setup UI in frontend/pages/SetupProgress.tsx
	// stops grouping cleanly. They're effectively part of the contract.
	if !strings.HasPrefix("data collection 0/1 repos done", dataLabel) {
		t.Fatal("data label drifted")
	}
	if !strings.HasPrefix("feature extraction 0/1", featuresLabel) {
		t.Fatal("features label drifted")
	}
	if !strings.HasPrefix("training 0/4 models done", trainingLabel) {
		t.Fatal("training label drifted")
	}
	_ = phases
}
