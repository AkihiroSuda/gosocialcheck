package modpath

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/tools/go/analysis"
)

func DirFromFileAndMod(filePath, mod, modVer string) (string, error) {
	modPath := StripMajorVersion(mod)
	modOSPath := filepath.FromSlash(modPath)
	idx := strings.Index(filePath, modOSPath)
	if idx < 0 {
		return "", fmt.Errorf("module path %q not found in file path %q", modOSPath, filePath)
	}
	root := filePath[:idx+len(modOSPath)]
	if strings.Contains(filepath.ToSlash(root), "/pkg/mod/") && modVer != "" {
		root += "@" + modVer
	}
	return root, nil
}

func StripMajorVersion(pkg string) string {
	return regexp.MustCompile(`(/v[0-9]+)$`).ReplaceAllString(pkg, "")
}

func NewGuesser() *Guesser {
	return &Guesser{
		modDirs: make(map[string]string),
	}
}

type Guesser struct {
	modDirs map[string]string // key: MODULE@VER
	mu      sync.RWMutex
}

// GuessModuleDir guess the directory that contains go.mod and go.sum.
// This function might not be robust.
//
// A workaround for https://github.com/golang/go/issues/73878
func (guesser *Guesser) GuessModuleDir(pass *analysis.Pass) (string, error) {
	if pass.Module == nil {
		return "", errors.New("got nil module")
	}
	mod := pass.Module.Path
	modVer := pass.Module.Version
	guesser.mu.RLock()
	k := mod
	if modVer != "" {
		k += "@" + modVer
	}
	v := guesser.modDirs[k]
	guesser.mu.RUnlock()
	if v != "" {
		return v, nil
	}
	if len(pass.Files) == 0 {
		return "", fmt.Errorf("%s: got no files", mod)
	}
	var sawGoBuildDir bool
	for _, f := range pass.Files {
		ff := pass.Fset.File(f.Pos())
		file := ff.Name()
		fileSlash := filepath.ToSlash(file)
		if strings.Contains(fileSlash, "/go-build/") {
			// tmp file like /Users/suda/Library/Caches/go-build/a0/a0f5d4693b09f2e3e24d18608f43e8540c5c52248877ef966df196f36bed5dfb-d
			sawGoBuildDir = true
		}
		if strings.Contains(fileSlash, StripMajorVersion(mod)) {
			dir, err := DirFromFileAndMod(file, mod, modVer)
			if err != nil {
				return "", err
			}
			slog.Debug("guessed module dir", "mod", mod, "modVer", modVer, "dir", dir)
			guesser.mu.Lock()
			guesser.modDirs[k] = dir
			guesser.mu.Unlock()
			return dir, nil
		}
	}
	if sawGoBuildDir {
		return "", nil
	}
	return "", fmt.Errorf("could not guess the directory of module %s", k)
}
