package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

// NewRootCommand creates the root `agent` cobra command with all sub-commands.
func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "MySQL PITR agent — point-in-time recovery via binlog flashback",
		Long: `agent is a MySQL point-in-time recovery (PITR) tool that parses binary
logs and generates reverse SQL statements to roll back changes made
before a specified recovery time.

Sub-commands:
  flashback    Perform offline binlog flashback (local-only, no WebSocket)
  serve        Run as a persistent daemon serving the mysql-pitr-server
  config       Encrypt the agent config file
`,
		SilenceUsage: true,
	}

	cmd.AddCommand(NewFlashbackCommand())
	cmd.AddCommand(NewServeCommand())
	cmd.AddCommand(NewConfigCommand())

	return cmd
}
