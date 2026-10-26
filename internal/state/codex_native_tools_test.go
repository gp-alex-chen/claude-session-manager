package state

import "testing"

func TestCodexNativeToolsCapabilitySurvivesFreshStoreWithoutGrants(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	id := "01900000-0000-7000-8000-000000000001"
	if err := s.SaveCodexNativeTools(id, "theone_threads/v1"); err != nil {
		t.Fatal(err)
	}
	restored, err := NewStore(dir).LoadCodexNativeTools()
	if err != nil || len(restored) != 1 || restored[id] != "theone_threads/v1" {
		t.Fatal(restored, err)
	}
	if err := s.SaveCodexNativeTools("not-a-session", "theone_threads/v1"); err == nil {
		t.Fatal("invalid capability ID accepted")
	}
}
