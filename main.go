package main

import (
	"fmt"
	"os"
	"strconv"
)

func isPort(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func isDNSName(s string) bool {
	if s == "" {
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

func main() {
	args := os.Args[1:]

	// portmap --clean
	if len(args) == 1 && args[0] == "--clean" {
		entries, err := load()
		if err != nil {
			fatalf("load: %v", err)
		}
		before := len(entries)
		entries = removeInactive(entries)
		if err := save(entries); err != nil {
			fatalf("save: %v", err)
		}
		fmt.Fprintf(os.Stderr, "removed %d inactive entries\n", before-len(entries))
		return
	}

	// portmap (list)
	if len(args) == 0 {
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

	positional, noIngress := parseFlags(args)

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
			fatalf("usage: portmap [port] <name> [--no-ingress]")
		}

	default:
		fatalf("usage: portmap [port] <name> [--no-ingress]")
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

