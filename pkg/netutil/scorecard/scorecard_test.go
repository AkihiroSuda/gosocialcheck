package scorecard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/v3/assert"
)

func TestClientGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/github.com/foo/bar":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"date":"2026-06-29","repo":{"name":"github.com/foo/bar","commit":"deadbeef"},"score":6.5}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}

	t.Run("found", func(t *testing.T) {
		res, err := c.Get(context.Background(), "github.com", "foo", "bar")
		assert.NilError(t, err)
		assert.Equal(t, 6.5, res.Score)
		assert.Equal(t, "github.com/foo/bar", res.Repo.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := c.Get(context.Background(), "github.com", "foo", "unknown")
		assert.Assert(t, errors.Is(err, ErrNotFound), "expected ErrNotFound, got %v", err)
	})
}

func TestRepoForModule(t *testing.T) {
	tests := []struct {
		modPath string
		repoKey string // "host/owner/repo", or "" if not mappable
	}{
		{"github.com/spf13/cobra", "github.com/spf13/cobra"},
		{"github.com/urfave/cli/v2", "github.com/urfave/cli"},
		{"github.com/aws/aws-sdk-go-v2/service/s3", "github.com/aws/aws-sdk-go-v2"},
		{"gitlab.com/gitlab-org/api/client-go", "gitlab.com/gitlab-org/api"},
		{"golang.org/x/tools", "github.com/golang/tools"},
		{"golang.org/x/tools/go/expect", "github.com/golang/tools"},
		{"golang.org/x", ""},
		{"google.golang.org/protobuf", ""},
		{"gopkg.in/yaml.v3", ""},
		{"example.com/vanity", ""},
	}
	for _, tt := range tests {
		t.Run(tt.modPath, func(t *testing.T) {
			host, owner, repo, ok := RepoForModule(tt.modPath)
			if tt.repoKey == "" {
				assert.Assert(t, !ok, "expected not mappable, got %s/%s/%s", host, owner, repo)
			} else {
				assert.Assert(t, ok)
				assert.Equal(t, tt.repoKey, host+"/"+owner+"/"+repo)
			}
		})
	}
}
