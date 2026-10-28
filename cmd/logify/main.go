// Command logify runs a command and sends its output to dlogify:
//
//	logify run -- <command> [args...]
//
// Install it with go install dlogify.com/sdk/go/cmd/logify@latest.
package main

import (
	"os"

	_ "dlogify.com/sdk/go" // fills internal/bridge
	"dlogify.com/sdk/go/internal/run"
)

func main() {
	os.Exit(run.Main(os.Args[1:]))
}
