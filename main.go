package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
)

func isPort(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func isDNSName(s string) bool {
	if s == "" || s[0] == '-' || s[0] == '.' {
		return false
	}
	for _, c := range s {
		if c == '.' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

func parseFlags(args []string) (remaining []string, noIngress bool) {
	for _, a := range args {
		switch a {
		case "--no-ingress":
			noIngress = true
		default:
			remaining = append(remaining, a)
		}
	}
	return
}

const helpText = `usage: portmap [port] <name> [--no-ingress]
       portmap [flags]

Allocate and look up named ports.

flags:
  -l, --listening         list listening ports with pid and process name
  -f, --format <fmt>      output format: table, plain, json (default: table)
      --clean             remove entries whose port is no longer in use
  -h, --help              show this help
`

type listeningRow struct {
	Port    int    `json:"port"`
	Name    string `json:"name,omitempty"`
	Ingress string `json:"ingress,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
}

func main() {
	args := os.Args[1:]

	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(helpText)
		return
	}

	// Pre-scan for -l/--listening and -f/--format
	listening := false
	format := "table"
	var filtered []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-l", "--listening":
			listening = true
		case "-f", "--format":
			if i+1 < len(args) {
				i++
				format = args[i]
			}
		default:
			filtered = append(filtered, args[i])
		}
	}

	if listening {
		if len(filtered) != 0 {
			fatalf("usage: portmap -l [-f table|plain|json]")
		}
		if format != "table" && format != "plain" && format != "json" {
			fatalf("unknown format %q; use table, plain, or json", format)
		}
		listListening(format)
		return
	}

	// portmap --clean
	if len(filtered) == 1 && filtered[0] == "--clean" {
		entries, err := load()
		if err != nil {
			fatalf("load: %v", err)
		}
		before := len(entries)
		entries = removeInactive(entries, boundPorts())
		if err := save(entries); err != nil {
			fatalf("save: %v", err)
		}
		fmt.Fprintf(os.Stderr, "removed %d inactive entries\n", before-len(entries))
		return
	}

	// portmap (list)
	if len(filtered) == 0 {
		entries, err := load()
		if err != nil {
			fatalf("load: %v", err)
		}
		if len(entries) == 0 {
			return
		}
		maxName := 0
		for _, e := range entries {
			if len(e.Name) > maxName {
				maxName = len(e.Name)
			}
		}
		for _, e := range entries {
			ingress := "ingress"
			if !e.Ingress {
				ingress = "no-ingress"
			}
			fmt.Printf("%-5d  %-*s  %s\n", e.Port, maxName, e.Name, ingress)
		}
		return
	}

	positional, noIngress := parseFlags(filtered)

	switch len(positional) {
	case 1:
		arg := positional[0]
		if !isDNSName(arg) {
			fatalf("invalid name: %q", arg)
		}
		// portmap api.acme [--no-ingress]
		setOrGet(arg, -1, noIngress)

	case 2:
		if isPort(positional[0]) {
			// portmap 5173 vite [--no-ingress]
			port, _ := strconv.Atoi(positional[0])
			name := positional[1]
			if !isDNSName(name) {
				fatalf("invalid name: %q", name)
			}
			setOrGet(name, port, noIngress)
		} else {
			fatalf("usage: portmap [-l] [--clean] [port] <name> [--no-ingress]")
		}

	default:
		fatalf("usage: portmap [-l] [--clean] [port] <name> [--no-ingress]")
	}
}

func listListening(format string) {
	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}
	byPort := map[int]Entry{}
	for _, e := range entries {
		byPort[e.Port] = e
	}
	portInodes := listeningPorts()
	procs := socketProcs(portInodes)
	docker := dockerPorts()

	var rows []listeningRow
	for port := 1; port <= 65535; port++ {
		if _, ok := portInodes[port]; !ok {
			continue
		}
		row := listeningRow{Port: port}
		if e, ok := byPort[port]; ok {
			row.Name = e.Name
			row.Ingress = "ingress"
			if !e.Ingress {
				row.Ingress = "no-ingress"
			}
		}
		if p, ok := procs[port]; ok {
			row.PID = p.PID
			row.Process = p.Name
		} else if container, ok := docker[port]; ok {
			row.Process = "docker:" + container
		}
		rows = append(rows, row)
	}

	renderListening(rows, format, os.Stdout)
}

func renderListening(rows []listeningRow, format string, w io.Writer) {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(rows)
	case "plain":
		maxName := 0
		for _, r := range rows {
			if len(r.Name) > maxName {
				maxName = len(r.Name)
			}
		}
		for _, r := range rows {
			pid := ""
			if r.PID != 0 {
				pid = strconv.Itoa(r.PID)
			}
			line := fmt.Sprintf("%-5d  %-*s  %-10s  %-6s  %s", r.Port, maxName, r.Name, r.Ingress, pid, r.Process)
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	default: // "table"
		t := table.NewWriter()
		t.SetOutputMirror(w)
		t.AppendHeader(table.Row{"PORT", "NAME", "INGRESS", "PID", "PROCESS"})
		for _, r := range rows {
			pid := ""
			if r.PID != 0 {
				pid = strconv.Itoa(r.PID)
			}
			t.AppendRow(table.Row{r.Port, r.Name, r.Ingress, pid, r.Process})
		}
		t.Render()
	}
}

// setOrGet looks up name; if found, returns existing port. If not found,
// allocates (or uses explicit port) and stores the entry. Prints the port.
func setOrGet(name string, explicitPort int, noIngress bool) {
	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}

	if existing, ok := findByName(entries, name); ok {
		// update flags if changed
		changed := existing.Ingress == noIngress // ingress default true, noIngress flips it
		if changed {
			existing.Ingress = !noIngress
			entries = upsert(entries, existing)
			if err := save(entries); err != nil {
				fatalf("save: %v", err)
			}
		}
		fmt.Println(existing.Port)
		return
	}

	port := explicitPort
	if port < 0 {
		var ok bool
		port, ok = allocate(entries)
		if !ok {
			fatalf("no free port available in range %d-%d", portRangeMin, portRangeMax)
		}
	}

	e := Entry{Port: port, Name: name, Ingress: !noIngress}
	entries = upsert(entries, e)
	if err := save(entries); err != nil {
		fatalf("save: %v", err)
	}
	fmt.Println(port)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "portmap: "+format+"\n", a...)
	os.Exit(1)
}
