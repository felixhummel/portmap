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

	"github.com/spf13/cobra"
)

var version = "0.3.0"

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

type listeningRow struct {
	Port    int    `json:"port"`
	Host    string `json:"host,omitempty"`
	Name    string `json:"name,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
	Params  string `json:"params,omitempty"`
}

func main() {
	rootCmd := &cobra.Command{
		Use:     "portmap",
		Short:   "Allocate and look up named dev ports",
		Version: version,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				return cmd.Help()
			case 1:
				if !isDNSName(args[0]) {
					return fmt.Errorf("invalid name: %q", args[0])
				}
				setOrGet(args[0], -1)
			case 2:
				if !isPort(args[0]) {
					return fmt.Errorf("usage: portmap [port] <name>")
				}
				port, _ := strconv.Atoi(args[0])
				if !isDNSName(args[1]) {
					return fmt.Errorf("invalid name: %q", args[1])
				}
				setOrGet(args[1], port)
			default:
				return fmt.Errorf("usage: portmap [port] <name>")
			}
			return nil
		},
	}
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	cobra.AddTemplateFunc("nameWithAliases", func(cmd *cobra.Command) string {
		if len(cmd.Aliases) == 0 {
			return cmd.Name()
		}
		return cmd.Name() + "|" + strings.Join(cmd.Aliases, "|")
	})
	rootCmd.SetUsageTemplate(strings.NewReplacer(
		"{{rpad .Name .NamePadding }}", "{{rpad (nameWithAliases .) .NamePadding }}",
	).Replace(rootCmd.UsageTemplate()))

	// ls
	var lsAll, lsIface, lsVerbose bool
	var lsFormat string
	lsCmd := &cobra.Command{
		Use:   "ls [prefix]",
		Short: "List allocated ports (or all listening with -a)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lsFormat != "plain" && lsFormat != "json" {
				return fmt.Errorf("unknown format %q; use plain or json", lsFormat)
			}
			prefix := ""
			if len(args) == 1 {
				prefix = args[0]
			}
			if lsAll {
				listListening(lsFormat, lsVerbose, lsIface)
			} else {
				listStored(prefix)
			}
			return nil
		},
	}
	lsCmd.Flags().BoolVarP(&lsAll, "all", "a", false, "show all listening ports")
	lsCmd.Flags().BoolVarP(&lsIface, "interface", "i", false, "show host/interface column")
	lsCmd.Flags().BoolVarP(&lsVerbose, "verbose", "v", false, "include command params in process column")
	lsCmd.Flags().StringVarP(&lsFormat, "format", "f", "plain", "output format: plain, json")

	// alloc
	var allocStart int
	var allocFormat string
	allocCmd := &cobra.Command{
		Use:     "alloc [name]",
		Aliases: []string{"add"},
		Short:   "Allocate a new port",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if allocFormat != "plain" && allocFormat != "json" {
				return fmt.Errorf("unknown format %q; use plain or json", allocFormat)
			}
			entries, err := load()
			if err != nil {
				return fmt.Errorf("load: %w", err)
			}
			port, ok := allocate(entries, allocStart)
			if !ok {
				return fmt.Errorf("no free port available starting from %d", allocStart)
			}
			name := fmt.Sprintf("alloc-%d", port)
			if len(args) == 1 {
				name = args[0]
				if !isDNSName(name) {
					return fmt.Errorf("invalid name: %q", name)
				}
				if _, ok := findByName(entries, name); ok {
					return fmt.Errorf("duplicate name %q", name)
				}
			}
			entries = upsert(entries, Entry{Port: port, Name: name})
			if err := save(entries); err != nil {
				return fmt.Errorf("save: %w", err)
			}
			switch allocFormat {
			case "json":
				enc := json.NewEncoder(os.Stdout)
				enc.Encode(struct {
					Port int    `json:"port"`
					Name string `json:"name"`
				}{port, name})
			default:
				fmt.Println(port)
			}
			return nil
		},
	}
	allocCmd.Flags().IntVarP(&allocStart, "start", "s", portRangeMin, "minimum port")
	allocCmd.Flags().StringVarP(&allocFormat, "format", "f", "plain", "output format: plain, json")

	// remove
	var removeAll bool
	removeCmd := &cobra.Command{
		Use:     "remove [name|port]",
		Aliases: []string{"rm"},
		Short:   "Free a port by name or port number",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := load()
			if err != nil {
				return fmt.Errorf("load: %w", err)
			}
			if removeAll {
				before := len(entries)
				entries = removeInactive(entries, boundPorts())
				if err := save(entries); err != nil {
					return fmt.Errorf("save: %w", err)
				}
				fmt.Fprintf(os.Stderr, "removed %d inactive entries\n", before-len(entries))
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("usage: portmap remove [-a] <name|port>")
			}
			arg := args[0]
			var ok bool
			if isPort(arg) {
				port, _ := strconv.Atoi(arg)
				entries, ok = removeByPort(entries, port)
				if !ok {
					return fmt.Errorf("port not found: %s", arg)
				}
			} else if isGlob(arg) {
				before := len(entries)
				entries, err = removeByGlob(entries, arg)
				if err != nil {
					return err
				}
				if len(entries) == before {
					return fmt.Errorf("no entries matched %q", arg)
				}
			} else {
				entries, ok = removeByName(entries, arg)
				if !ok {
					return fmt.Errorf("name not found: %q", arg)
				}
			}
			return save(entries)
		},
	}
	removeCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all inactive entries")

	// set
	setCmd := &cobra.Command{
		Use:   "set <name> <port>",
		Short: "Assign a specific port to a name",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !isDNSName(name) {
				return fmt.Errorf("invalid name: %q", name)
			}
			port, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid port: %q", args[1])
			}
			entries, err := load()
			if err != nil {
				return fmt.Errorf("load: %w", err)
			}
			if e, ok := findByName(entries, name); ok && e.Port != port {
				return fmt.Errorf("name %q already allocated to port %d", name, e.Port)
			}
			entries, _ = removeByPort(entries, port)
			entries = upsert(entries, Entry{Port: port, Name: name})
			return save(entries)
		},
	}

	nameCompleter := func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		entries, err := load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var names []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name, toComplete) {
				names = append(names, e.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}

	removeCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		entries, err := load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var completions []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name, toComplete) {
				completions = append(completions, e.Name)
			}
			if strings.HasPrefix(strconv.Itoa(e.Port), toComplete) {
				completions = append(completions, strconv.Itoa(e.Port))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
	setCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nameCompleter(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	rootCmd.AddCommand(lsCmd, allocCmd, removeCmd, setCmd)

	if err := rootCmd.Execute(); err != nil {
		fatalf("%v", err)
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

// procParams returns argv[1:] for the given pid, joined by spaces.
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
