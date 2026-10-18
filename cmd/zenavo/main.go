// Command zenavo is the Zenavo CLI.
package main

import (
	"context"
	"os"

	"github.com/Zenavo-App/zenavo-cli/internal/cli"
)

func main() {
	os.Exit(cli.Main(context.Background(), os.Args[1:]))
}
