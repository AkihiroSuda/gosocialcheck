package cache

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"sync"
)

// fsDB is a read-only database backed by [fs.FS], with the same layout as
// the cache directory. Unlike the cache directory, which is searched with
// `git grep`, fsDB is searched with an in-memory index that is lazily built
// on the first lookup.
type fsDB struct {
	fsys   fs.FS
	once   sync.Once
	idx    map[string][]string // h1 sum -> directories containing the matching go.sum
	idxErr error
}

func (d *fsDB) buildIndex() {
	d.idx = make(map[string][]string)
	d.idxErr = fs.WalkDir(d.fsys, ".", func(p string, ent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ent.IsDir() || ent.Name() != "go.sum" {
			return nil
		}
		f, err := d.fsys.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		dir := path.Dir(p)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) != 3 {
				continue
			}
			if strings.HasSuffix(fields[1], "/go.mod") {
				// Lookup queries the h1 sum of the module zip, not of go.mod
				continue
			}
			sum := fields[2]
			dirs := d.idx[sum]
			if len(dirs) == 0 || dirs[len(dirs)-1] != dir {
				d.idx[sum] = append(dirs, dir)
			}
		}
		return sc.Err()
	})
}

func (d *fsDB) lookup(sum string) ([]Meta, error) {
	d.once.Do(d.buildIndex)
	if d.idxErr != nil {
		return nil, d.idxErr
	}
	var res []Meta
	for _, dir := range d.idx[sum] {
		b, err := fs.ReadFile(d.fsys, path.Join(dir, MetaFilename))
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
