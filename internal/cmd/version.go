package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/version"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the brain version",
		RunE: func(cmd *cobra.Command, args []string) error {
			printResult(cmd,
				map[string]string{"version": version.Version},
				func() string { return fmt.Sprintf("brain version %s\n", version.Version) },
			)
			return nil
		},
	}
}
