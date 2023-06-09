package main

import (
	webhook "github.com/caraml-dev/dap-secret-webhook/cmd/dap-secret-webhook"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{}
	rootCmd.AddCommand(webhook.CmdWebhook)
	if err := rootCmd.Execute(); err != nil {
		panic(err)
	}
}
