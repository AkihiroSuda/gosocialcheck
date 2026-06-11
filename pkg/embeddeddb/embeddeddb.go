// Package embeddeddb provides the database that was embedded into the binary
// at build time, so that `gosocialcheck run` can work without running
// `gosocialcheck update` first.
//
// The db directory is periodically updated by the "Update the embedded database"
// GitHub Actions workflow (.github/workflows/update-db.yml), which runs
// `gosocialcheck update --dir pkg/embeddeddb/db` and opens a pull request.
package embeddeddb

import (
	"embed"
	"io/fs"
)

//go:embed all:db
var embedded embed.FS

// FS returns the embedded database, rooted at the db directory.
// The layout is the same as the cache directory of [github.com/AkihiroSuda/gosocialcheck/pkg/cache].
func FS() (fs.FS, error) {
	return fs.Sub(embedded, "db")
}

// IsEmpty returns true if the embedded database contains no entries,
// e.g., when the binary was built from a source tree in which the db
// directory was not populated yet.
func IsEmpty() (bool, error) {
	fsys, err := FS()
	if err != nil {
		return false, err
	}
	ents, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return false, err
	}
	for _, ent := range ents {
		if ent.IsDir() {
			return false, nil
		}
	}
	return true, nil
}
