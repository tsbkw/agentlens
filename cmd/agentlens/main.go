package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	command := os.Args[1]
	switch command {
	case "version", "--version", "-v":
		fmt.Printf("agentlens version %s\n", version)
	case "help", "--help", "-h":
		printHelp()
	case "list":
		fmt.Println("Listing recorded sessions (Engine initialization in Phase 2)")
	case "graph":
		if len(os.Args) < 3 {
			fmt.Println("Usage: agentlens graph <session-id>")
			os.Exit(1)
		}
		fmt.Printf("Visualizing call graph for session %s (Phase 4 CLI implementation)\n", os.Args[2])
	case "inspect":
		if len(os.Args) < 3 {
			fmt.Println("Usage: agentlens inspect <call-id>")
			os.Exit(1)
		}
		fmt.Printf("Inspecting call node %s (Phase 4 CLI implementation)\n", os.Args[2])
	case "ui":
		fmt.Println("Starting embedded local Web UI server (Phase 5 implementation)")
		fmt.Println("Alternatively, access the free client-side viewer at: https://tsbkw.github.io/agentlens")
	case "watch":
		fmt.Println("Starting live session tailing (Phase 4 implementation)")
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Printf(`AgentLens 🔍 — Generative AI Call Graph Visualizer (v%s)

Usage:
  agentlens <command> [arguments]

Available Commands:
  list                 List recorded AI interaction sessions
  graph <session-id>   Render call graph in terminal with status & anomaly flags
  inspect <call-id>    Display detailed input, output, and anomaly diagnostics
  ui                   Launch local embedded Web UI dashboard
  watch                Live-tail an ongoing AI agent session
  version              Print the version of agentlens
  help                 Print this help message

Documentation & GitHub Pages:
  Free Web Viewer:  https://tsbkw.github.io/agentlens
  Repository:       https://github.com/tsbkw/agentlens
`, version)
}
