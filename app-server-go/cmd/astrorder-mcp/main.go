package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Stderr))
}

// run reports the retirement without reading configuration, credentials, or
// the network. The old MCP stdio executable must fail clearly and point users
// at the supported CLI and HTTP surfaces.
func run(stderr io.Writer) int {
	fmt.Fprintln(stderr, "astrorder-mcp: retired; use the astrorder-agent CLI or the HTTP Agent API")
	return 1
}
