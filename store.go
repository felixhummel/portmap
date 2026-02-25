package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Entry struct {
	Port    int
	Name    string
	Ingress bool // default true
}

func storePath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "portmap", "ports")
}

func load() ([]Entry, error) {
	path := storePath()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		port, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		e := Entry{Port: port, Name: fields[1], Ingress: true}
		for _, f := range fields[2:] {
			if f == "no-ingress" {
				e.Ingress = false
			}
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

func save(entries []Entry) error {
	path := storePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	// measure max name length for alignment
	maxName := 0
	for _, e := range entries {
		if len(e.Name) > maxName {
			maxName = len(e.Name)
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, e := range entries {
		line := fmt.Sprintf("%-5d %-*s", e.Port, maxName, e.Name)
		if !e.Ingress {
			line += " no-ingress"
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	return w.Flush()
}

func findByName(entries []Entry, name string) (Entry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

func upsert(entries []Entry, e Entry) []Entry {
	for i, existing := range entries {
		if existing.Name == e.Name {
			entries[i] = e
			return entries
		}
	}
	return append(entries, e)
}

func removeInactive(entries []Entry, active map[int]bool) []Entry {
	var result []Entry
	for _, e := range entries {
		if active[e.Port] {
			result = append(result, e)
		}
	}
	return result
}
