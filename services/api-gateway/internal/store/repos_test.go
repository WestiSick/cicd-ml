package store

import "testing"

func TestParseGithubURL(t *testing.T) {
	cases := []struct {
		in        string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{"vitejs/vite", "vitejs", "vite", false},
		{"https://github.com/vitejs/vite", "vitejs", "vite", false},
		{"https://github.com/vitejs/vite.git", "vitejs", "vite", false},
		{"https://github.com/vitejs/vite/", "vitejs", "vite", false},
		{"http://github.com/vitejs/vite", "vitejs", "vite", false},
		{"github.com/vitejs/vite", "vitejs", "vite", false},
		{"git@github.com:vitejs/vite.git", "vitejs", "vite", false},
		{"  vitejs/vite  ", "vitejs", "vite", false},

		{"", "", "", true},
		{"vitejs", "", "", true},
		{"vitejs/", "", "", true},
		{"/vite", "", "", true},
		{"vitejs/vite/extra", "", "", true},
		{"https://gitlab.com/foo/bar", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			owner, name, err := ParseGithubURL(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got owner=%q name=%q", owner, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != c.wantOwner || name != c.wantName {
				t.Fatalf("got %s/%s, want %s/%s", owner, name, c.wantOwner, c.wantName)
			}
		})
	}
}

func TestRepoSlug(t *testing.T) {
	if got := (Repo{Owner: "foo", Name: "bar"}).Slug(); got != "foo/bar" {
		t.Fatalf("got %q", got)
	}
}
