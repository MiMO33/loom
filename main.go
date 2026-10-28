//go:build darwin

package main

import "github.com/yetone/loom/internal/cli"

var version = "dev"

func main() {
	cli.Main(version)
}
