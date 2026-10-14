// Package cobra is a tiny stand-in for github.com/spf13/cobra: just the
// Command fields FR-16's cobraEntryPoints scan reads.
package cobra

// Command stands in for cobra.Command.
type Command struct {
	Use  string
	Run  func(cmd *Command, args []string)
	RunE func(cmd *Command, args []string) error
}
