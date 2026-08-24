// Package usage parses token usage records written by Claude Code sessions.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Usage is the token usage for one assistant request.
//
// ThinkingTokens is an output detail and is intentionally not added again to
// OutputTokens when calculating totals.
type Usage struct {
	InputTokens                int64 `json:"input_tokens"`
	OutputTokens               int64 `json:"output_tokens"`
	CacheCreationInputTokens   int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens       int64 `json:"cache_read_input_tokens"`
	ThinkingTokens             int64 `json:"thinking_tokens"`
	CacheCreation5mInputTokens int64 `json:"cache_creation_5m_input_tokens"`
	CacheCreation1hInputTokens int64 `json:"cache_creation_1h_input_tokens"`
}

// PromptTokens returns the complete prompt token count, including cache
// creation and cache reads.
func (u Usage) PromptTokens() int64 {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// TotalTokens returns prompt plus output tokens. ThinkingTokens is already a
// detail of OutputTokens and is therefore not added separately.
func (u Usage) TotalTokens() int64 {
	return u.PromptTokens() + u.OutputTokens
}

// Report is the deduplicated usage found in one JSONL session.
type Report struct {
	Latest       *Usage `json:"latest,omitempty"`
	Total        Usage  `json:"total"`
	RequestCount int    `json:"request_count"`

	// entries is retained for Scanner's project-wide deduplication. The key is
	// message:<id>, uuid:<id>, or line:<n> for a record without either ID.
	entries map[string]Usage
}

type rawRecord struct {
	Type    string     `json:"type"`
	UUID    string     `json:"uuid"`
	Message rawMessage `json:"message"`
}

type rawMessage struct {
	ID    string    `json:"id"`
	Usage *rawUsage `json:"usage"`
}

type rawUsage struct {
	InputTokens              int64                   `json:"input_tokens"`
	OutputTokens             int64                   `json:"output_tokens"`
	CacheCreationInputTokens int64                   `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                   `json:"cache_read_input_tokens"`
	OutputTokensDetails      rawOutputTokensDetails  `json:"output_tokens_details"`
	CacheCreation            rawCacheCreationDetails `json:"cache_creation"`
}

type rawOutputTokensDetails struct {
	ThinkingTokens int64 `json:"thinking_tokens"`
}

type rawCacheCreationDetails struct {
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
}

// ParseBytes parses newline-delimited Claude records from data.
func ParseBytes(data []byte) (Report, error) {
	return ParseReader(bytes.NewReader(data))
}

// ParseReader parses newline-delimited Claude records from r.
// Malformed JSON lines and records without assistant usage are ignored. I/O
// and scanner errors are returned because they mean the input was not fully
// examined.
func ParseReader(r io.Reader) (Report, error) {
	var report Report
	values := make(map[string]Usage)
	scanner := bufio.NewScanner(r)
	// Claude messages can contain large tool inputs and transcripts. Keep a
	// generous bound while still avoiding an unbounded allocation for a corrupt
	// input stream.
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var record rawRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if record.Type != "assistant" || record.Message.Usage == nil {
			continue
		}

		key := requestKey(record, lineNumber)
		current := convertUsage(*record.Message.Usage)
		if previous, ok := values[key]; ok {
			report.Total = subtract(report.Total, previous)
		} else {
			report.RequestCount++
		}
		values[key] = current
		report.Total = add(report.Total, current)
		latest := current
		report.Latest = &latest
	}
	if err := scanner.Err(); err != nil {
		return report, fmt.Errorf("read usage JSONL: %w", err)
	}
	report.entries = values
	return report, nil
}

func requestKey(record rawRecord, lineNumber int) string {
	if record.Message.ID != "" {
		return "message:" + record.Message.ID
	}
	if record.UUID != "" {
		return "uuid:" + record.UUID
	}
	// Without either identifier there is no safe way to infer that two lines
	// belong to one request. A line-number key is deterministic for the same
	// input and deliberately keeps identical-looking requests separate.
	return fmt.Sprintf("line:%d", lineNumber)
}

func convertUsage(raw rawUsage) Usage {
	return Usage{
		InputTokens:                raw.InputTokens,
		OutputTokens:               raw.OutputTokens,
		CacheCreationInputTokens:   raw.CacheCreationInputTokens,
		CacheReadInputTokens:       raw.CacheReadInputTokens,
		ThinkingTokens:             raw.OutputTokensDetails.ThinkingTokens,
		CacheCreation5mInputTokens: raw.CacheCreation.Ephemeral5mInputTokens,
		CacheCreation1hInputTokens: raw.CacheCreation.Ephemeral1hInputTokens,
	}
}

func add(left, right Usage) Usage {
	return Usage{
		InputTokens:                left.InputTokens + right.InputTokens,
		OutputTokens:               left.OutputTokens + right.OutputTokens,
		CacheCreationInputTokens:   left.CacheCreationInputTokens + right.CacheCreationInputTokens,
		CacheReadInputTokens:       left.CacheReadInputTokens + right.CacheReadInputTokens,
		ThinkingTokens:             left.ThinkingTokens + right.ThinkingTokens,
		CacheCreation5mInputTokens: left.CacheCreation5mInputTokens + right.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: left.CacheCreation1hInputTokens + right.CacheCreation1hInputTokens,
	}
}

func subtract(left, right Usage) Usage {
	return Usage{
		InputTokens:                left.InputTokens - right.InputTokens,
		OutputTokens:               left.OutputTokens - right.OutputTokens,
		CacheCreationInputTokens:   left.CacheCreationInputTokens - right.CacheCreationInputTokens,
		CacheReadInputTokens:       left.CacheReadInputTokens - right.CacheReadInputTokens,
		ThinkingTokens:             left.ThinkingTokens - right.ThinkingTokens,
		CacheCreation5mInputTokens: left.CacheCreation5mInputTokens - right.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: left.CacheCreation1hInputTokens - right.CacheCreation1hInputTokens,
	}
}
