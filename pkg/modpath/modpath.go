package modpath

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
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
