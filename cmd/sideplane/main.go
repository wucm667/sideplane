package main

import (
	"flag"
	"fmt"
	"os"
)

const version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sideplane %s\n", version)
		return
	}

	fmt.Fprintln(os.Stdout, "sideplane CLI skeleton")
}
