package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags:
//
//	go build -ldflags "-X github.com/aitestmanagement/gtms-cli/internal/cli.Version=v0.1.0" ./cmd/gtms
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the GTMS version",
		Long: `Print the GTMS version.

Equivalent to 'gtms --version'. Both forms print a single line: gtms <version>.
Note that -v is the global --verbose flag, not a version shorthand.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("gtms %s\n", Version)
		},
	}
}
