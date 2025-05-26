package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"

	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/commands/lookup"
	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/commands/run"
	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/commands/update"
	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/envutil"
	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/version"
)

var logLevel = new(slog.LevelVar)

func main() {
	logHandler := tint.NewHandler(os.Stderr, &tint.Options{
		Level:      logLevel,
		TimeFormat: time.Kitchen,
	})
	slog.SetDefault(slog.New(logHandler))
	cmd := newRootCommand()
	if err := cmd.Execute(); err != nil {
		slog.Error("exiting with an error: " + err.Error())
		os.Exit(1)
	}
}

const example = `
  # Set the token if facing the GitHub API rate limit (see README.md)
  export GITHUB_TOKEN=...

  gosocialcheck update

  gosocialcheck run ./...
`

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "gosocialcheck",
		Short:         "Social reputation checker for Go modules",
		Example:       example,
		Version:       version.GetVersion(),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	flags := cmd.PersistentFlags()
	flags.Bool("debug", envutil.Bool("DEBUG", false), "debug mode [$DEBUG]")
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()
		if debug, _ := flags.GetBool("debug"); debug {
			logLevel.Set(slog.LevelDebug)
		}
		return nil
	}

	cmd.AddCommand(
		update.New(),
		lookup.New(),
		run.New(),
	)
	return cmd
}
