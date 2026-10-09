package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPreferencesSurviveRestartAndPreserveOtherKeys(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	for key, value := range map[string]string{"ui-theme": "dark", "term-theme": "nord", "term-font-size": "18", "terminal-layout-mode": "grid-2x2"} {
		if err := s.SetPreference(key, value); err != nil {
			t.Fatal(err)
		}
	}
	restarted := NewStore(dir)
	if err := restarted.SetPreference("term-font-size", "20"); err != nil {
		t.Fatal(err)
	}
	values, err := NewStore(dir).Preferences()
	if err != nil || values["ui-theme"] != "dark" || values["term-theme"] != "nord" || values["term-font-size"] != "20" || values["terminal-layout-mode"] != "grid-2x2" {
		t.Fatalf("preferences=%v err=%v", values, err)
	}
}

func TestProviderFilterPreferenceValidationAndCanonicalPersistence(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	for _, value := range []string{`null`, `[]`, `{}`, `[1]`, `["bad provider"]`, `["Claude"]`, `["codex:uuid"]`} {
		if err := s.SetPreference("session-provider-filter", value); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	if err := s.SetPreference("session-provider-filter", `["codex","claude","codex"]`); err != nil {
		t.Fatal(err)
	}
	values, err := NewStore(dir).Preferences()
	if err != nil || values["session-provider-filter"] != `["claude","codex"]` {
		t.Fatalf("preferences=%v err=%v", values, err)
	}
}

func TestPreferencePatchesFromSeparateStoresDoNotLoseKeys(t *testing.T) {
	dir := t.TempDir()
	a, b := NewStore(dir), NewStore(dir)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := a.SetPreference("ui-theme", "dark"); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := b.SetPreference("term-theme", "dracula"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	values, err := a.Preferences()
	if err != nil || values["ui-theme"] != "dark" || values["term-theme"] != "dracula" {
		t.Fatalf("lost patches: %v %v", values, err)
	}
}

func TestSessionPatchesFromSeparateStoresPreserveAliasesAndHidden(t *testing.T) {
	dir := t.TempDir()
	a, b := NewStore(dir), NewStore(dir)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := a.SetAlias("one", "Name"); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := b.SetHidden("two", true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := a.Load()
	if err != nil || got.Aliases["one"] != "Name" || !got.HiddenSet()["two"] {
		t.Fatalf("lost session patches: %+v %v", got, err)
	}
}

func TestPreferenceValidationAndCorruptFilePreservation(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	for _, item := range [][2]string{{"unknown", "x"}, {"ui-theme", "sepia"}, {"term-theme", "missing"}, {"term-font-size", "NaN"}, {"terminal-layout-mode", "invalid"}, {"terminal-layout-split-ratios", "{"}} {
		if err := s.SetPreference(item[0], item[1]); err == nil {
			t.Fatalf("accepted invalid preference %v", item)
		}
	}
	if err := s.SetPreference("term-font-size", "18.6"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPreference("terminal-layout-split-ratios", `{"grid-2x2":{"vertical":{"first":9},"horizontal":{"first":-2}}}`); err != nil {
		t.Fatal(err)
	}
	values, _ := s.Preferences()
	if values["term-font-size"] != "19" {
		t.Fatal(values)
	}
	if values["terminal-layout-split-ratios"] != `{"grid-2x2":{"horizontal":{"first":0.16},"vertical":{"first":0.84}}}` {
		t.Fatal(values)
	}
	path := filepath.Join(dir, "preferences.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preferences(); err == nil {
		t.Fatal("corrupt preferences accepted")
	}
	if err := s.SetPreference("ui-theme", "dark"); err == nil {
		t.Fatal("corrupt preferences overwritten")
	}
	if data, _ := os.ReadFile(path); string(data) != "{" {
		t.Fatal("corrupt source changed")
	}
}
