package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	// These will be set during build using ldflags
	Version   = "dev"
	CommitSHA = "unknown"
	BuildDate = "unknown"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Display the version, commit SHA, build date, and runtime information for vendoci.`,
	Run:   runVersion,
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

func runVersion(cmd *cobra.Command, args []string) {
	fmt.Printf("vendoci version %s\n", Version)
	fmt.Printf("  Commit:    %s\n", CommitSHA)
	fmt.Printf("  Built:     %s\n", BuildDate)
	fmt.Printf("  Go:        %s\n", runtime.Version())
	fmt.Printf("  Platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
}
