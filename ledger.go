package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginMetadata           `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type pluginMetadata struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository"`
	Logo             string        `json:"Logo,omitempty"`
	ConfigFields     []configField `json:"ConfigFields"`
}

type configField struct {
	Name        string `json:"Name"`
	Type        string `json:"Type"`
	Default     any    `json:"Default,omitempty"`
	Description string `json:"Description"`
}

type registrationCapabilities struct {
	UsagePlugin   bool `json:"usage_plugin"`
	ManagementAPI bool `json:"management_api"`
}

type pluginConfig struct {
	DataFile      string            `yaml:"data_file"`
	RetentionDays int               `yaml:"retention_days"`
	KeyLabels     map[string]string `yaml:"key_labels"`
}

// usageRecord mirrors the JSON encoding of pluginapi.UsageRecord, which has no json tags.
type usageRecord struct {
	RequestID   string
	Provider    string
	Model       string
	Alias       string
	APIKey      string
	SessionID   string
	AuthID      string
	Source      string
	RequestedAt time.Time
	Failed      bool
	Detail      struct {
		InputTokens         int64
		OutputTokens        int64
		ReasoningTokens     int64
		CachedTokens        int64
		CacheReadTokens     int64
		CacheCreationTokens int64
		TotalTokens         int64
	}
}

type ledgerEntry struct {
	Time                time.Time `json:"ts"`
	RequestID           string    `json:"request_id"`
	Provider            string    `json:"provider"`
	Model               string    `json:"model"`
	Alias               string    `json:"alias,omitempty"`
	KeyID               string    `json:"key_id"`
	KeyLabel            string    `json:"key_label"`
	SessionID           string    `json:"session_id,omitempty"`
	AuthID              string    `json:"auth_id,omitempty"`
	Account             string    `json:"account,omitempty"`
	Failed              bool      `json:"failed,omitempty"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	ReasoningTokens     int64     `json:"reasoning_tokens"`
	CachedTokens        int64     `json:"cached_tokens"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	CacheCreationTokens int64     `json:"cache_creation_tokens"`
	TotalTokens         int64     `json:"total_tokens"`
}

var (
	ledgerMu  sync.Mutex
	ledgerCfg pluginConfig
	entries   []ledgerEntry
	appendLog *os.File
	nowFunc   = time.Now
)

func defaultPluginConfig() pluginConfig {
	return pluginConfig{
		DataFile:      "usage-ledger.jsonl",
		RetentionDays: 90,
		KeyLabels:     map[string]string{},
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: schemaVersion,
		Metadata: pluginMetadata{
			Name:             pluginName,
			Version:          pluginVersion,
			Author:           pluginAuthor,
			GitHubRepository: pluginRepo,
			Logo:             pluginLogo,
			ConfigFields: []configField{
				{Name: "data_file", Type: "string", Default: "usage-ledger.jsonl", Description: "JSONL file the ledger appends every request to. Relative paths resolve against the CLIProxyAPI working directory; point it at a persistent volume in Docker (e.g. /CLIProxyAPI/plugins/usage-ledger.jsonl)."},
				{Name: "retention_days", Type: "integer", Default: 90, Description: "Entries older than this are dropped from memory and compacted out of data_file on load. 0 keeps everything."},
				{Name: "key_labels", Type: "object", Description: "Map of client API key to a human name (e.g. sk-abc123: alice). Unlabeled keys are shown masked. Raw keys are never written to data_file."},
			},
		},
		Capabilities: registrationCapabilities{
			UsagePlugin:   true,
			ManagementAPI: true,
		},
	}
}

func configurePlugin(raw []byte) error {
	cfg := defaultPluginConfig()
	if len(raw) > 0 {
		var req lifecycleRequest
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return fmt.Errorf("decode lifecycle request: %w", errUnmarshal)
		}
		if errYAML := yaml.Unmarshal(req.ConfigYAML, &cfg); errYAML != nil {
			return fmt.Errorf("decode plugin config: %w", errYAML)
		}
	}
	cfg.DataFile = strings.TrimSpace(cfg.DataFile)
	if cfg.DataFile == "" {
		return fmt.Errorf("data_file must not be empty")
	}
	if cfg.RetentionDays < 0 {
		return fmt.Errorf("retention_days must be >= 0, got %d", cfg.RetentionDays)
	}
	return openLedger(cfg)
}

func openLedger(cfg pluginConfig) error {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if appendLog != nil {
		if errClose := appendLog.Close(); errClose != nil {
			return fmt.Errorf("close previous ledger: %w", errClose)
		}
		appendLog = nil
	}
	if errDir := os.MkdirAll(filepath.Dir(cfg.DataFile), 0o755); errDir != nil {
		return fmt.Errorf("create ledger directory: %w", errDir)
	}
	loaded, dropped, errLoad := loadEntries(cfg)
	if errLoad != nil {
		return errLoad
	}
	if dropped > 0 {
		if errCompact := rewriteEntries(cfg.DataFile, loaded); errCompact != nil {
			return errCompact
		}
	}
	f, errOpen := os.OpenFile(cfg.DataFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if errOpen != nil {
		return fmt.Errorf("open ledger %s: %w", cfg.DataFile, errOpen)
	}
	ledgerCfg = cfg
	entries = loaded
	appendLog = f
	return nil
}

func loadEntries(cfg pluginConfig) ([]ledgerEntry, int, error) {
	raw, errRead := os.ReadFile(cfg.DataFile)
	if os.IsNotExist(errRead) {
		return nil, 0, nil
	}
	if errRead != nil {
		return nil, 0, fmt.Errorf("read ledger %s: %w", cfg.DataFile, errRead)
	}
	cutoff := retentionCutoff(cfg)
	var out []ledgerEntry
	dropped := 0
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var e ledgerEntry
		if errLine := json.Unmarshal(scanner.Bytes(), &e); errLine != nil {
			return nil, 0, fmt.Errorf("ledger %s line %d: %w", cfg.DataFile, line, errLine)
		}
		if e.Time.Before(cutoff) {
			dropped++
			continue
		}
		out = append(out, e)
	}
	if errScan := scanner.Err(); errScan != nil {
		return nil, 0, fmt.Errorf("scan ledger %s: %w", cfg.DataFile, errScan)
	}
	return out, dropped, nil
}

func rewriteEntries(path string, kept []ledgerEntry) error {
	var buf bytes.Buffer
	for _, e := range kept {
		line, errMarshal := json.Marshal(e)
		if errMarshal != nil {
			return errMarshal
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if errWrite := os.WriteFile(tmp, buf.Bytes(), 0o600); errWrite != nil {
		return fmt.Errorf("write compacted ledger: %w", errWrite)
	}
	if errRename := os.Rename(tmp, path); errRename != nil {
		return fmt.Errorf("replace ledger with compacted copy: %w", errRename)
	}
	return nil
}

func retentionCutoff(cfg pluginConfig) time.Time {
	if cfg.RetentionDays == 0 {
		return time.Time{}
	}
	return nowFunc().Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
}

func closeLedger() error {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if appendLog == nil {
		return nil
	}
	errClose := appendLog.Close()
	appendLog = nil
	return errClose
}

func handleUsage(raw []byte) error {
	var rec usageRecord
	if errUnmarshal := json.Unmarshal(raw, &rec); errUnmarshal != nil {
		return fmt.Errorf("decode usage record: %w", errUnmarshal)
	}

	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if appendLog == nil {
		return fmt.Errorf("usage record received before plugin.register opened the ledger")
	}
	e := ledgerEntry{
		Time:                rec.RequestedAt,
		RequestID:           rec.RequestID,
		Provider:            rec.Provider,
		Model:               rec.Model,
		Alias:               rec.Alias,
		KeyID:               keyID(rec.APIKey),
		KeyLabel:            keyLabel(ledgerCfg, rec.APIKey),
		SessionID:           rec.SessionID,
		AuthID:              rec.AuthID,
		Account:             rec.Source,
		Failed:              rec.Failed,
		InputTokens:         rec.Detail.InputTokens,
		OutputTokens:        rec.Detail.OutputTokens,
		ReasoningTokens:     rec.Detail.ReasoningTokens,
		CachedTokens:        rec.Detail.CachedTokens,
		CacheReadTokens:     rec.Detail.CacheReadTokens,
		CacheCreationTokens: rec.Detail.CacheCreationTokens,
		TotalTokens:         rec.Detail.TotalTokens,
	}
	line, errMarshal := json.Marshal(e)
	if errMarshal != nil {
		return errMarshal
	}
	if _, errWrite := appendLog.Write(append(line, '\n')); errWrite != nil {
		return fmt.Errorf("append to ledger: %w", errWrite)
	}
	entries = append(entries, e)
	return nil
}

func keyID(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:6])
}

func keyLabel(cfg pluginConfig, apiKey string) string {
	if apiKey == "" {
		return "(no client key)"
	}
	if label, ok := cfg.KeyLabels[apiKey]; ok {
		return label
	}
	if len(apiKey) <= 10 {
		return "key " + keyID(apiKey)
	}
	return apiKey[:6] + "…" + apiKey[len(apiKey)-4:]
}

type summaryQuery struct {
	Model   string
	Since   time.Time
	GroupBy string
}

type summaryRow struct {
	Key           string    `json:"key"`
	Label         string    `json:"label"`
	Requests      int64     `json:"requests"`
	Failed        int64     `json:"failed"`
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
	CachedTokens  int64     `json:"cached_tokens"`
	TotalTokens   int64     `json:"total_tokens"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	TopModels     []string  `json:"top_models"`
	modelTotalsBy map[string]int64
}

type modelTotal struct {
	Model       string `json:"model"`
	Requests    int64  `json:"requests"`
	TotalTokens int64  `json:"total_tokens"`
}

type summaryResponse struct {
	DataFile    string       `json:"data_file"`
	Entries     int          `json:"entries"`
	OldestEntry *time.Time   `json:"oldest_entry"`
	Models      []modelTotal `json:"models"`
	Rows        []summaryRow `json:"rows"`
	TotalTokens int64        `json:"total_tokens"`
	Requests    int64        `json:"requests"`
}

var groupKeyFuncs = map[string]func(ledgerEntry) (string, string){
	"key":      func(e ledgerEntry) (string, string) { return e.KeyID, e.KeyLabel },
	"session":  func(e ledgerEntry) (string, string) { return e.SessionID, e.SessionID },
	"account":  func(e ledgerEntry) (string, string) { return e.AuthID, e.Account },
	"provider": func(e ledgerEntry) (string, string) { return e.Provider, e.Provider },
	"model":    func(e ledgerEntry) (string, string) { return e.Model, e.Model },
}

func matchesModel(e ledgerEntry, model string) bool {
	return model == "" || e.Model == model || e.Alias == model
}

func buildSummary(q summaryQuery) (summaryResponse, error) {
	groupKey, ok := groupKeyFuncs[q.GroupBy]
	if !ok {
		return summaryResponse{}, fmt.Errorf("unknown group_by %q", q.GroupBy)
	}

	ledgerMu.Lock()
	defer ledgerMu.Unlock()

	resp := summaryResponse{DataFile: ledgerCfg.DataFile, Entries: len(entries)}
	if len(entries) > 0 {
		oldest := entries[0].Time
		resp.OldestEntry = &oldest
	}
	models := map[string]*modelTotal{}
	rows := map[string]*summaryRow{}
	for _, e := range entries {
		if e.Time.Before(q.Since) {
			continue
		}
		m := models[e.Model]
		if m == nil {
			m = &modelTotal{Model: e.Model}
			models[e.Model] = m
		}
		m.Requests++
		m.TotalTokens += e.TotalTokens

		if !matchesModel(e, q.Model) {
			continue
		}
		key, label := groupKey(e)
		row := rows[key]
		if row == nil {
			row = &summaryRow{Key: key, Label: label, FirstSeen: e.Time, modelTotalsBy: map[string]int64{}}
			rows[key] = row
		}
		row.Label = label
		row.Requests++
		if e.Failed {
			row.Failed++
		}
		row.InputTokens += e.InputTokens
		row.OutputTokens += e.OutputTokens
		row.CachedTokens += e.CachedTokens
		row.TotalTokens += e.TotalTokens
		row.LastSeen = e.Time
		row.modelTotalsBy[e.Model] += e.TotalTokens
		resp.Requests++
		resp.TotalTokens += e.TotalTokens
	}

	for _, m := range models {
		resp.Models = append(resp.Models, *m)
	}
	sort.Slice(resp.Models, func(i, j int) bool { return resp.Models[i].TotalTokens > resp.Models[j].TotalTokens })
	for _, row := range rows {
		row.TopModels = topModels(row.modelTotalsBy, 3)
		resp.Rows = append(resp.Rows, *row)
	}
	sort.Slice(resp.Rows, func(i, j int) bool { return resp.Rows[i].TotalTokens > resp.Rows[j].TotalTokens })
	return resp, nil
}

func topModels(totals map[string]int64, n int) []string {
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return totals[names[i]] > totals[names[j]] })
	if len(names) > n {
		names = names[:n]
	}
	return names
}
