package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

var version = "0.2.0"

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
  -i, --interface         show host/interface column (with -l)
  -v, --verbose           include command params in process column (with -l)
  -f, --format <fmt>      output format: plain, json (default: plain)
      --clean             remove entries whose port is no longer in use
  -h, --help              show this help
`

type listeningRow struct {
	Port    int    `json:"port"`
	Host    string `json:"host,omitempty"`
	Name    string `json:"name,omitempty"`
	Ingress string `json:"ingress,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
	Params  string `json:"params,omitempty"`
}

func main() {
	flags := pflag.NewFlagSet("portmap", pflag.ContinueOnError)
	flags.Usage = func() { fmt.Print(helpText) }

	listening := flags.BoolP("listening", "l", false, "list listening ports")
	iface := flags.BoolP("interface", "i", false, "show host/interface column (with -l)")
	verbose := flags.BoolP("verbose", "v", false, "include command params in process column (with -l)")
	format := flags.StringP("format", "f", "plain", "output format: plain, json")
	help := flags.BoolP("help", "h", false, "show this help")

	if err := flags.Parse(os.Args[1:]); err != nil {
		fatalf("%v", err)
	}

	if *help {
		fmt.Print(helpText)
		return
	}

	rest := flags.Args()

	if *listening {
		if len(rest) != 0 {
			fatalf("usage: portmap -l [-i] [-v] [-f plain|json]")
		}
		if *format != "plain" && *format != "json" {
			fatalf("unknown format %q; use plain or json", *format)
		}
		listListening(*format, *verbose, *iface)
		return
	}

	// portmap --clean
	if len(rest) == 1 && rest[0] == "--clean" {
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
	if len(rest) == 0 {
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

	positional, noIngress := parseFlags(rest)

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

func listListening(format string, verbose bool, showInterface bool) {
	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}
	byPort := map[int]Entry{}
	for _, e := range entries {
		byPort[e.Port] = e
	}
	bindings := listeningPorts()
	procs := socketProcs(bindings)
	docker := dockerPorts()

	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Port != bindings[j].Port {
			return bindings[i].Port < bindings[j].Port
		}
		return bindings[i].Host < bindings[j].Host
	})

	var rows []listeningRow
	seen := map[int]bool{}
	for _, b := range bindings {
		if !showInterface {
			if seen[b.Port] {
				continue
			}
			seen[b.Port] = true
		}
		row := listeningRow{Port: b.Port, Host: b.Host}
		if e, ok := byPort[b.Port]; ok {
			row.Name = e.Name
			row.Ingress = "ingress"
			if !e.Ingress {
				row.Ingress = "no-ingress"
			}
		}
		if p, ok := procs[b.Inode]; ok {
			row.PID = p.PID
			row.Process = p.Name
			if verbose {
				row.Params = procParams(p.PID)
			}
		} else if container, ok := docker[b.Port]; ok {
			row.Process = "docker:" + container
		}
		rows = append(rows, row)
	}

	withPager(func(w io.Writer) {
		renderListening(rows, format, verbose, showInterface, w)
	})
}

// withPager pipes fn's output through $PAGER (default: less -S) when stdout
// is a terminal. Falls back to writing directly if pager can't be started.
func withPager(fn func(io.Writer)) {
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		fn(os.Stdout)
		return
	}

	pagerCmd := os.Getenv("PAGER")
	var cmd *exec.Cmd
	if pagerCmd != "" {
		cmd = exec.Command("sh", "-c", pagerCmd)
	} else {
		cmd = exec.Command("less", "-S")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	pipe, err := cmd.StdinPipe()
	if err != nil {
		fn(os.Stdout)
		return
	}
	if err := cmd.Start(); err != nil {
		fn(os.Stdout)
		return
	}

	fn(pipe)
	pipe.Close()
	cmd.Wait()
}

// procParams returns the command-line arguments (argv[1:]) for the given pid,
// joined by spaces. Returns "" on error or if there are no arguments.
func procParams(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return ""
	}
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[1:], " ")
}

func renderListening(rows []listeningRow, format string, verbose bool, showInterface bool, w io.Writer) {
	// processCol returns the display value for the process column.
	processCol := func(r listeningRow) string {
		if verbose && r.Params != "" {
			return r.Process + " " + r.Params
		}
		return r.Process
	}

	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(rows)
	default: // "plain"
		maxName := 0
		maxHost := 0
		for _, r := range rows {
			if len(r.Name) > maxName {
				maxName = len(r.Name)
			}
			if len(r.Host) > maxHost {
				maxHost = len(r.Host)
			}
		}
		for _, r := range rows {
			pid := ""
			if r.PID != 0 {
				pid = strconv.Itoa(r.PID)
			}
			var line string
			if showInterface {
				line = fmt.Sprintf("%-5d  %-*s  %-*s  %-10s  %-6s  %s", r.Port, maxHost, r.Host, maxName, r.Name, r.Ingress, pid, processCol(r))
			} else {
				line = fmt.Sprintf("%-5d  %-*s  %-10s  %-6s  %s", r.Port, maxName, r.Name, r.Ingress, pid, processCol(r))
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
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
		if explicitPort >= 0 && existing.Port != explicitPort {
			fatalf("duplicate name %s", name)
		}
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
