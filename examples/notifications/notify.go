// Build with: go build -o notify.exe examples/notifications/notify.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func send() error {
	url := "http://127.0.0.1:8787/events"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var event struct {
		Version int `json:"schema_version"`
	}
	if err = json.Unmarshal(payload, &event); err != nil {
		return err
	}
	if event.Version != 1 {
		return fmt.Errorf("unsupported TheOne event schema: %d", event.Version)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", response.Status)
	}
	return nil
}

func main() {
	if err := send(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
