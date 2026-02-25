package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --- helpers ---

func withTempStore(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	orig := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", dir)
	return func() {
		if orig == "" {
			os.Unsetenv("XDG_CONFIG_HOME")
		} else {
			os.Setenv("XDG_CONFIG_HOME", orig)
		}
	}
}

func mustSave(t *testing.T, entries []Entry) {
	t.Helper()
	if err := save(entries); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func mustLoad(t *testing.T) []Entry {
	t.Helper()
	entries, err := load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return entries
}

// --- isDNSName ---

func TestIsDNSName(t *testing.T) {
	valid := []string{"vite", "api.acme", "db.acme", "worker.acme", "a.b.c.d"}
	for _, s := range valid {
		if !isDNSName(s) {
			t.Errorf("expected %q to be a valid DNS name", s)
		}
	}
	invalid := []string{"", "foo bar", "foo/bar", "foo:8080", "--clean"}
	for _, s := range invalid {
		if isDNSName(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

// --- isPort ---

func TestIsPort(t *testing.T) {
	if !isPort("3000") || !isPort("80") {
		t.Error("expected numeric strings to be ports")
	}
	if isPort("api") || isPort("") || isPort("3000x") {
		t.Error("expected non-numeric strings to not be ports")
	}
}

// --- parseFlags ---

func TestParseFlags(t *testing.T) {
	rem, noIngress := parseFlags([]string{"api.acme", "--no-ingress"})
	if noIngress != true {
		t.Error("expected noIngress=true")
	}
	if len(rem) != 1 || rem[0] != "api.acme" {
		t.Errorf("unexpected remaining args: %v", rem)
	}

	rem, noIngress = parseFlags([]string{"api.acme"})
	if noIngress != false {
		t.Error("expected noIngress=false")
	}
	if len(rem) != 1 {
		t.Errorf("unexpected remaining args: %v", rem)
	}
}

// --- load / save roundtrip ---

func TestLoadEmpty(t *testing.T) {
	defer withTempStore(t)()
	entries := mustLoad(t)
	if len(entries) != 0 {
		t.Errorf("expected empty, got %v", entries)
	}
}

func TestSaveLoad(t *testing.T) {
	defer withTempStore(t)()

	input := []Entry{
		{Port: 3001, Name: "api.acme", Ingress: true},
		{Port: 3002, Name: "db.acme", Ingress: false},
		{Port: 5173, Name: "vite", Ingress: true},
	}
	mustSave(t, input)

	got := mustLoad(t)
	if len(got) != len(input) {
		t.Fatalf("expected %d entries, got %d", len(input), len(got))
	}
	for i, e := range got {
		if e != input[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, e, input[i])
		}
	}
}

func TestSaveLoadComments(t *testing.T) {
	defer withTempStore(t)()

	path := storePath()
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("# comment\n3001  api.acme\n\n3002  db.acme  no-ingress\n"), 0644)

	entries := mustLoad(t)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Port != 3001 || entries[0].Name != "api.acme" || !entries[0].Ingress {
		t.Errorf("unexpected entry 0: %+v", entries[0])
	}
	if entries[1].Port != 3002 || entries[1].Name != "db.acme" || entries[1].Ingress {
		t.Errorf("unexpected entry 1: %+v", entries[1])
	}
}

// --- findByName ---

func TestFindByName(t *testing.T) {
	entries := []Entry{
		{Port: 3001, Name: "api.acme", Ingress: true},
		{Port: 3002, Name: "db.acme", Ingress: false},
	}
	e, ok := findByName(entries, "api.acme")
	if !ok || e.Port != 3001 {
		t.Errorf("expected to find api.acme at 3001, got %+v %v", e, ok)
	}
	_, ok = findByName(entries, "missing")
	if ok {
		t.Error("expected not found")
	}
}

// --- upsert ---

func TestUpsertInsert(t *testing.T) {
	entries := []Entry{{Port: 3001, Name: "api.acme", Ingress: true}}
	entries = upsert(entries, Entry{Port: 3002, Name: "db.acme", Ingress: false})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestUpsertUpdate(t *testing.T) {
	entries := []Entry{{Port: 3001, Name: "api.acme", Ingress: true}}
	entries = upsert(entries, Entry{Port: 3001, Name: "api.acme", Ingress: false})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Ingress {
		t.Error("expected Ingress=false after update")
	}
}

// --- removeInactive ---

func TestRemoveInactive(t *testing.T) {
	entries := []Entry{
		{Port: 3001, Name: "api.acme", Ingress: true},
		{Port: 3002, Name: "db.acme", Ingress: false},
		{Port: 3003, Name: "vite", Ingress: true},
	}
	active := map[int]bool{3001: true, 3003: true}
	result := removeInactive(entries, active)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].Port != 3001 || result[1].Port != 3003 {
		t.Errorf("unexpected result: %+v", result)
	}
}

// --- allocate ---

func TestAllocate(t *testing.T) {
	entries := []Entry{
		{Port: 3000, Name: "a"},
		{Port: 3001, Name: "b"},
	}
	port, ok := allocate(entries)
	if !ok {
		t.Fatal("expected allocation to succeed")
	}
	if port < portRangeMin || port > portRangeMax {
		t.Errorf("port %d out of range", port)
	}
	if port == 3000 || port == 3001 {
		t.Errorf("allocated already registered port %d", port)
	}
}

func TestAllocateExhausted(t *testing.T) {
	var entries []Entry
	for p := portRangeMin; p <= portRangeMax; p++ {
		entries = append(entries, Entry{Port: p, Name: "x"})
	}
	_, ok := allocate(entries)
	if ok {
		t.Error("expected allocation to fail when range is exhausted")
	}
}
