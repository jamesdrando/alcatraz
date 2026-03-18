package main

import (
	"fmt"
	"os"

	"github.com/jamesdrando/alcatraz/internal/mcp"
)

const version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			fmt.Println(`Usage:
  alcatraz-mcp

Runs the Alcatraz MCP server over stdio.`)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", os.Args[1])
			os.Exit(1)
		}
	}

	server := mcp.New("alcatraz-mcp", version)
	if err := server.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[alcatraz-mcp] %s\n", err)
		os.Exit(1)
	}
}
