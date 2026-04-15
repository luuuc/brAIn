package main

import (
	"os"

	"github.com/luuuc/brain/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
