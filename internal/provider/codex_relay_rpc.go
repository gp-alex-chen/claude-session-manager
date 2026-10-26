package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type relayReply struct {
	data json.RawMessage
	err  error
}

// call shares the native transport's reader and write gate. It never resumes
// another thread, answers approval requests, or changes execution permissions.
func (r *codexRelay) call(ctx context.Context, method string, params, result any, guard func() error, afterWrite ...func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return errors.New("Codex 连接已关闭")
	case <-r.ready:
	}
	if err := r.acquireWrite(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		r.releaseWrite()
		return err
	}
	r.mu.Lock()
	if r.closed || r.socket == nil {
		r.mu.Unlock()
		r.releaseWrite()
		return errors.New("目标 Codex 连接已断开")
	}
	r.rpcSequence++
	id := r.rpcPrefix + fmt.Sprint(r.rpcSequence)
	ch := make(chan relayReply, 1)
	r.rpcPending[id] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.rpcPending, id); r.mu.Unlock() }()
	if guard != nil {
		if err := guard(); err != nil {
			r.releaseWrite()
			return err
		}
	}
	payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err == nil {
		err = r.writePacket(ctx, append(payload, '\n'))
	}
	for _, release := range afterWrite {
		release()
	}
	r.releaseWrite()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("接收状态未知，请勿自动重试：%w", ctx.Err())
	case <-r.done:
		return errors.New("连接已断开，接收状态未知，请勿自动重试")
	case reply := <-ch:
		if reply.err != nil {
			return reply.err
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(reply.data, result)
	}
}

func (r *codexRelay) acquireWrite(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return errors.New("Codex 连接已关闭")
	case r.writeGate <- struct{}{}:
		r.mu.Lock()
		closed := r.closed
		r.mu.Unlock()
		if closed {
			r.releaseWrite()
			return errors.New("Codex 连接已关闭")
		}
		if err := ctx.Err(); err != nil {
			r.releaseWrite()
			return err
		}
		return nil
	}
}
func (r *codexRelay) releaseWrite() { <-r.writeGate }
func (r *codexRelay) writePacket(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	written := make(chan error, 1)
	go func() { _, err := r.conn.Write(data); written <- err }()
	select {
	case err := <-written:
		return err
	default:
	}
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		go r.Close()
		return fmt.Errorf("写入未完成，连接将断开；接收状态未知，请勿自动重试：%w", ctx.Err())
	case <-r.done:
		return errors.New("Codex 连接已断开，接收状态未知")
	}
}

// Only the private namespace is reserved. All ordinary native payloads retain
// their exact bytes, including opaque IDs and future protocol fields.
func (r *codexRelay) nativeRequest(data []byte) []byte {
	var header struct {
		ID     json.RawMessage
		Method string
	}
	if json.Unmarshal(data, &header) != nil || header.Method == "" {
		return data
	}
	var id string
	if json.Unmarshal(header.ID, &id) != nil || !strings.HasPrefix(id, r.rpcPrefix) {
		return data
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(data, &msg) != nil {
		return data
	}
	r.mu.Lock()
	r.rpcSequence++
	wireID := r.rpcPrefix + "native-" + fmt.Sprint(r.rpcSequence)
	r.nativeIDs[wireID] = append(json.RawMessage(nil), msg["id"]...)
	r.mu.Unlock()
	msg["id"], _ = json.Marshal(wireID)
	encoded, _ := json.Marshal(msg)
	return encoded
}

func (r *codexRelay) serverReply(data []byte) ([]byte, bool) {
	var header struct {
		ID     json.RawMessage
		Method string
	}
	if json.Unmarshal(data, &header) != nil || header.Method != "" {
		return data, false
	}
	var id string
	if json.Unmarshal(header.ID, &id) != nil || !strings.HasPrefix(id, r.rpcPrefix) {
		return data, false
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(data, &msg) != nil {
		return data, false
	}
	r.mu.Lock()
	original, native := r.nativeIDs[id]
	if native {
		delete(r.nativeIDs, id)
	}
	ch := r.rpcPending[id]
	r.mu.Unlock()
	if native {
		msg["id"] = original
		encoded, _ := json.Marshal(msg)
		return encoded, false
	}
	if ch != nil {
		reply := relayReply{data: msg["result"]}
		if len(msg["error"]) != 0 {
			var rpcError struct{ Message string }
			json.Unmarshal(msg["error"], &rpcError)
			reply.err = fmt.Errorf("Codex 请求失败：%s", rpcError.Message)
		} else if len(reply.data) == 0 {
			reply.err = errors.New("Codex 返回缺少结果")
		}
		select {
		case ch <- reply:
		default:
		}
	}
	// A late response to a cancelled internal request must not reach the TUI.
	return nil, true
}
