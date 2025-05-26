package github

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNewRepo(t *testing.T) {
	cases := []struct {
		url      string
		expected *Repo
	}{
		{"https://github.com/containerd/nerdctl", &Repo{Owner: "containerd", Repo: "nerdctl"}},
		{"http://www.github.com/containerd/nerdctl.git", &Repo{Owner: "containerd", Repo: "nerdctl"}},
	}
	for _, tc := range cases {
		got, err := NewRepo(tc.url)
		if tc.expected != nil {
			assert.NilError(t, err)
			assert.DeepEqual(t, tc.expected, got)
		} else {
			assert.ErrorContains(t, err, "invalid")
		}
	}
}

func TestTags(t *testing.T) {
	ctx := context.TODO() // t.Context is too new
	repo, err := NewRepo("https://github.com/containerd/containerd")
	assert.NilError(t, err)
	tags, err := repo.Tags(ctx)
	assert.NilError(t, err)
	for _, tag := range tags {
		t.Logf("%s\t%s", tag.Name, tag.Commit.SHA)
	}
}
