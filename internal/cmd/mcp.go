package cmd

import (
	"log/slog"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/mcp"
)

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP server (stdin/stdout)",
		Long:  "Start an MCP server over stdio. Stdout is reserved for JSON-RPC; all logging goes to stderr.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()

			// Redirect slog to stderr so stdout stays clean for JSON-RPC.
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

			eng := engineFrom(cmd)
			teng := trustEngineFrom(cmd)
			srv := mcp.NewServer(os.Stdin, os.Stdout, eng, teng)
			return srv.Run(ctx)
		},
	}
	return cmd
}
