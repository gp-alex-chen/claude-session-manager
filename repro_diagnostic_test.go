package main

// 回归测试：验证"恢复第二个会话"双击关闭不再崩溃。
// 场景：会话A(长任务) 读协程阻塞 -> 启动会话B 强关A（killLocked 路径）
//       -> A读协程退出后收尾 close（此前会导致 A 被关闭两次，
//          句柄值被 B 复用 -> 堆损坏 0xc0000374 闪退）。
// 修复后 ptyRef.close() 幂等，Close 只执行一次。

import (
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
)

// readerLoop 模拟 app.go 中 startPTY 的读协程
func readerLoop(t *testing.T, r *ptyRef, done chan<- struct{}) {
	buf := make([]byte, 8192)
	for {
		n, err := r.pty.Read(buf)
		if n > 0 {
			_ = n
		}
		if err != nil {
			t.Logf("reader read error: %v", err)
			break
		}
	}
	t.Log("reader closing ptyRef (收尾)")
	r.close() // 修复点：幂等
	close(done)
}

func TestPtyRefNoDoubleClose(t *testing.T) {
	// 会话 A：长任务，保持存活
	p1, err := conpty.Start(`cmd /c echo FIRST-SESSION & ping -n 20 127.0.0.1 >nul`,
		conpty.ConPtyDimensions(120, 32), conpty.ConPtyWorkDir(`C:\`))
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	r1 := &ptyRef{pty: p1}
	t.Logf("session A started, pid=%d", r1.pid())

	done := make(chan struct{})
	go readerLoop(t, r1, done)

	time.Sleep(1200 * time.Millisecond)

	// killLocked 路径：强关 A
	t.Log("killLocked closing r1")
	r1.close()

	// 启动会话 B（此前 A 的句柄值会被 B 复用）
	p2, err := conpty.Start(`cmd /c echo SECOND-SESSION`,
		conpty.ConPtyDimensions(120, 32), conpty.ConPtyWorkDir(`C:\`))
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	r2 := &ptyRef{pty: p2}
	t.Logf("session B started, pid=%d", r2.pid())

	select {
	case <-done:
		t.Log("A reader exited cleanly")
	case <-time.After(3 * time.Second):
		t.Fatal("A reader STILL blocked")
	}

	// 再关一次 r1：必须为幂等空操作（旧代码第二次 Close 的等价场景）
	r1.close()

	// B 必须仍然可用
	time.Sleep(300 * time.Millisecond)
	if n, err := r2.write([]byte("echo OK-MARKER\r\n")); err != nil || n == 0 {
		t.Fatalf("B write FAILED (n=%d err=%v) <- 句柄被破坏", n, err)
	}
	t.Log("B write OK")
	r2.close()
	t.Log("PASS: no crash, no double close")
}