package main

import (
	"dslq/internal/rpc"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	h, _ := os.UserHomeDir()
	dir := flag.String("data-dir", filepath.Join(h, "Library", "Application Support", "DSL Q"), "private data directory")
	flag.Parse()
	if e := rpc.Serve(os.Stdin, os.Stdout, *dir); e != nil {
		fmt.Fprintln(os.Stderr, "DSL Q core: initialization or communication failed")
		os.Exit(1)
	}
}
