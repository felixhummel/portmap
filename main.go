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

var version = "0.2.1"

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

const helpText = `usage: portmap alloc [--start N] [-f plain|json] <name>
       portmap set <name> <port>
       portmap remove [-a] <name>
       portmap ls [-a] [-i] [-v] [-f plain|json] [prefix]

Allocate and look up named ports.

subcommands:
  alloc, add      allocate a new port
  set             assign a specific port to a name
  remove, rm      free a port by name
  ls              list allocated ports (optional name prefix filter)
  ls -a           list all listening ports with pid and process name

flags (alloc):
  -s, --start N       minimum port to allocate from (default: 3000)
  -f, --format <fmt>  output format: plain, json (default: plain)

flags (remove):
  -a, --all           remove all inactive entries

flags (ls):
  -a, --all           show all listening ports (not just allocated)
  -i, --interface     show host/interface column (with -a)
  -v, --verbose       include command params in process column (with -a)
  -f, --format <fmt>  output format: plain, json (default: plain)
  -h, --help          show this help
`

type listeningRow struct {
	Port    int    `json:"port"`
	Host    string `json:"host,omitempty"`
	Name    string `json:"name,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
	Params  string `json:"params,omitempty"`
}

func main() {
	args := os.Args[1:]

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(helpText)
		return
	}

	switch args[0] {
	case "ls":
		runLS(args[1:])
	case "alloc", "add":
		runAlloc(args[1:])
	case "remove", "rm":
		runRemove(args[1:])
	case "set":
		runSet(args[1:])
	default:
		runDefault(args)
	}
}

func runLS(args []string) {
	flags := pflag.NewFlagSet("portmap ls", pflag.ContinueOnError)
	flags.Usage = func() { fmt.Print(helpText) }

	all := flags.BoolP("all", "a", false, "show all listening ports")
	iface := flags.BoolP("interface", "i", false, "show host/interface column")
	verbose := flags.BoolP("verbose", "v", false, "include command params in process column")
	format := flags.StringP("format", "f", "plain", "output format: plain, json")

	if err := flags.Parse(args); err != nil {
		fatalf("%v", err)
	}
	rest := flags.Args()
	if len(rest) > 1 {
		fatalf("usage: portmap ls [-a] [-i] [-v] [-f plain|json] [prefix]")
	}
	if *format != "plain" && *format != "json" {
		fatalf("unknown format %q; use plain or json", *format)
	}
	prefix := ""
	if len(rest) == 1 {
		prefix = rest[0]
	}

	if *all {
		listListening(*format, *verbose, *iface)
	} else {
		listStored(prefix)
	}
}

func listStored(prefix string) {
	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Port < entries[j].Port })
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, prefix) {
			continue
		}
		fmt.Printf("%-5d  %s\n", e.Port, e.Name)
	}
}

func runAlloc(args []string) {
	flags := pflag.NewFlagSet("portmap alloc", pflag.ContinueOnError)
	flags.Usage = func() { fmt.Print(helpText) }

	start := flags.IntP("start", "s", portRangeMin, "minimum port")
	format := flags.StringP("format", "f", "plain", "output format: plain, json")
	if err := flags.Parse(args); err != nil {
		fatalf("%v", err)
	}
	if *format != "plain" && *format != "json" {
		fatalf("unknown format %q; use plain or json", *format)
	}
	rest := flags.Args()
	if len(rest) != 1 {
		fatalf("usage: portmap alloc [--start N] <name>")
	}
	name := rest[0]
	if !isDNSName(name) {
		fatalf("invalid name: %q", name)
	}

	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}
	if _, ok := findByName(entries, name); ok {
		fatalf("duplicate name %q", name)
	}

	port, ok := allocate(entries, *start)
	if !ok {
		fatalf("no free port available starting from %d", *start)
	}
	if e, ok := findByPort(entries, port); ok {
		fatalf("port %d already allocated to %q", port, e.Name)
	}

	entries = upsert(entries, Entry{Port: port, Name: name})
	if err := save(entries); err != nil {
		fatalf("save: %v", err)
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.Encode(struct {
			Port int    `json:"port"`
			Name string `json:"name"`
		}{port, name})
	default:
		fmt.Println(port)
	}
}

func runRemove(args []string) {
	flags := pflag.NewFlagSet("portmap remove", pflag.ContinueOnError)
	all := flags.BoolP("all", "a", false, "remove all inactive entries")
	if err := flags.Parse(args); err != nil {
		fatalf("%v", err)
	}

	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}

	if *all {
		before := len(entries)
		entries = removeInactive(entries, boundPorts())
		if err := save(entries); err != nil {
			fatalf("save: %v", err)
		}
		fmt.Fprintf(os.Stderr, "removed %d inactive entries\n", before-len(entries))
		return
	}

	rest := flags.Args()
	if len(rest) != 1 {
		fatalf("usage: portmap remove [-a] <name>")
	}
	entries, ok := removeByName(entries, rest[0])
	if !ok {
		fatalf("name not found: %q", rest[0])
	}
	if err := save(entries); err != nil {
		fatalf("save: %v", err)
	}
}

func runSet(args []string) {
	if len(args) != 2 {
		fatalf("usage: portmap set <name> <port>")
	}
	name := args[0]
	if !isDNSName(name) {
		fatalf("invalid name: %q", name)
	}
	port, err := strconv.Atoi(args[1])
	if err != nil {
		fatalf("invalid port: %q", args[1])
	}

	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}
	if e, ok := findByName(entries, name); ok && e.Port != port {
		fatalf("name %q already allocated to port %d", name, e.Port)
	}
	// remove any existing entry for this port (may have a different name)
	entries, _ = removeByPort(entries, port)
	entries = upsert(entries, Entry{Port: port, Name: name})
	if err := save(entries); err != nil {
		fatalf("save: %v", err)
	}
}

func runDefault(args []string) {
	switch len(args) {
	case 1:
		arg := args[0]
		if !isDNSName(arg) {
			fatalf("invalid name: %q", arg)
		}
		setOrGet(arg, -1)

	case 2:
		if isPort(args[0]) {
			port, _ := strconv.Atoi(args[0])
			name := args[1]
			if !isDNSName(name) {
				fatalf("invalid name: %q", name)
			}
			setOrGet(name, port)
		} else {
			fatalf("usage: portmap [port] <name>")
		}

	default:
		fatalf("usage: portmap [port] <name>")
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
				line = fmt.Sprintf("%-5d  %-*s  %-*s  %-6s  %s", r.Port, maxHost, r.Host, maxName, r.Name, pid, processCol(r))
			} else {
				line = fmt.Sprintf("%-5d  %-*s  %-6s  %s", r.Port, maxName, r.Name, pid, processCol(r))
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
}

// setOrGet looks up name; if found, returns existing port. If not found,
// allocates (or uses explicit port) and stores the entry. Prints the port.
func setOrGet(name string, explicitPort int) {
	entries, err := load()
	if err != nil {
		fatalf("load: %v", err)
	}

	if existing, ok := findByName(entries, name); ok {
		if explicitPort >= 0 && existing.Port != explicitPort {
			fatalf("duplicate name %s", name)
		}
		fmt.Println(existing.Port)
		return
	}

	port := explicitPort
	if port < 0 {
		var ok bool
		port, ok = allocate(entries, portRangeMin)
		if !ok {
			fatalf("no free port available in range %d-%d", portRangeMin, portRangeMax)
		}
	}
	if e, ok := findByPort(entries, port); ok {
		fatalf("port %d already allocated to %q", port, e.Name)
	}

	entries = upsert(entries, Entry{Port: port, Name: name})
	if err := save(entries); err != nil {
		fatalf("save: %v", err)
	}
	fmt.Println(port)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "portmap: "+format+"\n", a...)
	os.Exit(1)
}
