//go:build windows && integration

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

// This suite uses only a synthetic executable and stdin. It never starts agy,
// accesses an account or reads the user's CLI history.
func TestAntigravityShellArgumentsIntegration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "路径 %THEONE_EXPAND_ME% & bang!")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "fixture.go")
	code := `package main
import("encoding/json";"fmt";"os")
func main(){data,_:=json.Marshal(os.Args[1:]);if err:=os.WriteFile(os.Getenv("THEONE_TEST_RESULT"),data,0600);err!=nil{panic(err)};fmt.Println("THEONE_ARGV_OK")}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "fixture.exe")
	build := exec.Command("go", "build", "-o", bin, source)
	configureRPCProcess(build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build synthetic fixture: %v %s", err, output)
	}
	for _, shell := range []string{"cmd", "pwsh"} {
		for _, resume := range []bool{false, true} {
			label := shell
			if resume {
				label += "-resume"
			}
			t.Run(label, func(t *testing.T) {
				if _, err := exec.LookPath(shell); err != nil {
					t.Skip(shell + " unavailable")
				}
				settings := filepath.Join(root, "settings %THEONE_EXPAND_ME% 中文 & !.json")
				result := filepath.Join(root, label+"-argv.json")
				args := []string{"--settings", settings}
				id := "01900000-0000-7000-8000-000000000001"
				if resume {
					args = []string{"--conversation", id, "--settings", settings}
				}
				command, err := AntigravityShellCommand(shell, LaunchSpec{Command: "agy", Executable: bin, Args: args})
				if err != nil {
					t.Fatal(err)
				}
				env := append(os.Environ(), "THEONE_AGY_EXE="+bin, "THEONE_AGY_SETTINGS="+settings, "THEONE_TEST_RESULT="+result, "THEONE_EXPAND_ME=SHOULD_NOT_EXPAND", "THEONE_AGY_CONVERSATION="+id)
				p, err := conpty.Start(command, conpty.ConPtyWorkDir(root), conpty.ConPtyEnv(env))
				if err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				ready := make(chan struct{})
				go func() {
					buffer := make([]byte, 8192)
					text := ""
					for {
						n, e := p.Read(buffer)
						text += string(buffer[:n])
						if strings.Contains(text, "THEONE_ARGV_OK") {
							close(ready)
							return
						}
						if e != nil {
							return
						}
					}
				}()
				select {
				case <-ready:
				case <-time.After(15 * time.Second):
					t.Fatal("synthetic argv fixture did not run")
				}
				data, err := os.ReadFile(result)
				if err != nil {
					t.Fatal(err)
				}
				var actual []string
				if json.Unmarshal(data, &actual) != nil || !reflect.DeepEqual(actual, args) {
					t.Fatalf("shell changed arguments: %s", data)
				}
			})
		}
	}
}

func TestAntigravityHostHelperCommitLockIntegration(t *testing.T) {
	host := os.Getenv("THEONE_ANTIGRAVITY_TEST_HOST")
	if host == "" {
		t.Skip("set THEONE_ANTIGRAVITY_TEST_HOST to a built TheOne executable")
	}
	root := t.TempDir()
	path := filepath.Join(root, "snapshot.json")
	if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"working"}`), path); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockAntigravitySnapshot(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	reader, err := openAntigravitySnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shimName := "theone-antigravity-status.cmd"
	if err := os.WriteFile(filepath.Join(root, shimName), []byte("@echo off\r\n@\"%THEONE_AGY_HOST%\" "+AntigravityHelperFlag+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, shimName), []byte("@echo SHADOWED\r\n@exit /b 99\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	event, err := windows.FindFirstChangeNotification(root, false, windows.FILE_NOTIFY_CHANGE_FILE_NAME)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.FindCloseChangeNotification(event)
	cmd := exec.CommandContext(ctx, "cmd", "/d", "/c", shimName)
	cmd.Dir = workspace
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	configureRPCProcess(cmd)
	cmd.Env = append(os.Environ(), "THEONE_AGY_HOST="+host, "THEONE_AGY_SNAPSHOT="+path, "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "NoDefaultCurrentDirectoryInExePath=1")
	cmd.Stdin = strings.NewReader(`{"agent_state":"idle","conversation_title":"Synthetic helper title","email":"MUST_NOT_PERSIST"}`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The child writes its staging file before waiting on our held commit lock.
	// A native change event provides a deterministic handshake without sleeps.
	status, waitErr := windows.WaitForSingleObject(event, 5000)
	reader.Close()
	unlock()
	unlock = nil
	if err := cmd.Wait(); err != nil {
		t.Fatal("host helper failed", err, output.String())
	}
	if waitErr != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("child staging handshake: %d %v", status, waitErr)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "idle") || strings.Contains(string(data), "MUST_NOT_PERSIST") {
		t.Fatal(string(data), err)
	}
}

// Explicit opt-in uses only a separately created synthetic session. Metadata
// for unrelated sessions is neither printed nor persisted by this test.
func TestAntigravityNativeCatalogIntegration(t *testing.T) {
	bin, dir, id := os.Getenv("THEONE_AGY_LIVE_CLI"), os.Getenv("THEONE_AGY_LIVE_CWD"), os.Getenv("THEONE_AGY_LIVE_SESSION")
	if bin == "" || dir == "" || !uuidPattern.MatchString(id) {
		t.Skip("set dedicated THEONE_AGY_LIVE_CLI/CWD/SESSION")
	}
	list, err := antigravityACPForExecutable(bin).ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, err := normalizeAntigravityDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range list {
		if session.Ref.ID == id {
			if !strings.EqualFold(session.Dir, want) || session.Name == "" {
				t.Fatal("target session metadata mismatch")
			}
			t.Log("dedicated native session is present with normalized cwd and title")
			return
		}
	}
	t.Fatal("dedicated session missing from complete native catalog")
}

func TestAntigravityStorePowerShellSettingsVisibilityIntegration(t *testing.T) {
	pwsh, bin, host, root := os.Getenv("THEONE_AGY_STORE_PWSH"), os.Getenv("THEONE_AGY_LIVE_CLI"), os.Getenv("THEONE_ANTIGRAVITY_TEST_HOST"), os.Getenv("THEONE_AGY_SHARED_RUNTIME")
	if pwsh == "" || bin == "" || host == "" || root == "" {
		t.Skip("set explicit Store PowerShell and shared runtime fixture paths")
	}
	p := NewAntigravity(AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: host, TempRoot: root})
	spec, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: "new-antigravity-visibility", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Close()
	script := `if (!(Test-Path -LiteralPath $env:THEONE_AGY_SETTINGS)) {exit 2}; $settings=Get-Content -LiteralPath $env:THEONE_AGY_SETTINGS -Raw | ConvertFrom-Json; if (!(Get-Command $settings.statusLine.command -ErrorAction SilentlyContinue)) {exit 3}; Write-Output VISIBLE_SETTINGS_AND_SHIM`
	cmd := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-Command", script)
	configureRPCProcess(cmd)
	cmd.Env = append(os.Environ(), spec.Env...)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "VISIBLE_SETTINGS_AND_SHIM") {
		t.Fatal("cross-package runtime is inaccessible", err, string(output))
	}
}

// Exercise both launch paths with an inherited opt-in, then check what the
// executable actually receives. No installed CLI, network or user data is used.
func TestAntigravityManagedChildEnvironmentIntegration(t *testing.T) {
	t.Setenv("AGY_CLI_DISABLE_AUTO_UPDATE", "false")
	root := t.TempDir()
	source := filepath.Join(root, "fixture.go")
	code := `package main
import("bufio";"encoding/json";"fmt";"os")
func main(){
 if os.Getenv("AGY_CLI_DISABLE_AUTO_UPDATE")!="true" {os.Exit(42)}
 if len(os.Args)>1 && os.Args[1]=="acp" {
  scanner:=bufio.NewScanner(os.Stdin)
  for scanner.Scan(){
   var req struct {ID int; Method string}; if json.Unmarshal(scanner.Bytes(),&req)!=nil {os.Exit(43)}
   var result any
   switch req.Method {
   case "initialize": result=map[string]any{"protocolVersion":1,"agentCapabilities":map[string]any{"sessionCapabilities":map[string]any{"list":map[string]any{}}}}
   case "session/list": result=map[string]any{"sessions":[]any{}}
   default: os.Exit(44)
   }
   json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc":"2.0","id":req.ID,"result":result})
  };return
 }
 fmt.Println("THEONE_MANAGED_ENV_OK")
}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "fixture.exe")
	build := exec.Command("go", "build", "-o", bin, source)
	configureRPCProcess(build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	t.Run("ACP", func(t *testing.T) {
		list, err := antigravityACPForExecutable(bin).ListSessions(context.Background())
		if err != nil || len(list) != 0 {
			t.Fatalf("managed ACP environment: %v %v", list, err)
		}
	})
	id := "01900000-0000-7000-8000-000000000001"
	adapter := NewAntigravity(AntigravityConfig{
		Lookup: func(string) (string, error) { return bin, nil }, TempRoot: root, HelperExecutable: bin,
		History: antigravityHistoryFunc(func(context.Context) ([]Session, error) {
			return []Session{{Ref: SessionRef{Antigravity, id}, Dir: root}}, nil
		}),
	})
	for _, shell := range []string{"cmd", "pwsh"} {
		for _, resume := range []bool{false, true} {
			label := shell + "-new"
			if resume {
				label = shell + "-resume"
			}
			t.Run(label, func(t *testing.T) {
				if _, err := exec.LookPath(shell); err != nil {
					t.Skip(shell + " unavailable")
				}
				req := OpenRequest{Dir: root, RuntimeToken: "new-antigravity-" + label}
				if resume {
					req.Ref = &SessionRef{Antigravity, id}
				}
				spec, err := adapter.PrepareLaunch(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				defer spec.Close()
				command, err := AntigravityShellCommand(shell, spec)
				if err != nil {
					t.Fatal(err)
				}
				// Match Manager's override semantics; a raw Windows environment block may
				// contain duplicates, whose resolution depends on the receiving runtime.
				env := []string{}
				for _, entry := range os.Environ() {
					key, _, _ := strings.Cut(entry, "=")
					overridden := false
					for _, override := range spec.Env {
						k, _, _ := strings.Cut(override, "=")
						if strings.EqualFold(k, key) {
							overridden = true
							break
						}
					}
					if !overridden {
						env = append(env, entry)
					}
				}
				env = append(env, spec.Env...)
				p, err := conpty.Start(command, conpty.ConPtyWorkDir(root), conpty.ConPtyEnv(env))
				if err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				ready := make(chan bool, 1)
				go func() {
					buffer := make([]byte, 8192)
					var output strings.Builder
					for {
						n, e := p.Read(buffer)
						output.Write(buffer[:n])
						if strings.Contains(output.String(), "THEONE_MANAGED_ENV_OK") {
							ready <- true
							return
						}
						if e != nil {
							ready <- false
							return
						}
					}
				}()
				select {
				case ok := <-ready:
					if !ok {
						t.Fatal("managed child rejected updater environment")
					}
				case <-time.After(10 * time.Second):
					t.Fatal("managed child did not confirm updater environment")
				}
			})
		}
	}
	if os.Getenv("AGY_CLI_DISABLE_AUTO_UPDATE") != "false" {
		t.Fatal("parent environment was changed")
	}
}

// This opt-in reads native metadata only. It proves that repeated fresh
// catalogs use one actual process, then verifies shutdown reaps it.
func TestAntigravitySharedMetadataProcessIntegration(t *testing.T) {
	bin, id := os.Getenv("THEONE_AGY_LIVE_CLI"), os.Getenv("THEONE_AGY_LIVE_SESSION")
	if bin == "" || !uuidPattern.MatchString(id) {
		t.Skip("set explicit dedicated CLI/SESSION fixtures")
	}
	p := NewAntigravity(AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }})
	defer p.CloseService()
	var initial *antigravityACPConnection
	for i := 0; i < 3; i++ {
		begin := time.Now()
		rows, err := p.readHistory(context.Background(), true, bin)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.Ref.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("dedicated session missing")
		}
		p.metadata.mu.Lock()
		entry := p.metadata.current
		p.metadata.mu.Unlock()
		if i == 0 {
			initial = entry
		} else if entry != initial {
			t.Fatal("fresh lookup restarted the native ACP process")
		}
		t.Logf("fresh catalog %d: %s; same native process=%t", i+1, time.Since(begin).Round(time.Millisecond), entry == initial)
	}
	if initial == nil {
		t.Fatal("no native ACP instance")
	}
	native, ok := initial.conn.(*processConnection)
	if !ok || native.cmd.Process.Pid == 0 {
		t.Fatal("not a real process transport")
	}
	p.CloseService()
	if native.cmd.ProcessState == nil || !native.cmd.ProcessState.Exited() {
		t.Fatal("ACP remained alive after CloseService")
	}
	p.BeginService()
	if _, err := p.ListSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.metadata.mu.Lock()
	fresh := p.metadata.current
	p.metadata.mu.Unlock()
	if fresh == nil || fresh == initial {
		t.Fatal("new lifecycle reused old connection/cache")
	}
	t.Log("closed metadata process was reaped; new lifecycle created a fresh instance")
}

type antigravityCancelAfterListWrite struct {
	*processConnection
	cancel context.CancelFunc
	once   sync.Once
}

func (c *antigravityCancelAfterListWrite) Write(data []byte) (int, error) {
	n, err := c.processConnection.Writer.Write(data)
	var req struct{ Method string }
	if json.Unmarshal(data, &req) == nil && req.Method == "session/list" {
		c.once.Do(c.cancel)
	}
	return n, err
}

func TestAntigravitySharedMetadataAbortProcessIntegration(t *testing.T) {
	bin, id := os.Getenv("THEONE_AGY_LIVE_CLI"), os.Getenv("THEONE_AGY_LIVE_SESSION")
	if bin == "" || !uuidPattern.MatchString(id) {
		t.Skip("set dedicated CLI/SESSION fixtures")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var aborted *processConnection
	opens := 0
	service := newSharedAntigravityACP(func(ctx context.Context, bin string) (io.ReadWriteCloser, error) {
		conn, err := openAntigravityACPProcess(ctx, bin)
		if err != nil {
			return nil, err
		}
		opens++
		if opens == 1 {
			aborted = conn.(*processConnection)
			return &antigravityCancelAfterListWrite{processConnection: aborted, cancel: cancel}, nil
		}
		return conn, nil
	})
	defer service.CloseService()
	if list, err := service.ListSessions(ctx, bin); !errors.Is(err, context.Canceled) || list != nil {
		t.Fatal("native cancellation did not fail closed", err)
	}
	if aborted == nil || aborted.cmd.ProcessState == nil || !aborted.cmd.ProcessState.Exited() {
		t.Fatal("native Abort left the metadata process alive")
	}
	rows, err := service.ListSessions(context.Background(), bin)
	if err != nil || opens != 2 {
		t.Fatal("native service failed to rebuild", opens, err)
	}
	found := false
	for _, row := range rows {
		if row.Ref.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("dedicated session missing after recovery")
	}
	t.Log("cancelled native metadata process was reaped; next request rebuilt successfully")
}
