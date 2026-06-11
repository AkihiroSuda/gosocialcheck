package info

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/cacheopt"
	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "info",
		Short:                 "Show cache info",
		Args:                  cobra.NoArgs,
		RunE:                  action,
		DisableFlagsInUseLine: true,
	}
	flags := cmd.Flags()
	flags.Bool("json", false, "JSON output")
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	jsonOut, _ := flags.GetBool("json")
	cacheOpts, err := cacheopt.FromCommand(cmd)
	if err != nil {
		return err
	}
	c, err := cache.New(cacheOpts...)
	if err != nil {
		return err
	}
	s := c.Status()
	w := cmd.OutOrStdout()
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	fmt.Fprintf(w, "Cache mode:     %s\n", s.Mode)
	fmt.Fprintln(w, "Local:")
	fmt.Fprintf(w, "  Path:         %s\n", s.Local.Dir)
	fmt.Fprintf(w, "  Exists:       %t\n", s.Local.Exists)
	if !s.Local.LastUpdated.IsZero() {
		fmt.Fprintf(w, "  Last updated: %s\n", s.Local.LastUpdated.Format(time.RFC3339))
	}
	fmt.Fprintln(w, "Remote:")
	fmt.Fprintf(w, "  URL:          %s\n", s.Remote.URL)
	fmt.Fprintf(w, "  Path:         %s\n", s.Remote.Dir)
	fmt.Fprintf(w, "  Exists:       %t\n", s.Remote.Exists)
	if !s.Remote.LastUpdated.IsZero() {
		fmt.Fprintf(w, "  Last updated: %s\n", s.Remote.LastUpdated.Format(time.RFC3339))
	}
	return nil
}
