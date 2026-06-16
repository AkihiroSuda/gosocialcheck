package analyzer

import (
	"bytes"
	"context"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"gotest.tools/v3/assert"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	assert.NilError(t, err)
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

func TestFlushGHAPrioritizesAndCaps(t *testing.T) {
	sumPosn := func(line int) token.Position {
		return token.Position{Filename: "/repo/go.sum", Line: line, Column: 1}
	}

	t.Run("only go.sum is annotated", func(t *testing.T) {
		inst := &instance{Opts: Opts{GHA: true}, cwd: "/repo"}
		inst.addGHAFinding(ghaFinding{msg: "m", sumPosn: sumPosn(3)})
		out := captureStdout(t, func() { inst.flushGHA(context.Background()) })
		lines := strings.Split(strings.TrimSpace(out), "\n")
		assert.Equal(t, 1, len(lines))
		assert.Assert(t, strings.Contains(lines[0], "file=go.sum"), "must annotate go.sum: %q", lines[0])
		assert.Assert(t, !strings.Contains(out, ".go,"), "must not annotate .go files: %q", out)
	})

	t.Run("changed-in-PR findings emitted first", func(t *testing.T) {
		inst := &instance{Opts: Opts{GHA: true}, cwd: "/repo"}
		inst.addGHAFinding(ghaFinding{msg: "other-finding", sumPosn: sumPosn(1)})
		inst.addGHAFinding(ghaFinding{msg: "priority-finding", sumPosn: sumPosn(9), changedInPR: true})
		out := captureStdout(t, func() { inst.flushGHA(context.Background()) })
		lines := strings.Split(strings.TrimSpace(out), "\n")
		assert.Equal(t, 2, len(lines))
		assert.Assert(t, strings.Contains(lines[0], "priority-finding"), "changed-in-PR finding should be first: %q", lines[0])
		assert.Assert(t, strings.Contains(lines[1], "other-finding"))
	})

	t.Run("caps total annotations", func(t *testing.T) {
		inst := &instance{Opts: Opts{GHA: true}, cwd: "/repo"}
		for i := 0; i < 60; i++ {
			inst.addGHAFinding(ghaFinding{msg: "m", sumPosn: sumPosn(i + 1)})
		}
		out := captureStdout(t, func() { inst.flushGHA(context.Background()) })
		assert.Equal(t, ghaMaxAnnotations, strings.Count(out, "::warning"))
	})

	t.Run("changed-in-PR findings survive the cap", func(t *testing.T) {
		inst := &instance{Opts: Opts{GHA: true}, cwd: "/repo"}
		// Fill well past the cap with unchanged findings, then add one changed.
		for i := 0; i < 60; i++ {
			inst.addGHAFinding(ghaFinding{msg: "other-finding", sumPosn: sumPosn(i + 1)})
		}
		inst.addGHAFinding(ghaFinding{msg: "priority-finding", sumPosn: sumPosn(999), changedInPR: true})
		out := captureStdout(t, func() { inst.flushGHA(context.Background()) })
		assert.Assert(t, strings.Contains(out, "priority-finding"), "changed-in-PR finding must survive the cap")
	})
}

func TestModuleVersion(t *testing.T) {
	tests := []struct {
		name     string
		goMod    string
		imp      string
		expected *module.Version
	}{
		{
			name: "simple require",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
`,
			imp:      "github.com/bar/baz",
			expected: &module.Version{Path: "github.com/bar/baz", Version: "v1.0.0"},
		},
		{
			name: "subpackage import",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
`,
			imp:      "github.com/bar/baz/sub/pkg",
			expected: &module.Version{Path: "github.com/bar/baz", Version: "v1.0.0"},
		},
		{
			name: "blanket replace",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz => github.com/my/fork v2.0.0
`,
			imp:      "github.com/bar/baz",
			expected: &module.Version{Path: "github.com/my/fork", Version: "v2.0.0"},
		},
		{
			name: "version-specific replace (matching)",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz v1.0.0 => github.com/my/fork v2.0.0
`,
			imp:      "github.com/bar/baz",
			expected: &module.Version{Path: "github.com/my/fork", Version: "v2.0.0"},
		},
		{
			name: "version-specific replace (not matching)",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz v1.1.0 => github.com/my/fork v2.0.0
`,
			imp:      "github.com/bar/baz",
			expected: &module.Version{Path: "github.com/bar/baz", Version: "v1.0.0"},
		},
		{
			name: "local path replace",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz => ../local/baz
`,
			imp:      "github.com/bar/baz",
			expected: nil,
		},
		{
			name: "local path replace with dot",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz => ./local/baz
`,
			imp:      "github.com/bar/baz",
			expected: nil,
		},
		{
			name: "absolute path replace",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz => /home/user/baz
`,
			imp:      "github.com/bar/baz",
			expected: nil,
		},
		{
			name: "unknown import",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
`,
			imp:      "github.com/other/pkg",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goMod, err := modfile.Parse("go.mod", []byte(tt.goMod), nil)
			assert.NilError(t, err)

			got := moduleVersion(goMod, tt.imp)
			if tt.expected == nil {
				assert.Assert(t, got == nil, "expected nil, got %v", got)
			} else {
				assert.Assert(t, got != nil, "expected %v, got nil", tt.expected)
				assert.Equal(t, tt.expected.Path, got.Path)
				assert.Equal(t, tt.expected.Version, got.Version)
			}
		})
	}
}

type fakeResolver struct {
	// hits maps an h1 sum to a non-empty result (adopted by a trusted project).
	hits map[string][]cache.Meta
}

func (f *fakeResolver) Lookup(_ context.Context, sum string) ([]cache.Meta, error) {
	return f.hits[sum], nil
}

func newInstanceForTest(t *testing.T, gha bool, resolver Resolver) *instance {
	t.Helper()
	return &instance{
		Opts:                Opts{GHA: gha, Cache: resolver},
		processedSums:       make(map[string]struct{}),
		changedSumLines:     make(map[string]map[int]struct{}),
		changedSumLinesDone: make(map[string]bool),
		mods:                make(map[string]*modInfo),
	}
}

func TestCollectIndirect(t *testing.T) {
	const goModSrc = `module example.com/foo

go 1.25.0

require example.com/direct v1.0.0

require (
	example.com/indirect-untrusted v1.2.0 // indirect
	example.com/indirect-trusted-adopted v1.3.0 // indirect
)
`
	goMod, err := modfile.Parse("go.mod", []byte(goModSrc), nil)
	assert.NilError(t, err)

	const goSumSrc = `example.com/direct v1.0.0 h1:direct=
example.com/indirect-untrusted v1.2.0 h1:untrusted=
example.com/indirect-trusted-adopted v1.3.0 h1:adopted=
`
	goSum, err := parseGoSum(strings.NewReader(goSumSrc))
	assert.NilError(t, err)

	resolver := &fakeResolver{hits: map[string][]cache.Meta{
		"h1:adopted=": {{Category: "cncf-graduated"}},
	}}

	mi := &modInfo{
		goModFilename: "/repo/go.mod",
		goSumFilename: "/repo/go.sum",
		goMod:         goMod,
		goSum:         goSum,
		policies:      map[string]string{},
	}

	t.Run("non-gha reports unadopted indirect deps at go.mod lines", func(t *testing.T) {
		inst := newInstanceForTest(t, false, resolver)
		inst.recordModule(mi)
		// Simulate the direct dependency having been reported via its import site.
		inst.processedSums["h1:direct="] = struct{}{}

		findings, err := inst.collectIndirect(context.Background())
		assert.NilError(t, err)
		assert.Equal(t, 1, len(findings))
		f := findings[0]
		assert.Assert(t, strings.Contains(f.msg, "example.com/indirect-untrusted@v1.2.0"), "msg: %q", f.msg)
		assert.Equal(t, "/repo/go.mod", f.modPosn.Filename)
		assert.Equal(t, 8, f.modPosn.Line)
	})

	t.Run("trusted directive silences indirect dep", func(t *testing.T) {
		inst := newInstanceForTest(t, false, resolver)
		miTrusted := *mi
		miTrusted.policies = map[string]string{"example.com/indirect-untrusted": directivePolicyTrusted}
		inst.recordModule(&miTrusted)
		inst.processedSums["h1:direct="] = struct{}{}

		findings, err := inst.collectIndirect(context.Background())
		assert.NilError(t, err)
		assert.Equal(t, 0, len(findings))
	})

	t.Run("gha annotates go.sum line", func(t *testing.T) {
		inst := newInstanceForTest(t, true, resolver)
		inst.recordModule(mi)
		inst.processedSums["h1:direct="] = struct{}{}

		findings, err := inst.collectIndirect(context.Background())
		assert.NilError(t, err)
		assert.Equal(t, 1, len(findings))
		f := findings[0]
		assert.Equal(t, "/repo/go.sum", f.sumPosn.Filename)
		assert.Equal(t, 2, f.sumPosn.Line) // line of indirect-untrusted in go.sum
	})

	t.Run("already-processed direct deps are skipped", func(t *testing.T) {
		inst := newInstanceForTest(t, false, resolver)
		inst.recordModule(mi)
		// Mark every module as already processed.
		for _, h := range []string{"h1:direct=", "h1:untrusted=", "h1:adopted="} {
			inst.processedSums[h] = struct{}{}
		}
		findings, err := inst.collectIndirect(context.Background())
		assert.NilError(t, err)
		assert.Equal(t, 0, len(findings))
	})
}

func TestResolveReplace(t *testing.T) {
	tests := []struct {
		name     string
		goMod    string
		reqPath  string
		reqVer   string
		expected *module.Version
	}{
		{
			name: "no replace",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
`,
			reqPath:  "github.com/bar/baz",
			reqVer:   "v1.0.0",
			expected: &module.Version{Path: "github.com/bar/baz", Version: "v1.0.0"},
		},
		{
			name: "blanket replace",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz => github.com/my/fork v2.0.0
`,
			reqPath:  "github.com/bar/baz",
			reqVer:   "v1.0.0",
			expected: &module.Version{Path: "github.com/my/fork", Version: "v2.0.0"},
		},
		{
			name: "local path replace yields nil",
			goMod: `module example.com/foo
require github.com/bar/baz v1.0.0
replace github.com/bar/baz => ../local/baz
`,
			reqPath:  "github.com/bar/baz",
			reqVer:   "v1.0.0",
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goMod, err := modfile.Parse("go.mod", []byte(tt.goMod), nil)
			assert.NilError(t, err)
			got := resolveReplace(goMod, module.Version{Path: tt.reqPath, Version: tt.reqVer})
			if tt.expected == nil {
				assert.Assert(t, got == nil, "expected nil, got %v", got)
			} else {
				assert.Assert(t, got != nil, "expected %v, got nil", tt.expected)
				assert.Equal(t, tt.expected.Path, got.Path)
				assert.Equal(t, tt.expected.Version, got.Version)
			}
		})
	}
}

func TestParseDiffNewLines(t *testing.T) {
	tests := []struct {
		name     string
		diff     string
		expected []int
	}{
		{
			name:     "empty diff",
			diff:     "",
			expected: nil,
		},
		{
			name: "single added line",
			diff: `diff --git a/go.sum b/go.sum
index 111..222 100644
--- a/go.sum
+++ b/go.sum
@@ -5 +5,1 @@
+example.com/foo v1.0.0 h1:abc=
`,
			expected: []int{5},
		},
		{
			name: "multiple added lines in one hunk",
			diff: `@@ -10,0 +11,3 @@
+a
+b
+c
`,
			expected: []int{11, 12, 13},
		},
		{
			name: "implicit count of one",
			diff: `@@ -1 +2 @@
+x
`,
			expected: []int{2},
		},
		{
			name: "pure deletion has no new lines",
			diff: `@@ -3,2 +2,0 @@
-gone1
-gone2
`,
			expected: nil,
		},
		{
			name: "multiple hunks",
			diff: `@@ -1 +1 @@
-old
+new
@@ -8,0 +9,2 @@
+p
+q
`,
			expected: []int{1, 9, 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDiffNewLines(tt.diff)
			assert.Equal(t, len(tt.expected), len(got), "got %v", got)
			for _, want := range tt.expected {
				_, ok := got[want]
				assert.Assert(t, ok, "expected line %d in %v", want, got)
			}
		})
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, args...)
	assert.NilError(t, err, "git %v: %s", args, out)
	return out
}

func initRepoT(t *testing.T, dir string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(dir, 0o755))
	gitT(t, dir, "-c", "init.defaultBranch=master", "init")
	gitT(t, dir, "config", "user.email", "test@example.com")
	gitT(t, dir, "config", "user.name", "test")
	gitT(t, dir, "config", "commit.gpgsign", "false")
}

func commitFileT(t *testing.T, dir, name, content string) string {
	t.Helper()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	gitT(t, dir, "add", name)
	gitT(t, dir, "commit", "-m", "commit "+content)
	return gitT(t, dir, "rev-parse", "HEAD")
}

func TestResolveBaseTip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	inst := &instance{}

	t.Run("remote-tracking ref only", func(t *testing.T) {
		root := t.TempDir()
		upstream := filepath.Join(root, "upstream")
		work := filepath.Join(root, "work")
		initRepoT(t, upstream)
		commitFileT(t, upstream, "f", "c1")
		initRepoT(t, work)
		gitT(t, work, "remote", "add", "origin", upstream)
		gitT(t, work, "fetch", "origin")

		ref, ok := inst.resolveBaseTip(context.Background(), work, "master")
		assert.Assert(t, ok)
		assert.Equal(t, "origin/master", ref)
	})

	t.Run("local branch only", func(t *testing.T) {
		root := t.TempDir()
		work := filepath.Join(root, "work")
		initRepoT(t, work)
		commitFileT(t, work, "f", "c1")

		ref, ok := inst.resolveBaseTip(context.Background(), work, "master")
		assert.Assert(t, ok)
		assert.Equal(t, "refs/heads/master", ref)
	})

	t.Run("local branch newer than remote is preferred", func(t *testing.T) {
		root := t.TempDir()
		upstream := filepath.Join(root, "upstream")
		work := filepath.Join(root, "work")
		initRepoT(t, upstream)
		commitFileT(t, upstream, "f", "c1")
		initRepoT(t, work)
		gitT(t, work, "remote", "add", "origin", upstream)
		gitT(t, work, "fetch", "origin")
		// Local master starts at origin/master, then advances ahead of it.
		gitT(t, work, "checkout", "-B", "master", "origin/master")
		commitFileT(t, work, "f", "c2")

		ref, ok := inst.resolveBaseTip(context.Background(), work, "master")
		assert.Assert(t, ok)
		assert.Equal(t, "refs/heads/master", ref)
	})

	t.Run("remote newer than local branch is preferred", func(t *testing.T) {
		root := t.TempDir()
		upstream := filepath.Join(root, "upstream")
		work := filepath.Join(root, "work")
		initRepoT(t, upstream)
		c1 := commitFileT(t, upstream, "f", "c1")
		initRepoT(t, work)
		gitT(t, work, "remote", "add", "origin", upstream)
		gitT(t, work, "fetch", "origin")
		// Local master stays at c1 while origin advances to c2.
		gitT(t, work, "update-ref", "refs/heads/master", c1)
		commitFileT(t, upstream, "f", "c2")
		gitT(t, work, "fetch", "origin")

		ref, ok := inst.resolveBaseTip(context.Background(), work, "master")
		assert.Assert(t, ok)
		assert.Equal(t, "origin/master", ref)
	})

	t.Run("fetches into FETCH_HEAD when absent locally", func(t *testing.T) {
		root := t.TempDir()
		upstream := filepath.Join(root, "upstream")
		work := filepath.Join(root, "work")
		initRepoT(t, upstream)
		commitFileT(t, upstream, "f", "c1")
		initRepoT(t, work)
		// Remote is configured but never fetched: no origin/master, no local master.
		gitT(t, work, "remote", "add", "origin", upstream)

		ref, ok := inst.resolveBaseTip(context.Background(), work, "master")
		assert.Assert(t, ok)
		assert.Equal(t, "FETCH_HEAD", ref)
	})

	t.Run("returns false when the base ref cannot be obtained", func(t *testing.T) {
		root := t.TempDir()
		work := filepath.Join(root, "work")
		initRepoT(t, work)
		commitFileT(t, work, "f", "c1")
		// No remote and no branch named "missing".
		ref, ok := inst.resolveBaseTip(context.Background(), work, "missing")
		assert.Assert(t, !ok)
		assert.Equal(t, "", ref)
	})
}

func TestIsLocalPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"./local", true},
		{"../parent", true},
		{"/absolute/path", true},
		{"github.com/foo/bar", false},
		{"example.com/pkg", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isLocalPath(tt.path)
			assert.Equal(t, tt.expected, got)
		})
	}
}
