// Command acm-mock-resolver resolves and validates OCM/ACM policy templates
// against mock Kubernetes data, without requiring a cluster.
package main

import (
	"os"

	"github.com/gscode1/gs-acm-utils/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
