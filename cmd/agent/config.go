package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/a-shan/mysql-pitr/internal/config"
)

// NewConfigCommand creates the `agent config` cobra command tree for
// encrypting and inspecting the agent config file.
func NewConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Encrypt and inspect the agent config file",
	}
	cmd.AddCommand(NewConfigEncryptCommand())
	return cmd
}

// NewConfigEncryptCommand creates the `agent config encrypt` subcommand that
// reads a plaintext JSON config and writes the AES-256-GCM encrypted form
// that `serve` and `flashback --config` load.
func NewConfigEncryptCommand() *cobra.Command {
	var (
		input      string
		output     string
		passphrase string
	)

	cmd := &cobra.Command{
		Use:   "encrypt",
		Short: "Encrypt a plaintext JSON config into the on-disk format",
		Long: `Read a plaintext JSON config (the shape documented in deploy/README.md)
and write the AES-256-GCM encrypted version that the agent loads with
--config. The plaintext file is not modified.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				return fmt.Errorf("--input is required")
			}
			if output == "" {
				return fmt.Errorf("--output is required")
			}
			if passphrase == "" {
				return fmt.Errorf("--passphrase is required")
			}

			raw, err := os.ReadFile(input)
			if err != nil {
				return fmt.Errorf("config encrypt: read input: %w", err)
			}
			// Validate the shape early so a bad config never gets encrypted.
			var cfg config.Config
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return fmt.Errorf("config encrypt: input is not a valid agent config: %w", err)
			}
			if err := config.SaveConfig(output, passphrase, &cfg); err != nil {
				return fmt.Errorf("config encrypt: %w", err)
			}
			fmt.Printf("wrote encrypted config to %s\n", output)
			return nil
		},
		SilenceUsage: true,
	}

	flags := cmd.Flags()
	flags.StringVar(&input, "input", "", "Plaintext JSON config path (required)")
	flags.StringVar(&output, "output", "", "Encrypted config output path (required)")
	flags.StringVar(&passphrase, "passphrase", "", "Passphrase for encryption (required)")

	return cmd
}
