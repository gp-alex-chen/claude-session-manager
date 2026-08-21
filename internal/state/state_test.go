package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestCompatibleRoundTripPreservesAllFields(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(&FavState{Ids: []string{"favorite"}, Aliases: map[string]string{"old": "name"}, Hidden: []string{"hidden"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias("new", "alias"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHidden("archived", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveOpen([]string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetShell("pwsh"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Ids, []string{"favorite"}) || got.Aliases["new"] != "alias" || !got.HiddenSet()["archived"] {
		t.Fatalf("state not preserved: %+v", got)
	}
	ids, err := s.LoadOpen()
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("open ids=%v err=%v", ids, err)
	}
	shell, err := s.Shell()
	if err != nil || shell != "pwsh" {
		t.Fatalf("shell=%q err=%v", shell, err)
	}

	var fav map[string]any
	b, _ := os.ReadFile(filepath.Join(s.dir, "favorites.json"))
	if err := json.Unmarshal(b, &fav); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ids", "aliases", "hidden"} {
		if _, ok := fav[key]; !ok {
			t.Fatalf("favorites missing %s", key)
		}
	}
	var open map[string]any
	b, _ = os.ReadFile(filepath.Join(s.dir, "open-sessions.json"))
	if err := json.Unmarshal(b, &open); err != nil {
		t.Fatal(err)
	}
	if _, ok := open["ids"]; !ok {
		t.Fatal("open sessions missing ids")
	}
}

func TestMutationsAreConcurrentAndIdempotent(t *testing.T) {
	s := NewStore(t.TempDir())
	const n = 60
	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		id := "alias-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		go func() {
			defer wg.Done()
			if err := s.SetAlias(id, "value"); err != nil {
				t.Errorf("alias: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.SetHidden(id, true); err != nil {
				t.Errorf("hidden: %v", err)
			}
		}()
	}
	wg.Wait()
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Aliases) != n || len(got.Hidden) != n {
		t.Fatalf("lost concurrent updates aliases=%d hidden=%d", len(got.Aliases), len(got.Hidden))
	}
	for i := 0; i < n; i++ {
		id := "same"
		if err := s.SetHidden(id, true); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.Load()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, id := range got.Hidden {
		if id == "same" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate hidden entries=%d", count)
	}
	if err := s.SetHidden("same", false); err != nil {
		t.Fatal(err)
	}
}

func TestSaveOpenSortsWithoutMutatingInput(t *testing.T) {
	s := NewStore(t.TempDir())
	input := []string{"b", "a", "c"}
	want := append([]string(nil), input...)
	if err := s.SaveOpen(input); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input mutated: %v", input)
	}
	got, err := s.LoadOpen()
	if err != nil || !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestCorruptFilesReturnSafeDefaultsAndErrors(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := os.WriteFile(s.favPath(), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	fav, err := s.Load()
	if err == nil || fav == nil || fav.Aliases == nil {
		t.Fatalf("favorites corruption fav=%+v err=%v", fav, err)
	}
	if err := os.WriteFile(s.openPath(), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	ids, err := s.LoadOpen()
	if err == nil || ids != nil {
		t.Fatalf("open corruption ids=%v err=%v", ids, err)
	}
	if err := os.WriteFile(s.shellPath(), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	shell, err := s.Shell()
	if err == nil || shell != "cmd" {
		t.Fatalf("shell corruption shell=%q err=%v", shell, err)
	}
}

func TestAtomicWritesLeaveNoTemporaryFilesAndReportConflicts(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.SetAlias("id", "name"); err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, ".state-*")); len(files) != 0 {
		t.Fatalf("temporary files remain: %v", files)
	}
	if err := os.Remove(s.favPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.favPath(), 0755); err != nil {
		t.Fatal(err)
	}
	err := s.SetAlias("id", "new")
	if err == nil {
		t.Fatal("directory conflict should return error")
	}
	if files, _ := filepath.Glob(filepath.Join(dir, ".state-*")); len(files) != 0 {
		t.Fatalf("temporary files remain after failure: %v", files)
	}
}
