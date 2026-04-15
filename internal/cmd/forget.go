package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/store"
)

func forgetCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "forget <path>",
		Short: "Soft-retire a memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			eng := engineFrom(cmd)

			err := eng.Forget(cmd.Context(), path, reason)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return &ExitError{Code: 2, Err: fmt.Errorf("memory not found: %s", path)}
				}
				return err
			}

			jr := ForgetResult{Path: path, Status: "retired", Reason: reason}

			printResult(cmd, jr, func() string {
				s := fmt.Sprintf("Forgot: %s\n", path)
				if reason != "" {
					s += fmt.Sprintf("Reason: %s\n", reason)
				}
				return s
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "reason for forgetting")
	return cmd
}
