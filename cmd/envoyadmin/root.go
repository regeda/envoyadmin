package main

import (
	"fmt"

	"github.com/regeda/envoyadmin/internal/config"
	"github.com/spf13/cobra"
)

func newRootCmd(version string) *cobra.Command {
	cfg := config.Default()
	cmd := &cobra.Command{
		Use:           "envoyadmin",
		Short:         "Real-time terminal UI for Envoy",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE:       func(*cobra.Command, []string) error { return cfg.Validate() },
		RunE:          func(cmd *cobra.Command, _ []string) error { return run(cmd.Context(), cfg) },
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Admin, "admin", cfg.Admin, "Envoy admin base URL")
	f.DurationVar(&cfg.Refresh, "refresh", cfg.Refresh, "poll interval (minimum 500ms)")
	f.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "per-request timeout for each admin call")
	f.BoolVar(&cfg.Demo, "demo", false, "run against a built-in fake admin server")

	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	})
	return cmd
}
