package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestSendUTF8JSONToLocalServer(t *testing.T) {
	payload := `{"schema_version":1,"event":"permission_requested","session_name":"中文"}`
	for _, status := range []int{http.StatusAccepted, http.StatusServiceUnavailable} {
		received := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Error("incorrect request", r.Method, r.Header)
			}
			b, _ := io.ReadAll(r.Body)
			received <- string(b)
			w.WriteHeader(status)
		}))
		oldArgs, oldStdin := os.Args, os.Stdin
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = write.WriteString(payload)
		_ = write.Close()
		os.Args = []string{"notify", server.URL}
		os.Stdin = read
		err = send()
		os.Args, os.Stdin = oldArgs, oldStdin
		_ = read.Close()
		server.Close()
		if got := <-received; got != payload {
			t.Fatalf("payload changed: %q", got)
		}
		if (err == nil) != (status == http.StatusAccepted) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}
