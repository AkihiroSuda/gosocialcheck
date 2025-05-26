package github

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"

	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil"
)

func NewRepo(urlStr string) (*Repo, error) {
	pattern := regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+)/([^/]+?)(?:\.git)?(?:/.*)?$`)
	matches := pattern.FindStringSubmatch(urlStr)
	if len(matches) != 3 {
		return nil, fmt.Errorf("invalid GitHub repo URL: %q", urlStr)
	}
	repo := &Repo{
		Owner: matches[1],
		Repo:  matches[2],
	}
	return repo, nil
}

type Repo struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

type Tag struct {
	Name       string `json:"name"`
	ZipballURL string `json:"zipball_url,omitempty"`
	TarballURL string `json:"tarball_url,omitempty"`
	Commit     struct {
		SHA string `json:"sha"`
		URL string `json:"url,omitempty"`
	} `json:"commit"`
	NodeID string `json:"node_id,omitempty"`
}

func (t *Tag) Compact() Tag {
	n := *t
	n.ZipballURL = ""
	n.TarballURL = ""
	n.Commit.URL = ""
	n.NodeID = ""
	return n
}

// Tags returns 30 tags at most.
func (r *Repo) Tags(ctx context.Context, o ...netutil.HTTPOpt) ([]Tag, error) {
	urlStr := fmt.Sprintf("https://api.github.com/repos/%s/%s/tags", r.Owner, r.Repo)
	b, err := netutil.Get(ctx, urlStr, o...)
	if err != nil {
		return nil, err
	}
	var tags []Tag
	if err = json.Unmarshal(b, &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

func (r *Repo) ContentURL(commit, p string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		r.Owner, r.Repo, path.Clean(commit), path.Clean(p))
}
