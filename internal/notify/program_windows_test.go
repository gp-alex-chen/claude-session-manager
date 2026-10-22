//go:build windows

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"golang.org/x/sys/windows"
)

func TestNotificationWindowsChildHelper(t *testing.T) {
	mode := ""
	for _, a := range os.Args {
		if a == "--notification-tree-parent" || a == "--notification-tree-child" {
			mode = a
		}
	}
	if mode == "" {
		return
	}
	if mode == "--notification-tree-child" {
		fmt.Fprintln(os.Stdout, os.Getpid())
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		<-ticker.C
		os.Exit(0)
	}
	var input struct {
		Output     string `json:"output"`
		ReadyEvent string `json:"ready_event"`
	}
	if json.NewDecoder(os.Stdin).Decode(&input) != nil {
		os.Exit(20)
	}
	exe, _ := os.Executable()
	child := exec.Command(exe, "-test.run=^TestNotificationWindowsChildHelper$", "--", "--notification-tree-child")
	// Reporting the PID through stdout proves the child reached main before
	// cancellation; no polling, sleep, real CLI or user data is involved.
	pipe, err := child.StdoutPipe()
	if err != nil {
		os.Exit(21)
	}
	if child.Start() != nil {
		os.Exit(22)
	}
	var pid int
	if _, err = fmt.Fscan(pipe, &pid); err != nil {
		os.Exit(23)
	}
	if os.WriteFile(input.Output, []byte(fmt.Sprint(pid)), 0600) != nil {
		os.Exit(24)
	}
	name, _ := windows.UTF16PtrFromString(input.ReadyEvent)
	ready, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		os.Exit(25)
	}
	if windows.SetEvent(ready) != nil {
		os.Exit(26)
	}
	windows.CloseHandle(ready)
	_ = child.Wait()
	os.Exit(0)
}

func TestNotificationWindowsJobKillsDescendants(t *testing.T) {
	// The helper communicates readiness through a named event, then its child
	// PID through a temporary file. Parent cancellation is triggered by the event.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "child.pid")
	c := state.DefaultNotificationConfig()
	c.Program = exe
	c.Args = []string{"-test.run=^TestNotificationWindowsChildHelper$", "--", "--notification-tree-parent"}
	name := fmt.Sprintf("Local\\TheOneNotificationTest-%d-%d", os.Getpid(), time.Now().UnixNano())
	wideName, _ := windows.UTF16PtrFromString(name)
	ready, err := windows.CreateEvent(nil, 1, 0, wideName)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(ready)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]string{"output": output, "ready_event": name})
	results := make(chan ProgramResult, 1)
	go func() { results <- runProgram(ctx, c, payload) }()
	wait, err := windows.WaitForSingleObject(ready, 4000)
	if err != nil || wait != windows.WAIT_OBJECT_0 {
		cancel()
		result := receiveResult(t, results)
		t.Fatalf("helper did not start child: %v %v %#v", wait, err, result)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var pid uint32
	if _, err = fmt.Sscan(string(b), &pid); err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	cancel()
	if result := receiveResult(t, results); result.Success {
		t.Fatal("cancelled process succeeded")
	}
	wait, err = windows.WaitForSingleObject(process, 2000)
	if err != nil || wait != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant survived cancellation: %v %v", wait, err)
	}
}
