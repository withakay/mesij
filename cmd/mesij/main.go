package main

import (
	"context"
	"os"

	"mesij/internal/cli"
)

func main() {
	runner := cli.Runner{Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(runner.Run(context.Background(), os.Args[1:]))
}
