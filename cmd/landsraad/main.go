package main

import (
	"fmt"
	"os"
)

// Version is the binary version, overridden at release time with -ldflags.
var Version = "dev"

// Exit codes. See the spec, §12.
const (
	exitOK         = 0
	exitUsage      = 1
	exitValidation = 2
)

func main() {
	fmt.Fprintf(os.Stdout, "landsraad %s\n", Version)
	os.Exit(exitOK)
}
