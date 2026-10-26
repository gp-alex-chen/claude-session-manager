package provider

import (
	"context"
	"errors"
	"time"
)

func (c *CodexRPC) ReadConversation(parent context.Context, id, cursor string) (ReferencePage, error) {
	if err := validateRef(SessionRef{Codex, id}); err != nil {
		return ReferencePage{}, err
	}
	if len(cursor) > 4096 {
		return ReferencePage{}, errors.New("分页游标过长")
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	conn, err := c.Open(ctx)
	if err != nil {
		return ReferencePage{}, err
	}
	defer conn.Close()
	rpc := newRPCSession(ctx, conn)
	var initialized map[string]any
	if err = rpc.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "theone_referenced_history", "version": "1"}}, &initialized); err != nil {
		return ReferencePage{}, err
	}
	if _, err = conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		return ReferencePage{}, err
	}
	params := map[string]any{"threadId": id, "limit": 5, "sortDirection": "desc", "itemsView": "full"}
	if cursor != "" {
		params["cursor"] = cursor
	}
	var page struct {
		Data []struct {
			ID    string
			Items []struct {
				Type    string
				Text    string
				Content []struct {
					Type string
					Text string
				}
			}
		}
		NextCursor *string
	}
	if err = rpc.call("thread/turns/list", params, &page); err != nil {
		return ReferencePage{}, err
	}
	if page.Data == nil {
		return ReferencePage{}, errors.New("Codex 未返回会话历史页；请确认 CLI 支持 thread/turns/list")
	}
	result := ReferencePage{Messages: []ReferenceText{}, NextCursor: page.NextCursor}
	remaining := 48 << 10
	for i := len(page.Data) - 1; i >= 0; i-- {
		turn := page.Data[i]
		for _, item := range turn.Items {
			role, text := "", ""
			switch item.Type {
			case "userMessage":
				role = "user"
				for _, part := range item.Content {
					if part.Type == "text" {
						text += part.Text + "\n"
					}
				}
			case "agentMessage":
				role = "assistant"
				text = item.Text
			default:
				continue
			}
			runes := []rune(text)
			if len(runes) > remaining {
				runes = runes[:remaining]
				result.Truncated = true
			}
			remaining -= len(runes)
			if len(runes) > 0 {
				result.Messages = append(result.Messages, ReferenceText{Role: role, Text: string(runes), TurnID: turn.ID})
			}
		}
	}
	return result, nil
}
