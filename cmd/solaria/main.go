// Command solaria plans and applies agent definitions across hosts.
package main

import (
	"os"

	"github.com/solaria-sh/solaria/internal/cli"
)

func main() {
	os.Exit(cli.Main())
}
