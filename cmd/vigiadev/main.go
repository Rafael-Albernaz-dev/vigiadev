package main

import (
	"os"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
