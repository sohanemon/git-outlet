package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sohanemon/git-outlet/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		if errors.Is(err, cli.ErrAborted) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
