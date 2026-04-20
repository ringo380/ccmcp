package cmd

import "github.com/spf13/cobra"

// Back-compat shims preserve the old bash CLI surface so muscle memory keeps working.

var compatApplyCmd = &cobra.Command{
	Use:    "apply <name> [<name>...]",
	Short:  "Back-compat: enable MCPs in the current project (from stash/user scope)",
	Args:   cobra.MinimumNArgs(1),
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		mcpScope = "project"
		mcpFromStash = false
		return runMCPEnable(args)
	},
}

var compatRemoveLocalCmd = &cobra.Command{
	Use:    "remove-local <name> [<name>...]",
	Short:  "Back-compat: remove MCPs from the current project",
	Args:   cobra.MinimumNArgs(1),
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		mcpScope = "project"
		return runMCPDisable(args)
	},
}

var compatClearLocalCmd = &cobra.Command{
	Use:    "clear-local",
	Short:  "Back-compat: clear every MCP in the current project",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		mcpScope = "project"
		mcpAll = true
		return runMCPDisable(nil)
	},
}

var compatStashUserCmd = &cobra.Command{
	Use:    "stash-user",
	Short:  "Back-compat: move all user-scope MCPs into the stash",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcpStashCmd.RunE(cmd, nil)
	},
}

var compatRestoreUserCmd = &cobra.Command{
	Use:    "restore-user",
	Short:  "Back-compat: restore all stashed MCPs to user scope",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcpRestoreCmd.RunE(cmd, nil)
	},
}

var compatSaveProfileCmd = &cobra.Command{
	Use:    "save-profile <name> <mcp> [<mcp>...]",
	Short:  "Back-compat: alias for `profile save`",
	Args:   cobra.MinimumNArgs(2),
	Hidden: true,
	RunE:   func(cmd *cobra.Command, args []string) error { return profileSaveCmd.RunE(cmd, args) },
}

var compatListProfilesCmd = &cobra.Command{
	Use:    "list-profiles",
	Short:  "Back-compat: alias for `profile list`",
	Hidden: true,
	RunE:   func(cmd *cobra.Command, args []string) error { return profileListCmd.RunE(cmd, args) },
}

var compatShowProfileCmd = &cobra.Command{
	Use:    "show-profile <name>",
	Short:  "Back-compat: alias for `profile show`",
	Args:   cobra.ExactArgs(1),
	Hidden: true,
	RunE:   func(cmd *cobra.Command, args []string) error { return profileShowCmd.RunE(cmd, args) },
}

var compatUseProfileCmd = &cobra.Command{
	Use:    "use-profile <name>",
	Short:  "Back-compat: alias for `profile use`",
	Args:   cobra.ExactArgs(1),
	Hidden: true,
	RunE:   func(cmd *cobra.Command, args []string) error { return profileUseCmd.RunE(cmd, args) },
}

func init() {
	rootCmd.AddCommand(
		compatApplyCmd,
		compatRemoveLocalCmd,
		compatClearLocalCmd,
		compatStashUserCmd,
		compatRestoreUserCmd,
		compatSaveProfileCmd,
		compatListProfilesCmd,
		compatShowProfileCmd,
		compatUseProfileCmd,
	)
}
