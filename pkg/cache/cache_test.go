package cache

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil/github"
)

func tagWithSHA(name, sha string) github.Tag {
	var t github.Tag
	t.Name = name
	t.Commit.SHA = sha
	return t
}

func TestDedupTagsBySHA(t *testing.T) {
	// https://github.com/AkihiroSuda/gosocialcheck/issues/64
	// Multiple Falco component tags share a single commit SHA;
	// the cache must pick the same tag deterministically across runs.
	const sharedSHA = "323213fe0f87ceb825c29c9e8094439d7f8edb56"
	cases := []struct {
		name string
		in   []github.Tag
		want []github.Tag
	}{
		{
			name: "no duplicates",
			in: []github.Tag{
				tagWithSHA("v1.0.0", "aaa"),
				tagWithSHA("v0.9.0", "bbb"),
			},
			want: []github.Tag{
				tagWithSHA("v1.0.0", "aaa"),
				tagWithSHA("v0.9.0", "bbb"),
			},
		},
		{
			name: "shared SHA keeps lexicographically smallest name",
			in: []github.Tag{
				tagWithSHA("agent/0.84.3", sharedSHA),
				tagWithSHA("agent/0.84.1", sharedSHA),
				tagWithSHA("agent/0.84.2", sharedSHA),
			},
			want: []github.Tag{
				tagWithSHA("agent/0.84.1", sharedSHA),
			},
		},
		{
			name: "deterministic regardless of input order",
			in: []github.Tag{
				tagWithSHA("agent/0.84.1", sharedSHA),
				tagWithSHA("agent/0.84.3", sharedSHA),
				tagWithSHA("agent/0.84.2", sharedSHA),
			},
			want: []github.Tag{
				tagWithSHA("agent/0.84.1", sharedSHA),
			},
		},
		{
			name: "preserves first-occurrence order across distinct SHAs",
			in: []github.Tag{
				tagWithSHA("v1.0.0", "aaa"),
				tagWithSHA("v0.9.1", "bbb"),
				tagWithSHA("v0.9.0", "bbb"),
				tagWithSHA("v0.8.0", "ccc"),
			},
			want: []github.Tag{
				tagWithSHA("v1.0.0", "aaa"),
				tagWithSHA("v0.9.0", "bbb"),
				tagWithSHA("v0.8.0", "ccc"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupTagsBySHA(tc.in)
			assert.DeepEqual(t, tc.want, got)
		})
	}
}
