package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aitestmanagement/gtms-cli/internal/cli"
	"github.com/aitestmanagement/gtms-cli/internal/output"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.ExecuteContext(ctx); err != nil {
		if !output.IsDisplayed(err) {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		os.Exit(1)
	}
}
