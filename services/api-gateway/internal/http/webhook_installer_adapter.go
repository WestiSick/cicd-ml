package http

import (
	"context"

	gh "github.com/buzdin/cicd-ml/api-gateway/internal/github"
)

// installerAdapter wraps the rich github.Installer.Result into the
// flatter 4-return signature the bootstrap.WebhookInstaller interface
// expects. Without this adapter the bootstrap package would have to
// import the github package directly — circular-import-adjacent, and
// would couple unit-tests on the orchestrator to the GitHub API surface.
//
// The adapter lives in the http package because that's the only place
// that already imports both halves. (The orchestrator → http edge is
// the natural place for this wiring.)
type installerAdapter struct {
	inner *gh.Installer
}

func (a installerAdapter) Install(ctx context.Context, token, owner, repo string) (status string, hookID int64, callbackURL, errMsg string) {
	r := a.inner.Install(ctx, token, owner, repo)
	return string(r.Status), r.HookID, r.CallbackURL, r.Err
}
