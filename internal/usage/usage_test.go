package usage

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestParseBytesDeduplicatesRepeatedAssistantMessage(t *testing.T) {
	data := strings.Join([]string{
		assistantLine("gen-1", "", usageJSON(2, 396, 25633, 0, 0, 25633, 0)),
		assistantLine("gen-1", "", usageJSON(2, 396, 25633, 0, 0, 25633, 0)),
	}, "\n")

	report, err := ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if report.RequestCount != 1 {
		t.Fatalf("RequestCount = %d, want 1", report.RequestCount)
	}
	if report.Total.InputTokens != 2 || report.Total.OutputTokens != 396 || report.Total.CacheCreationInputTokens != 25633 {
		t.Fatalf("Total = %+v, want one usage record", report.Total)
	}
}

func TestParseBytesLaterDuplicateReplacesPreviousUsage(t *testing.T) {
	data := strings.Join([]string{
		assistantLine("gen-1", "", usageJSON(2, 4, 10, 3, 0, 10, 0)),
		assistantLine("gen-1", "", usageJSON(8, 9, 20, 7, 2, 20, 0)),
	}, "\n")

	report, err := ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if report.RequestCount != 1 {
		t.Fatalf("RequestCount = %d, want 1", report.RequestCount)
	}
	want := Usage{
		InputTokens: 8, OutputTokens: 9, CacheCreationInputTokens: 20,
		CacheReadInputTokens: 7, ThinkingTokens: 2, CacheCreation5mInputTokens: 20,
	}
	if report.Total != want {
		t.Fatalf("Total = %+v, want %+v", report.Total, want)
	}
	if report.Latest == nil || *report.Latest != want {
		t.Fatalf("Latest = %+v, want %+v", report.Latest, want)
	}
}

func TestParseBytesAccumulatesDistinctRequestsAndCacheDetails(t *testing.T) {
	data := strings.Join([]string{
		assistantLine("gen-1", "", usageJSON(10, 5, 100, 20, 2, 60, 40)),
		assistantLine("gen-2", "", usageJSON(3, 7, 30, 4, 3, 10, 20)),
	}, "\n")

	report, err := ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	want := Usage{
		InputTokens: 13, OutputTokens: 12, CacheCreationInputTokens: 130,
		CacheReadInputTokens: 24, ThinkingTokens: 5,
		CacheCreation5mInputTokens: 70, CacheCreation1hInputTokens: 60,
	}
	if report.RequestCount != 2 || report.Total != want {
		t.Fatalf("Report = %+v, want requests=2 total=%+v", report, want)
	}
	if report.Latest == nil || report.Latest.OutputTokens != 7 {
		t.Fatalf("Latest = %+v, want second request", report.Latest)
	}
	if report.Total.TotalTokens() != 179 {
		t.Fatalf("TotalTokens = %d, want 179 (thinking must not be added twice)", report.Total.TotalTokens())
	}
}

func TestParseBytesSkipsMalformedAndIrrelevantRecords(t *testing.T) {
	data := strings.Join([]string{
		"{not-json",
		`{"type":"user","uuid":"user-1","message":{"usage":{"input_tokens":99}}}`,
		`{"type":"assistant","uuid":"assistant-no-usage","message":{"content":[]}}`,
		assistantLine("gen-1", "", usageJSON(1, 2, 3, 4, 0, 0, 0)),
	}, "\n")

	report, err := ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if report.RequestCount != 1 || report.Total.InputTokens != 1 {
		t.Fatalf("Report = %+v, want only assistant usage", report)
	}
}

func TestParseBytesSupportsLongJSONLLine(t *testing.T) {
	line := map[string]any{
		"type": "assistant",
		"uuid": "long-1",
		"message": map[string]any{
			"id":    "long-message",
			"usage": map[string]any{"input_tokens": 11, "output_tokens": 13},
		},
		"content": strings.Repeat("x", 128*1024),
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	report, err := ParseBytes(encoded)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if report.RequestCount != 1 || report.Total.InputTokens != 11 || report.Total.OutputTokens != 13 {
		t.Fatalf("Report = %+v, want long line usage", report)
	}
}

func TestParseBytesFallbackIdentifiersDoNotMergeRequests(t *testing.T) {
	withoutIDs := `{"type":"assistant","message":{"usage":{"input_tokens":2}}}`
	report, err := ParseBytes([]byte(withoutIDs + "\n" + withoutIDs))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if report.RequestCount != 2 || report.Total.InputTokens != 4 {
		t.Fatalf("Report = %+v, want two independent fallback records", report)
	}
}

func TestParseBytesUsesUUIDWhenMessageIDIsMissing(t *testing.T) {
	data := strings.Join([]string{
		assistantLine("", "uuid-1", usageJSON(2, 3, 0, 0, 0, 0, 0)),
		assistantLine("", "uuid-1", usageJSON(9, 8, 0, 0, 0, 0, 0)),
	}, "\n")
	report, err := ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if report.RequestCount != 1 || report.Total.InputTokens != 9 || report.Total.OutputTokens != 8 {
		t.Fatalf("Report = %+v, want UUID deduplication", report)
	}
}

func TestParseBytesEmptyInput(t *testing.T) {
	report, err := ParseBytes(nil)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if report.Latest != nil || report.RequestCount != 0 || report.Total != (Usage{}) {
		t.Fatalf("Report = %+v, want empty report", report)
	}
}

func TestParseReaderMatchesParseBytes(t *testing.T) {
	data := []byte(assistantLine("reader-1", "", usageJSON(4, 5, 6, 7, 0, 2, 4)))
	fromBytes, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	fromReader, err := ParseReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParseReader() error = %v", err)
	}
	if fromReader.RequestCount != fromBytes.RequestCount || fromReader.Total != fromBytes.Total {
		t.Fatalf("ParseReader() = %+v, ParseBytes() = %+v", fromReader, fromBytes)
	}
}

func assistantLine(messageID, uuid, usage string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","message":{"id":"` + messageID + `","usage":` + usage + `}}`
}

func usageJSON(input, output, cacheCreation, cacheRead, thinking, cache5m, cache1h int64) string {
	return `{"input_tokens":` + int64String(input) +
		`,"output_tokens":` + int64String(output) +
		`,"cache_creation_input_tokens":` + int64String(cacheCreation) +
		`,"cache_read_input_tokens":` + int64String(cacheRead) +
		`,"output_tokens_details":{"thinking_tokens":` + int64String(thinking) + `}` +
		`,"cache_creation":{"ephemeral_5m_input_tokens":` + int64String(cache5m) +
		`,"ephemeral_1h_input_tokens":` + int64String(cache1h) + `}}`
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}
