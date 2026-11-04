// Command solaria plans and applies agent definitions across hosts.
package main

import (
	"fmt"
	"os"

	"github.com/renantarouco/solaria/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
