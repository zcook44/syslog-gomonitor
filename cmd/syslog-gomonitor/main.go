package main

import (
	"os"

	"syslog-gomonitor/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
