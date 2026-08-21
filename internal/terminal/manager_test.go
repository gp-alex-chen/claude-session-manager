package terminal

import "testing"

func TestOpenIDsExcludesTemporaryTokens(t *testing.T) {
	m := NewManager(Callbacks{}, nil)
	m.terms["session-b"] = &ptyRef{}
	m.terms["new-x"] = &ptyRef{}
	m.terms["session-a"] = &ptyRef{}
	got := m.OpenIDs()
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestDecodeInputRejectsInvalidBase64(t *testing.T) {
	if _, err := DecodeInput("!"); err == nil {
		t.Fatal("expected invalid input error")
	}
}
