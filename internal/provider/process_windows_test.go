//go:build windows

package provider

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A test runner may already own a console. Use a console-free parent to model
// the production GUI, then verify the actual RPC child rather than just flags.
func TestRPCProcessDoesNotAllocateConsoleFromGUIParent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := rpcConsoleProbeCommand(t, ctx, "parent")
	cmd.Env = append(cmd.Env, "THEONE_RPC_CONSOLE_CHILD="+cmd.Path)
	// Give a private copy of the test executable the same PE GUI subsystem as
	// TheOne. CREATE_NO_WINDOW on a console-subsystem parent alone does not
	// reproduce Windows' child-console allocation behavior for a GUI parent.
	image, err := os.ReadFile(cmd.Path)
	if err != nil {
		t.Fatal(err)
	}
	peOffset := int(binary.LittleEndian.Uint32(image[0x3c:0x40]))
	subsystemOffset := peOffset + 4 + 20 + 68
	if subsystemOffset+2 > len(image) || string(image[peOffset:peOffset+4]) != "PE\x00\x00" {
		t.Fatal("test executable is not a valid Windows PE image")
	}
	binary.LittleEndian.PutUint16(image[subsystemOffset:subsystemOffset+2], 2) // IMAGE_SUBSYSTEM_WINDOWS_GUI
	cmd.Path = filepath.Join(t.TempDir(), "gui-parent.exe")
	if err := os.WriteFile(cmd.Path, image, 0700); err != nil {
		t.Fatal(err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("console-free parent: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "console=0" {
		t.Fatalf("background RPC allocated a console: %s", out)
	}
}

func rpcConsoleProbeCommand(t *testing.T, ctx context.Context, role string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if role == "child" {
		exe = os.Getenv("THEONE_RPC_CONSOLE_CHILD")
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestRPCConsoleProbeHelper$")
	cmd.Env = append(os.Environ(), "THEONE_RPC_CONSOLE_PROBE="+role)
	return cmd
}

func TestRPCConsoleProbeHelper(t *testing.T) {
	role := os.Getenv("THEONE_RPC_CONSOLE_PROBE")
	if role == "" {
		return
	}
	getConsoleWindow := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	console, _, _ := getConsoleWindow.Call()
	if role == "child" {
		fmt.Printf("console=%d\n", console)
		os.Exit(0)
	}
	if role != "parent" || console != 0 {
		t.Fatalf("invalid GUI-parent probe: role=%q console=%d", role, console)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := rpcConsoleProbeCommand(t, ctx, "child")
	configureRPCProcess(child)
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("RPC child: %v: %s", err, out)
	}
	fmt.Print(string(out))
	os.Exit(0)
}
