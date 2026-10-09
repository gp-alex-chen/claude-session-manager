package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeRenameReadsLastTitleIncludingEscapesAndLateMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var transcript strings.Builder
	for _, record := range []map[string]any{
		{"type": "user", "message": map[string]any{"content": "First prompt"}},
		{"type": "ai-title", "aiTitle": "Generated title"},
		{"type": "custom-title", "customTitle": "Old title"},
	} {
		data, _ := json.Marshal(record)
		transcript.Write(data)
		transcript.WriteByte('\n')
	}
	for i := 0; i < 5100; i++ {
		transcript.WriteString(`{"type":"progress"}` + "\n")
	}
	want := `新名字 "quoted" C:\work`
	data, _ := json.Marshal(map[string]string{"type": "custom-title", "customTitle": want})
	transcript.Write(data)
	transcript.WriteString("\n{\"type\":")
	if err := os.WriteFile(path, []byte(transcript.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := parseSessionDetailed(path, "project")
	if got == nil || got.Name != want || got.Text != "First prompt" {
		t.Fatalf("session=%+v err=%v; want latest %q", got, err, want)
	}
	if err == nil {
		t.Fatal("incomplete trailing record must still produce a warning")
	}
}
