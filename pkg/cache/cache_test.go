package cache

import (
	"path/filepath"
	"testing"
	"testing/fstest"

	"gotest.tools/v3/assert"
)

func newTestFS() fstest.MapFS {
	const (
		fooMeta  = `{"repo":{"owner":"foo","repo":"foo"},"tag":{"name":"v1.0.0","commit":{"sha":"f00"}},"category":"test"}`
		barMeta  = `{"repo":{"owner":"bar","repo":"bar"},"tag":{"name":"v2.0.0","commit":{"sha":"ba2"}},"category":"test"}`
		fooGoSum = `example.com/aaa v1.0.0 h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=
example.com/aaa v1.0.0/go.mod h1:gomodgomodgomodgomodgomodgomodgomodgomodgom=
example.com/bbb v1.2.3 h1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=
example.com/bbb v1.2.3/go.mod h1:dommdommdommdommdommdommdommdommdommdommdom=
`
		barGoSum = `example.com/bbb v1.2.3 h1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=
example.com/bbb v1.2.3/go.mod h1:dommdommdommdommdommdommdommdommdommdommdom=
`
	)
	return fstest.MapFS{
		"github.com/foo/foo/f00/go.sum":                  &fstest.MapFile{Data: []byte(fooGoSum)},
		"github.com/foo/foo/f00/" + MetaFilename:         &fstest.MapFile{Data: []byte(fooMeta)},
		"github.com/bar/bar/ba2/go.sum":                  &fstest.MapFile{Data: []byte(barGoSum)},
		"github.com/bar/bar/ba2/" + MetaFilename:         &fstest.MapFile{Data: []byte(barMeta)},
		"github.com/qux/qux/c0ffee/" + MetaFilename + "": &fstest.MapFile{Data: []byte(`{}`)}, // no go.sum (not Go code)
	}
}

func TestFSDB(t *testing.T) {
	db := &fsDB{fsys: newTestFS()}

	t.Run("single hit", func(t *testing.T) {
		res, err := db.lookup("h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=")
		assert.NilError(t, err)
		assert.Equal(t, 1, len(res))
		assert.Equal(t, "foo", res[0].Repo.Owner)
	})

	t.Run("multiple hits", func(t *testing.T) {
		res, err := db.lookup("h1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=")
		assert.NilError(t, err)
		assert.Equal(t, 2, len(res))
	})

	t.Run("miss", func(t *testing.T) {
		res, err := db.lookup("h1:ccccccccccccccccccccccccccccccccccccccccccc=")
		assert.NilError(t, err)
		assert.Equal(t, 0, len(res))
	})

	t.Run("go.mod sums are not indexed", func(t *testing.T) {
		res, err := db.lookup("h1:gomodgomodgomodgomodgomodgomodgomodgomodgom=")
		assert.NilError(t, err)
		assert.Equal(t, 0, len(res))
	})
}

// TestLookupWithExtraFS ensures that Lookup falls back to the extra FS
// even when the cache directory does not exist (i.e., `update` was never run).
func TestLookupWithExtraFS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent")
	c, err := New(WithDir(dir), WithExtraFS(newTestFS()))
	assert.NilError(t, err)

	ctx := t.Context()
	res, err := c.Lookup(ctx, "h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=")
	assert.NilError(t, err)
	assert.Equal(t, 1, len(res))
	assert.Equal(t, "foo", res[0].Repo.Owner)

	res, err = c.Lookup(ctx, "h1:ccccccccccccccccccccccccccccccccccccccccccc=")
	assert.NilError(t, err)
	assert.Equal(t, 0, len(res))

	_, err = c.Lookup(ctx, "not-an-h1-sum")
	assert.ErrorContains(t, err, "expected h1 sum")
}
