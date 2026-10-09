//go:build windows && integration

package provider

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/windows"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Real bundled SDK/Node/process-job pipeline, fake CLI only. No user account,
// ConPTY, network service, model prompt, or external API is touched.
func TestQuotaHelperWindowsProcessLifecycleIntegration(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js unavailable")
	}
	for _, mode := range []string{"success", "cancel", "kill-helper"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			dir := t.TempDir()
			cli := filepath.Join(dir, "fake-claude.js")
			code := `const net=require('node:net');
const rl=require('node:readline').createInterface({input:process.stdin});
let socket;
rl.on('line',line=>{
 const m=JSON.parse(line);
 if(m.type!=='control_request') process.exit(2);
 const subtype=m.request.subtype;
 const response=subtype==='get_usage'?{subscription_type:'max',rate_limits_available:true,rate_limits:{five_hour:{utilization:20,resets_at:null},seven_day:{utilization:40,resets_at:null}}}:{commands:[],models:[]};
 const reply=()=>process.stdout.write(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:m.request_id,response}})+'\n');
 if(subtype==='get_usage') {
  socket=net.connect({host:'127.0.0.1',port:Number(process.env.QUOTA_FIXTURE_ADDR.split(':').pop())},()=>socket.write(JSON.stringify({pid:process.pid,parent:process.ppid})+'\n',()=>{ if(process.env.QUOTA_FIXTURE_MODE==='success') reply(); }));
  return;
 }
 reply();
});
rl.on('close',()=>{ if(process.env.QUOTA_FIXTURE_MODE==='kill-helper') return; if(socket) socket.end();process.exit(0); });`
			if err = os.WriteFile(cli, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			env := []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + dir, "TMP=" + dir,
				"QUOTA_FIXTURE_ADDR=" + listener.Addr().String(), "QUOTA_FIXTURE_MODE=" + mode}
			result := make(chan error, 1)
			go func() {
				q, err := runClaudeQuotaHelper(ctx, node, cli, env)
				if mode == "success" && (err == nil && len(q.Windows) != 2) {
					err = exec.ErrNotFound
				}
				result <- err
			}()
			listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
			conn, err := listener.Accept()
			if err != nil {
				cancel()
				t.Fatal("fixture never received get_usage", err, <-result)
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var ready struct {
				PID    int `json:"pid"`
				Parent int `json:"parent"`
			}
			if err = json.NewDecoder(conn).Decode(&ready); err != nil {
				t.Fatal(err)
			}
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(ready.PID))
			if err != nil {
				if mode == "success" {
					<-result
					return
				}
				t.Fatal(err)
			}
			defer windows.CloseHandle(handle)
			if mode == "cancel" {
				cancel()
			}
			if mode == "kill-helper" {
				parent, _ := os.FindProcess(ready.Parent)
				if err = parent.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			err = <-result
			if (mode == "success") != (err == nil) {
				t.Fatal("unexpected result", err)
			}
			status, err := windows.WaitForSingleObject(handle, 3000)
			if err != nil || status != windows.WAIT_OBJECT_0 {
				t.Fatalf("orphaned child %d: %d %v", ready.PID, status, err)
			}
		})
	}
}

func TestClaudeNativeQuotaControlIntegration(t *testing.T) {
	if os.Getenv("CLAUDE_QUOTA_NATIVE_INTEGRATION") != "1" {
		t.Skip("set CLAUDE_QUOTA_NATIVE_INTEGRATION=1 for isolated installed-CLI test")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cli, err := ResolveClaudeExecutable(exec.LookPath)
	if err != nil {
		t.Fatal(err)
	}
	var modelRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			modelRequests.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"type":"authentication_error","message":"isolated fixture"}}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + dir, "TMP=" + dir,
		"HOME=" + dir, "USERPROFILE=" + dir, "APPDATA=" + dir, "LOCALAPPDATA=" + dir, "CLAUDE_CONFIG_DIR=" + dir,
		"ANTHROPIC_API_KEY=isolated-test-key", "ANTHROPIC_BASE_URL=" + server.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, err := runClaudeQuotaHelper(ctx, node, cli, env)
	if err != nil || q.Status != "unsupported" || q.AuthMode != "api" || len(q.Windows) != 0 {
		t.Fatal("native quota control", q, err)
	}
	if modelRequests.Load() != 0 {
		t.Fatal("quota query invoked model", modelRequests.Load())
	}
}
