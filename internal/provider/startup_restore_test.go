package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAntigravityStartupProofPinsBinaryDirectoryAndExpiry(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "agy.exe")
	os.WriteFile(bin, []byte("fixture"), 0600)
	id := "01900000-0000-7000-8000-000000000001"
	reads := 0
	p := NewAntigravity(AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, TempRoot: root, HelperExecutable: bin, History: antigravityHistoryFunc(func(context.Context) ([]Session, error) {
		reads++
		return []Session{{Ref: SessionRef{Antigravity, id}, Dir: root}}, nil
	})})
	now := time.Now()
	p.now = func() time.Time { return now }
	_, proof, err := p.ReadStartupHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := OpenRequest{Ref: &SessionRef{Antigravity, id}, Dir: root, RuntimeToken: "new-antigravity-proof"}
	wrong := req
	wrong.Dir = t.TempDir()
	if spec, e := proof.PrepareLaunch(context.Background(), wrong); e == nil {
		spec.Close()
		t.Fatal("different directory accepted")
	}
	original := bin
	bin = filepath.Join(root, "different.exe")
	os.WriteFile(bin, []byte("fixture"), 0600)
	if spec, e := proof.PrepareLaunch(context.Background(), req); e == nil {
		spec.Close()
		t.Fatal("CLI change accepted")
	}
	bin = original
	now = now.Add(3 * time.Minute)
	if spec, e := proof.PrepareLaunch(context.Background(), req); e == nil {
		spec.Close()
		t.Fatal("expired proof accepted")
	}
	if reads != 1 {
		t.Fatal("invalid proof caused another metadata query", reads)
	}
}
