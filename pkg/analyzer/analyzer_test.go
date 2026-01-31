package analyzer

import (
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"gotest.tools/v3/assert"
)

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
