package bootstrap

import "testing"

func TestSetupRequestValidate(t *testing.T) {
	cases := []struct {
		name    string
		req     SetupRequest
		wantErr bool
	}{
		{
			name:    "no repos",
			req:     SetupRequest{Repos: nil, HistoryMonths: 6, Models: []string{"xgboost"}},
			wantErr: true,
		},
		{
			name:    "no models",
			req:     SetupRequest{Repos: []string{"vitejs/vite"}, HistoryMonths: 6, Models: nil},
			wantErr: true,
		},
		{
			name:    "bad months",
			req:     SetupRequest{Repos: []string{"vitejs/vite"}, HistoryMonths: 5, Models: []string{"xgboost"}},
			wantErr: true,
		},
		{
			name:    "ok 3 months",
			req:     SetupRequest{Repos: []string{"vitejs/vite"}, HistoryMonths: 3, Models: []string{"xgboost"}},
			wantErr: false,
		},
		{
			name:    "ok 12 months",
			req:     SetupRequest{Repos: []string{"vitejs/vite"}, HistoryMonths: 12, Models: []string{"xgboost", "lightgbm"}},
			wantErr: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.req.Validate()
			if c.wantErr != (err != nil) {
				t.Fatalf("want err=%v, got %v", c.wantErr, err)
			}
		})
	}
}
