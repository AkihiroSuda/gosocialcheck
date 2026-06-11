// Package cacheopt resolves the persistent --cache-mode flag into a
// [cache.Opt] slice that can be passed to [cache.New].
package cacheopt

import (
	"github.com/spf13/cobra"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

// FromCommand reads the persistent --cache-mode flag from cmd and returns
// the matching [cache.Opt]s.
func FromCommand(cmd *cobra.Command) ([]cache.Opt, error) {
	flags := cmd.Flags()
	cacheMode, _ := flags.GetString("cache-mode")
	mode, err := cache.ParseMode(cacheMode)
	if err != nil {
		return nil, err
	}
	return []cache.Opt{cache.WithMode(mode)}, nil
}
