// Command acm-mock-resolver resolves and validates OCM/ACM policy templates
// against mock Kubernetes data, without requiring a cluster.
package main

import (
	"fmt"
	"os"

	"github.com/gscode1/gs-acm-utils/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
