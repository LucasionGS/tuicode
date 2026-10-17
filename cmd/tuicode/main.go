package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"tuicode/internal/ui"
)

const usage = `tuicode - a Visual Studio Code style editor for the terminal

Usage: tuicode [options] [paths...]

  tuicode                       Open the current folder
  tuicode <folder>              Open a folder
  tuicode <file> [file...]      Open files
  tuicode -g <file:line[:col]>  Open a file at a line and column
  tuicode -d <file> <file>      Compare two files
  tuicode -n                    Open with a new untitled file

Options:
  -g, --goto       Open a file at path:line[:character]
  -d, --diff       Compare two files with each other
  -n, --new        Start with a new untitled file
  -r, --readonly   Open files read-only
      --no-lsp     Do not start language servers
  -v, --version    Print version
  -h, --help       Print usage

Run "tuicode --help" for more information.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opts := ui.Options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help" || a == "help":
			fmt.Print(usage)
			return 0
		case a == "-v" || a == "--version" || a == "version":
			fmt.Println(ui.Version)
			return 0
		case a == "-g" || a == "--goto":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Missing argument for --goto. Usage: tuicode -g <file:line[:col]>")
				return 2
			}
			i++
			path, line, col := parseGoto(args[i])
			opts.Paths = append(opts.Paths, path)
			opts.Line, opts.Col = line, col
		case a == "-d" || a == "--diff":
			if i+2 >= len(args) {
				fmt.Fprintln(os.Stderr, "Missing arguments for --diff. Usage: tuicode -d <file> <file>")
				return 2
			}
			opts.Diff = [2]string{args[i+1], args[i+2]}
			i += 2
		case a == "-n" || a == "--new" || a == "--new-window":
			opts.NewFile = true
		case a == "-r" || a == "--readonly":
			opts.ReadOnly = true
		case a == "--no-lsp":
			opts.NoLSP = true
		case a == "-w" || a == "--wait" || a == "--reuse-window":
			// accepted for compatibility with "code"
		case a == "--":
			opts.Paths = append(opts.Paths, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-") && a != "-":
			fmt.Fprintf(os.Stderr, "Unknown option: %s\nRun \"tuicode --help\" for usage.\n", a)
			return 2
		default:
			opts.Paths = append(opts.Paths, a)
		}
	}
	app, err := ui.New(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to start:", err)
		return 1
	}
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to start the terminal UI:", err)
		return 1
	}
	return 0
}

// parseGoto splits "file:line:col".
func parseGoto(s string) (string, int, int) {
	parts := strings.Split(s, ":")
	if len(parts) >= 3 {
		if l, err := strconv.Atoi(parts[len(parts)-2]); err == nil {
			if c, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				return strings.Join(parts[:len(parts)-2], ":"), l, c
			}
		}
	}
	if len(parts) >= 2 {
		if l, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			return strings.Join(parts[:len(parts)-1], ":"), l, 1
		}
	}
	return s, 0, 0
}
