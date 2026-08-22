package main

import (
	"context"
	"os"

	"mesij/internal/cli"
)

func main() {
	runner := cli.Runner{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(runner.Run(context.Background(), os.Args[1:]))
}
