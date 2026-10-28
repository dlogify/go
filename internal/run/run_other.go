//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package run

import (
	"os"

	"dlogify.com/sdk/go/internal/bridge"
)

func runCommand(bridge.Client, string, []string) int {
	os.Stderr.WriteString("logify: logify run is not supported on this platform\n")
	return 1
}
