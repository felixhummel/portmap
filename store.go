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
	Port int
	Name string
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
		entries = append(entries, Entry{Port: port, Name: fields[1]})
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
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("%-5d %-*s", e.Port, maxName, e.Name), " "))
	}
	return w.Flush()
}

func findByPort(entries []Entry, port int) (Entry, bool) {
	for _, e := range entries {
		if e.Port == port {
			return e, true
		}
	}
	return Entry{}, false
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

func removeByPort(entries []Entry, port int) ([]Entry, bool) {
	for i, e := range entries {
		if e.Port == port {
			return append(entries[:i], entries[i+1:]...), true
		}
	}
	return entries, false
}

func removeByName(entries []Entry, name string) ([]Entry, bool) {
	for i, e := range entries {
		if e.Name == name {
			return append(entries[:i], entries[i+1:]...), true
		}
	}
	return entries, false
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
