// Command waffle is the developer CLI for the waffle payment
// orchestration platform: login, keys, balance, charges, payouts. It is
// deliberately its own Go module (see go.mod) alongside sdk/go,
// sdk/typescript, sdk/php, and sdk/mcp — a distributable client tool,
// not a backend binary.
package main

import "github.com/very-good-labs/waffle-cli/internal/cli"

func main() {
	cli.Execute()
}
