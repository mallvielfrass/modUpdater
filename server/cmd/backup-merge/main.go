package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"modupdater/internal/chunkbackup"
)

const usageLine = "Использование: backup-merge -milestone <веха.tar.zst> [-snapshot <снапшот.tar.zst>] -out <каталог>"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("backup-merge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	milestone := fs.String("milestone", "", "")
	snapshot := fs.String("snapshot", "", "")
	out := fs.String("out", "", "")
	if err := fs.Parse(args); err != nil || *milestone == "" || *out == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, usageLine)
		return 2
	}
	if err := chunkbackup.Merge(*milestone, *snapshot, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
