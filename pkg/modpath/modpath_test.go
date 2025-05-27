package modpath

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestDirFromFileAndPkg(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		Mod     string
		modVer  string
		want    string
		wantErr bool
	}{
		{
			name:   "Without version suffix",
			file:   "/Users/me/gopath/src/github.com/containerd/containerd/pkg/archive/tar.go",
			Mod:    "github.com/containerd/containerd",
			modVer: "v1.0.0",
			want:   "/Users/me/gopath/src/github.com/containerd/containerd",
		},
		{
			name:   "With /v2 version suffix",
			file:   "/Users/me/gopath/src/github.com/containerd/containerd/pkg/archive/tar.go",
			Mod:    "github.com/containerd/containerd/v2",
			modVer: "v2.0.0",
			want:   "/Users/me/gopath/src/github.com/containerd/containerd",
		},
		{
			name:   "Without /v2 version suffix, in GOPATH/pkg/mod",
			file:   "/Users/me/gopath/pkg/mod/github.com/containerd/containerd@v1.0.0/pkg/archive/tar.go",
			Mod:    "github.com/containerd/containerd",
			modVer: "v1.0.0",
			want:   "/Users/me/gopath/pkg/mod/github.com/containerd/containerd@v1.0.0",
		},
		{
			name:   "With /v2 version suffix, in GOPATH/pkg/mod",
			file:   "/Users/me/gopath/pkg/mod/github.com/containerd/containerd@v2.0.0/pkg/archive/tar.go",
			Mod:    "github.com/containerd/containerd/v2",
			modVer: "v2.0.0",
			want:   "/Users/me/gopath/pkg/mod/github.com/containerd/containerd@v2.0.0",
		},
		{
			name:    "Module path not found",
			file:    "/some/other/path/main.go",
			Mod:     "github.com/containerd/containerd",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, err := DirFromFileAndMod(tc.file, tc.Mod, tc.modVer)
			if tc.wantErr {
				assert.ErrorContains(t, err, "module path")
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tc.want, root)
			assert.Assert(t, strings.HasPrefix(tc.file, tc.want))
		})
	}
}
