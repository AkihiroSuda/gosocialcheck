package analyzer

import (
	"bytes"
	"context"
	"go/token"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"gotest.tools/v3/assert"
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
