// Package cli covers FR-16's cobra RunE heuristic. cobrastub is declared
// "package cobra" (see cobrastub/cobra.go), so it is referred to as cobra
// below, not cobrastub — the real module works the same way.
package cli

import "fixture.example/entrypoints/cobrastub"

func runServe(cmd *cobra.Command, args []string) error { return nil }

// ServeCmd is a cobra.Command composite literal with Use and RunE set — the
// shape FR-16's cobraEntryPoints scan looks for.
var ServeCmd = &cobra.Command{
	Use:  "serve",
	RunE: runServe,
}
