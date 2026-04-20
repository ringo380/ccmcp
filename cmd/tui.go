package cmd

import (
	"context"
	"fmt"

	"github.com/ringo380/ccmcp/internal/tui"
	"github.com/spf13/cobra"
)

var tuiDump bool
var tuiDumpTab string

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive TUI (same as running ccmcp with no args)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if tuiDump {
			return runTUIDump(tuiDumpTab)
		}
		return runTUI(cmd.Context())
	},
}

func runTUI(_ context.Context) error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	proj, err := projectPath()
	if err != nil {
		return err
	}
	return tui.Run(p, proj)
}

// runTUIDump prints the TUI's first render for a given tab (no TTY needed). Diagnostic only.
func runTUIDump(tab string) error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	proj, err := projectPath()
	if err != nil {
		return err
	}
	out, err := tui.Dump(p, proj, tab)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func init() {
	rootCmd.AddCommand(tuiCmd)
	tuiCmd.Flags().BoolVar(&tuiDump, "dump", false, "print the TUI's initial render and exit (no TTY needed)")
	tuiCmd.Flags().StringVar(&tuiDumpTab, "tab", "mcps", "which tab to dump: mcps|plugins|profiles|summary")
}
