package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func setupLedger(t *testing.T, yamlCfg string) string {
	t.Helper()
	nowFunc = func() time.Time { return testNow }
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	cfg := "data_file: " + path + "\n" + yamlCfg
	req, errMarshal := json.Marshal(lifecycleRequest{ConfigYAML: []byte(cfg)})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	if errConfig := configurePlugin(req); errConfig != nil {
		t.Fatalf("configurePlugin: %v", errConfig)
	}
	t.Cleanup(func() {
		if errClose := closeLedger(); errClose != nil {
			t.Fatal(errClose)
		}
	})
	return path
}

func sendUsage(t *testing.T, model, apiKey, session string, total int64, at time.Time) {
	t.Helper()
	raw, errMarshal := json.Marshal(map[string]any{
		"RequestID":   "req-" + model + "-" + session,
		"Provider":    "claude",
		"Model":       model,
		"APIKey":      apiKey,
		"SessionID":   session,
		"AuthID":      "auth-1",
		"Source":      "me@example.com",
		"RequestedAt": at,
		"Detail":      map[string]any{"InputTokens": total - 10, "OutputTokens": 10, "TotalTokens": total},
	})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	if errUsage := handleUsage(raw); errUsage != nil {
		t.Fatalf("handleUsage: %v", errUsage)
	}
}

func TestSummaryRanksKeysByTokensForOneModel(t *testing.T) {
	setupLedger(t, "key_labels:\n  sk-alice-0000000001: alice\n")
	sendUsage(t, "claude-opus-4-7", "sk-alice-0000000001", "s1", 500, testNow.Add(-time.Hour))
	sendUsage(t, "claude-opus-4-7", "sk-bob-00000000002", "s2", 9000, testNow.Add(-time.Hour))
	sendUsage(t, "claude-opus-4-7", "sk-bob-00000000002", "s3", 1000, testNow.Add(-30*time.Minute))
	sendUsage(t, "gpt-5.4", "sk-alice-0000000001", "s4", 50000, testNow.Add(-time.Hour))

	got, errSummary := buildSummary(summaryQuery{Model: "claude-opus-4-7", GroupBy: "key"})
	if errSummary != nil {
		t.Fatal(errSummary)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(got.Rows))
	}
	if got.Rows[0].Label != "sk-bob…0002" || got.Rows[0].TotalTokens != 10000 || got.Rows[0].Requests != 2 {
		t.Fatalf("top row = %+v", got.Rows[0])
	}
	if got.Rows[1].Label != "alice" || got.Rows[1].TotalTokens != 500 {
		t.Fatalf("second row = %+v", got.Rows[1])
	}
	if got.TotalTokens != 10500 {
		t.Fatalf("total = %d, want 10500", got.TotalTokens)
	}
	if len(got.Models) != 2 || got.Models[0].Model != "gpt-5.4" {
		t.Fatalf("models = %+v", got.Models)
	}
}

func TestSummaryHoursWindowAndGroupBySession(t *testing.T) {
	setupLedger(t, "")
	sendUsage(t, "m", "sk-x", "old", 100, testNow.Add(-48*time.Hour))
	sendUsage(t, "m", "sk-x", "new", 200, testNow.Add(-time.Hour))

	q, errQuery := parseSummaryQuery(url.Values{"hours": {"24"}, "group_by": {"session"}})
	if errQuery != nil {
		t.Fatal(errQuery)
	}
	got, errSummary := buildSummary(q)
	if errSummary != nil {
		t.Fatal(errSummary)
	}
	if len(got.Rows) != 1 || got.Rows[0].Key != "new" || got.TotalTokens != 200 {
		t.Fatalf("summary = %+v", got)
	}
}

func TestLedgerNeverStoresRawKeyAndSurvivesReload(t *testing.T) {
	path := setupLedger(t, "")
	sendUsage(t, "m", "sk-super-secret-key-123", "s", 42, testNow)

	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	if strings.Contains(string(raw), "sk-super-secret-key-123") {
		t.Fatalf("raw key written to ledger: %s", raw)
	}

	req, _ := json.Marshal(lifecycleRequest{ConfigYAML: []byte("data_file: " + path + "\n")})
	if errConfig := configurePlugin(req); errConfig != nil {
		t.Fatal(errConfig)
	}
	got, errSummary := buildSummary(summaryQuery{GroupBy: "key"})
	if errSummary != nil {
		t.Fatal(errSummary)
	}
	if got.TotalTokens != 42 || got.Rows[0].Key != keyID("sk-super-secret-key-123") {
		t.Fatalf("after reload = %+v", got)
	}
}

func TestRetentionCompactsOldEntriesOnLoad(t *testing.T) {
	path := setupLedger(t, "retention_days: 7\n")
	sendUsage(t, "m", "sk-x", "old", 100, testNow.Add(-30*24*time.Hour))
	sendUsage(t, "m", "sk-x", "new", 200, testNow)

	req, _ := json.Marshal(lifecycleRequest{ConfigYAML: []byte("data_file: " + path + "\nretention_days: 7\n")})
	if errConfig := configurePlugin(req); errConfig != nil {
		t.Fatal(errConfig)
	}
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 1 {
		t.Fatalf("ledger has %d lines after compaction, want 1", lines)
	}
}

func TestCorruptLedgerFailsLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	if errWrite := os.WriteFile(path, []byte("{not json}\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	req, _ := json.Marshal(lifecycleRequest{ConfigYAML: []byte("data_file: " + path + "\n")})
	errConfig := configurePlugin(req)
	if errConfig == nil || !strings.Contains(errConfig.Error(), "line 1") {
		t.Fatalf("configurePlugin error = %v, want line 1 decode error", errConfig)
	}
}

func TestUnknownGroupByIsRejected(t *testing.T) {
	setupLedger(t, "")
	if _, errSummary := buildSummary(summaryQuery{GroupBy: "nope"}); errSummary == nil {
		t.Fatal("expected error for unknown group_by")
	}
}
