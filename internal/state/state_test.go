package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTripAndAtomicFormat(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	st := &FavState{Ids: []string{"x"}, Aliases: map[string]string{"x": "demo"}, Hidden: []string{"y"}}
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	got := s.Load()
	if got.Aliases["x"] != "demo" || got.Hidden[0] != "y" {
		t.Fatalf("unexpected state: %+v", got)
	}
	if err := s.SaveOpen([]string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadOpen(); len(got) != 2 || got[0] != "a" {
		t.Fatalf("open sessions not sorted: %v", got)
	}
	if err := s.SaveShell("pwsh"); err != nil {
		t.Fatal(err)
	}
	if s.Shell() != "pwsh" {
		t.Fatal("shell roundtrip failed")
	}
	b, err := os.ReadFile(filepath.Join(dir, "favorites.json"))
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if json.Unmarshal(b, &shape) != nil {
		t.Fatal("invalid json")
	}
	if _, ok := shape["ids"]; !ok {
		t.Fatal("missing ids")
	}
}
