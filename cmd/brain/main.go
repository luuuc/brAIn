package main

import (
	"fmt"

	"github.com/luuuc/brain/internal/version"
)

func main() {
	fmt.Printf("brain %s\n", version.Version)
}
