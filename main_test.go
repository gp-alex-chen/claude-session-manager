package main

import (
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/app"
)

func TestWailsOptionsEnableDefaultContextMenu(t *testing.T) {
	opts := newWailsOptions(app.NewApp())
	if !opts.EnableDefaultContextMenu {
		t.Fatal("production Wails options must enable the WebView2 default context menu")
	}
}
