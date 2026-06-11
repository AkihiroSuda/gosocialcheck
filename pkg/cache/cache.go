// Package cache manages the cache.

/*
TODO: consider switching to bbolt

~/.cache: the cache home ($XDG_CACHE_HOME)
  gosocialcheck: the ModTime represents the last updated time
    github.com
      containerd
        containerd
         fb4c30d4ede3531652d86197bf3fc9515e5276d9
           gosocialcheck-cache.json
           go.mod
           go.sum
*/

package cache

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	"github.com/AkihiroSuda/gosocialcheck/pkg/categories"
	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil"
	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil/github"
	"github.com/AkihiroSuda/gosocialcheck/pkg/source/cncf"
)

type ProgressEvent struct {
	Message string `json:"message,omitempty"`
}

type ProgressEventHandler func(context.Context, ProgressEvent)

func DefaultProgressEventHandler(ctx context.Context, ev ProgressEvent) {
	slog.DebugContext(ctx, "progress: "+ev.Message)
}

type opts struct {
	dir        string
	onProgress ProgressEventHandler
	httpClient *http.Client
}

type Opt func(*opts) error

func WithDir(dir string) Opt {
	return func(opts *opts) error {
		opts.dir = dir
		return nil
	}
}

func WithProgressEventHandler(onProgress ProgressEventHandler) Opt {
	return func(opts *opts) error {
		opts.onProgress = onProgress
		return nil
	}
}

func WithHTTPClient(httpClient *http.Client) Opt {
	return func(opts *opts) error {
		opts.httpClient = httpClient
		return nil
	}
}

// New instantiates [Cache].
func New(o ...Opt) (*Cache, error) {
	var c Cache
	for _, f := range o {
		if err := f(&c.opts); err != nil {
			return nil, err
		}
	}
	if c.opts.dir == "" {
		cacheHome, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		c.opts.dir = filepath.Join(cacheHome, "gosocialcheck")
	}
	if c.opts.onProgress == nil {
		c.opts.onProgress = DefaultProgressEventHandler
	}
	if c.opts.httpClient == nil {
		c.opts.httpClient = http.DefaultClient
	}
	return &c, nil
}

type Cache struct {
	opts
	updated []string
}

func (c *Cache) httpOpts() []netutil.HTTPOpt {
	return []netutil.HTTPOpt{
		netutil.WithHTTPClient(c.httpClient),
		netutil.WithAutoGitHubToken(),
	}
}

// LastUpdated returns the last updated time.
// LastUpdated returns [fs.ErrNotExist] on the first run.
func (c *Cache) LastUpdated() (time.Time, error) {
	st, err := os.Stat(c.dir)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// Update updates the cache.
func (c *Cache) Update(ctx context.Context) error {
	b, err := netutil.Get(ctx, cncf.ProjectsURL, c.httpOpts()...)
	if err != nil {
		return err
	}
	var projects cncf.Projects
	if err = yaml.Unmarshal(b, &projects); err != nil {
		return err
	}
	for _, p := range projects {
		if err = c.updateCNCFProject(ctx, p); err != nil {
			return err
		}
	}
	if len(c.updated) > 0 {
		now := time.Now()
		if err = os.Chtimes(c.dir, now, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) updateCNCFProject(ctx context.Context, p cncf.Project) error {
	if p.Maturity != "graduated" {
		return nil
	}
	for _, r := range p.Repositories {
		if err := c.updateCNCFRepo(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) updateCNCFRepo(ctx context.Context, r cncf.Repository) error {
	var category string
	// TODO: include repos that belong to the same org as "code".
	//       Most of them should be accidentally ommited out from "code-lite".
	switch {
	case slices.Contains(r.CheckSets, "code"):
		category = categories.CNCFGraduated
	case slices.Contains(r.CheckSets, "code-lite"):
		// TODO: opt-in
		//	category = categories.CNCFGraduatedSub
	}
	if category != "" {
		if err := c.updateGitHubRepo(ctx, r.URL, category); err != nil {
			return err
		}
	}
	return nil
}

func filterPrelease(tags []github.Tag) []github.Tag {
	var res []github.Tag
	for _, tag := range tags {
		if semver.Prerelease(tag.Name) == "" {
			res = append(res, tag)
		}
	}
	return res
}

// updateGitHubRepo expects url to be "https://github.com/<ORG>/<REPO>".
func (c *Cache) updateGitHubRepo(ctx context.Context, urlStr, category string) error {
	repo, err := github.NewRepo(urlStr)
	if err != nil {
		return err
	}
	tags, err := repo.Tags(ctx, c.httpOpts()...)
	if err != nil {
		return err
	}
	tags = filterPrelease(tags)
	const maxTags = 10
	if len(tags) > maxTags {
		tags = tags[:maxTags]
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, tag := range tags {
		g.Go(func() error {
			return c.updateGitHubRepoTag(ctx, repo, tag, category)
		})
	}
	return g.Wait()
}

func (c *Cache) updateGitHubRepoTag(ctx context.Context, repo *github.Repo, tag github.Tag, category string) error {
	dir := filepath.Join(c.dir, "github.com", repo.Owner, repo.Repo, tag.Commit.SHA)
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, p := range []string{"go.mod", "go.sum"} {
		urlStr := repo.ContentURL(tag.Commit.SHA, p)
		b, err := netutil.Get(ctx, urlStr, c.httpOpts()...)
		if err != nil {
			var err2 *netutil.UnexpectedStatusCodeError
			if errors.As(err, &err2) && err2.StatusCode == 404 {
				// Not Go code
				break
			}
			return err
		}
		f := filepath.Join(dir, p)
		if err = os.WriteFile(f, b, 0o644); err != nil {
			return err
		}
	}
	meta := &Meta{
		Repo:     *repo,
		Tag:      tag.Compact(),
		Category: category,
	}
	metaB, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	metaF := filepath.Join(dir, MetaFilename)
	if err := os.WriteFile(metaF, metaB, 0o644); err != nil {
		return err
	}
	progress := ProgressEvent{
		Message: fmt.Sprintf("%s/%s %s %s (%s)",
			repo.Owner, repo.Repo, tag.Name, tag.Commit.SHA, category),
	}
	c.onProgress(ctx, progress)
	return nil
}

const MetaFilename = "gosocialcheck-meta.json"

type Meta struct {
	Repo     github.Repo `json:"repo"`
	Tag      github.Tag  `json:"tag"`
	Category string      `json:"category"`
}

func (c *Cache) Lookup(ctx context.Context, sum string) ([]Meta, error) {
	if !strings.HasPrefix(sum, "h1:") || !strings.HasSuffix(sum, "=") {
		return nil, fmt.Errorf("expected h1 sum, got %q", sum)
	}
	goSumFiles, err := c.lookupGoSumFiles(ctx, sum)
	if err != nil {
		return nil, err
	}
	var res []Meta
	for _, goSumFile := range goSumFiles {
		dir := filepath.Join(c.dir, filepath.Clean(filepath.Dir(goSumFile)))
		f := filepath.Join(dir, MetaFilename)
		b, err := os.ReadFile(f)
		if err != nil {
			return res, err
		}
		var m Meta
		if err = json.Unmarshal(b, &m); err != nil {
			return res, err
		}
		res = append(res, m)
	}
	return res, nil
}

func (c *Cache) lookupGoSumFiles(ctx context.Context, sum string) ([]string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", c.dir, "grep", "--name-only", "--no-index", sum)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		if exitCode == 1 && stderr.String() == "" {
			// failed successfully
			return nil, nil
		}
		return nil, fmt.Errorf("failed to run %v (stderr=%q)", cmd.Args, stderr.String())
	}
	sc := bufio.NewScanner(&stdout)
	var res []string
	for sc.Scan() {
		line := sc.Text()
		line = strings.TrimSpace(line)
		res = append(res, line)
	}
	return res, sc.Err()
}
