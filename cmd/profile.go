package cmd

import (
	"fmt"
	"strings"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/spf13/cobra"
)

var profileCmd = &cobra.Command{
	Use:     "profile",
	Aliases: []string{"profiles"},
	Short:   "Save, list, and apply named MCP profiles",
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		prof, err := config.LoadProfiles(p.Profiles)
		if err != nil {
			return err
		}
		names := prof.Names()
		if len(names) == 0 {
			fmt.Println("(no profiles saved)")
			return nil
		}
		for _, n := range names {
			mcps, _ := prof.MCPs(n)
			fmt.Printf("  %s: %s\n", n, strings.Join(mcps, ", "))
		}
		return nil
	},
}

var profileShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show the MCP list for a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		prof, err := config.LoadProfiles(p.Profiles)
		if err != nil {
			return err
		}
		mcps, ok := prof.MCPs(args[0])
		if !ok {
			return fmt.Errorf("profile %q not found", args[0])
		}
		for _, m := range mcps {
			fmt.Println("  - " + m)
		}
		return nil
	},
}

var profileSaveCmd = &cobra.Command{
	Use:   "save <name> <mcp> [<mcp>...]",
	Short: "Save a profile (an ordered list of MCP names)",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		prof, err := config.LoadProfiles(p.Profiles)
		if err != nil {
			return err
		}
		name := args[0]
		mcps := args[1:]
		prof.Set(name, mcps)
		if flagDryRun {
			fmt.Printf("[dry-run] would save profile %q = %s\n", name, strings.Join(mcps, ", "))
			return nil
		}
		if err := prof.Save(); err != nil {
			return err
		}
		fmt.Printf("saved profile %q\n", name)
		return nil
	},
}

var profileDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Aliases: []string{"rm", "remove"},
	Short:   "Delete a saved profile",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		prof, err := config.LoadProfiles(p.Profiles)
		if err != nil {
			return err
		}
		if !prof.Delete(args[0]) {
			return fmt.Errorf("profile %q not found", args[0])
		}
		if flagDryRun {
			fmt.Printf("[dry-run] would delete profile %q\n", args[0])
			return nil
		}
		if err := prof.Save(); err != nil {
			return err
		}
		fmt.Printf("deleted profile %q\n", args[0])
		return nil
	},
}

var profileUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Apply a profile: replace current project's MCPs with the profile's list",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, err := projectPath()
		if err != nil {
			return err
		}
		prof, err := config.LoadProfiles(p.Profiles)
		if err != nil {
			return err
		}
		names, ok := prof.MCPs(args[0])
		if !ok {
			return fmt.Errorf("profile %q not found", args[0])
		}

		cj, err := config.LoadClaudeJSON(p.ClaudeJSON)
		if err != nil {
			return err
		}
		stash, err := config.LoadStash(p.Stash)
		if err != nil {
			return err
		}

		// Reset project MCPs, then apply listed names from any available source.
		cj.ClearProjectMCPs(proj)
		var applied, missing []string
		for _, n := range names {
			cfg, found := findMCPConfig(n, cj, stash, proj)
			if !found {
				missing = append(missing, n)
				continue
			}
			cj.SetProjectMCP(proj, n, cfg)
			applied = append(applied, n)
		}

		if flagDryRun {
			fmt.Printf("[dry-run] would apply profile %q to %s: %s\n", args[0], proj, strings.Join(applied, ", "))
			if len(missing) > 0 {
				fmt.Printf("[dry-run] missing: %s\n", strings.Join(missing, ", "))
			}
			return nil
		}
		if err := backupAndSave(p, cj); err != nil {
			return err
		}
		fmt.Printf("applied profile %q to %s: %s\n", args[0], proj, strings.Join(applied, ", "))
		if len(missing) > 0 {
			fmt.Printf("missing (not in stash or user scope): %s\n", strings.Join(missing, ", "))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileListCmd, profileShowCmd, profileSaveCmd, profileUseCmd, profileDeleteCmd)
}
