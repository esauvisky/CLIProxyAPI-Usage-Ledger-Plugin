package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	uiResourcePath       = "/ui"
	mgmtSummaryRoutePath = "/plugins/usage-ledger/summary"
)

type managementRegistration struct {
	Routes    []managementRoute    `json:"routes,omitempty"`
	Resources []managementResource `json:"resources,omitempty"`
}

type managementRoute struct {
	Method      string `json:"Method"`
	Path        string `json:"Path"`
	Description string `json:"Description,omitempty"`
}

type managementResource struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu"`
	Description string `json:"Description"`
}

type managementRequest struct {
	Method  string      `json:"Method"`
	Path    string      `json:"Path"`
	Headers http.Header `json:"Headers"`
	Query   url.Values  `json:"Query"`
	Body    []byte      `json:"Body"`
}

type managementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers"`
	Body       []byte      `json:"Body"`
}

func managementRegistrationPayload() managementRegistration {
	return managementRegistration{
		Routes: []managementRoute{
			{
				Method:      http.MethodGet,
				Path:        mgmtSummaryRoutePath,
				Description: "Token usage totals per client key, session, account, provider or model. Query: model, hours, group_by.",
			},
		},
		Resources: []managementResource{
			{
				Path:        uiResourcePath,
				Menu:        "Usage Ledger",
				Description: "See which client keys, sessions and accounts spent tokens on each model.",
			},
		},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, fmt.Errorf("decode management request: %w", errUnmarshal)
	}

	if !strings.HasSuffix(strings.TrimRight(req.Path, "/"), mgmtSummaryRoutePath) {
		return okEnvelope(managementResponse{
			StatusCode: http.StatusOK,
			Headers: http.Header{
				"Content-Type":  []string{"text/html; charset=utf-8"},
				"Cache-Control": []string{"no-store"},
			},
			Body: []byte(ledgerHTML),
		})
	}

	q, errQuery := parseSummaryQuery(req.Query)
	if errQuery != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": errQuery.Error()})
	}
	summary, errSummary := buildSummary(q)
	if errSummary != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": errSummary.Error()})
	}
	return jsonResponse(http.StatusOK, summary)
}

func parseSummaryQuery(v url.Values) (summaryQuery, error) {
	q := summaryQuery{Model: strings.TrimSpace(v.Get("model")), GroupBy: "key"}
	if g := strings.TrimSpace(v.Get("group_by")); g != "" {
		q.GroupBy = g
	}
	if h := strings.TrimSpace(v.Get("hours")); h != "" {
		hours, errHours := strconv.ParseFloat(h, 64)
		if errHours != nil || hours <= 0 {
			return summaryQuery{}, fmt.Errorf("hours must be a positive number, got %q", h)
		}
		q.Since = nowFunc().Add(-time.Duration(hours * float64(time.Hour)))
	}
	return q, nil
}

func jsonResponse(status int, payload any) ([]byte, error) {
	body, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return okEnvelope(managementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type":  []string{"application/json; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
		},
		Body: body,
	})
}

const ledgerHTML = `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Usage Ledger</title>
<style>
  /* CPA Dark Theme (Default) */
  :root, [data-theme='dark'] {
    --bg-secondary: #151412;
    --bg-primary: #1d1b18;
    --bg-tertiary: #262320;
    --bg-hover: #2e2a26;
    --text-primary: #f6f4f1;
    --text-secondary: #c9c3bb;
    --text-tertiary: #9c958d;
    --border-color: #3a3530;
    --border-hover: #5a544d;
    --primary-color: #8b8680;
    --primary-hover: #9a948e;
    --primary-contrast: #ffffff;
    --success-color: #10b981;
    --bar-color: #c98a3c;
    --failure-badge-bg: rgba(198, 87, 70, 0.24);
    --failure-badge-text: #f1b0a6;
    --failure-badge-border: rgba(198, 87, 70, 0.5);
    --radius-md: 8px;
    --radius-full: 9999px;
  }
  /* CPA Warm-Stone Light Theme */
  [data-theme='light'] {
    --bg-secondary: #faf9f5;
    --bg-primary: #f0eee8;
    --bg-tertiary: #e9e6df;
    --bg-hover: #e9e6df;
    --text-primary: #2d2a26;
    --text-secondary: #6d6760;
    --text-tertiary: #a29c95;
    --border-color: #e3e1db;
    --border-hover: #cecac4;
    --primary-color: #8b8680;
    --primary-hover: #7f7a74;
    --bar-color: #b8742a;
    --failure-badge-bg: rgba(198, 87, 70, 0.14);
    --failure-badge-text: #8a3a30;
    --failure-badge-border: rgba(198, 87, 70, 0.35);
  }
  /* CPA Pure White Theme */
  [data-theme='white'] {
    --bg-secondary: #ffffff;
    --bg-primary: #ffffff;
    --bg-tertiary: #f6f6f6;
    --bg-hover: #f6f6f6;
    --text-primary: #2d2a26;
    --text-secondary: #6d6760;
    --text-tertiary: #a29c95;
    --border-color: #e5e5e5;
    --border-hover: #cccccc;
    --primary-color: #8b8680;
    --primary-hover: #7f7a74;
    --bar-color: #b8742a;
    --failure-badge-bg: rgba(198, 87, 70, 0.14);
    --failure-badge-text: #8a3a30;
    --failure-badge-border: rgba(198, 87, 70, 0.35);
  }
  * { box-sizing: border-box; }
  html, body {
    margin: 0;
    padding: 0;
    background-color: var(--bg-secondary);
    color: var(--text-primary);
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Roboto', 'Oxygen', 'Ubuntu', 'Cantarell', sans-serif;
    font-size: 14px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
  }
  .page {
    padding: 70px clamp(20px, 3vw, 48px) 40px;
    display: flex;
    flex-direction: column;
    gap: 20px;
    width: 100%;
  }
  .page-header { display: flex; flex-direction: column; gap: 8px; padding-right: 180px; }
  .page-title { margin: 0; font-size: 28px; font-weight: 700; line-height: 1.2; }
  .page-desc { margin: 0; color: var(--text-secondary); }
  .status-bar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px;
    border-radius: 10px;
    border: 1px solid color-mix(in srgb, var(--border-color) 60%, transparent);
    background: color-mix(in srgb, var(--bg-secondary) 60%, transparent);
    flex-wrap: wrap;
  }
  .status-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 12px;
    border-radius: var(--radius-full);
    border: 1px solid color-mix(in srgb, var(--border-color) 50%, transparent);
    background: color-mix(in srgb, var(--bg-primary) 56%, transparent);
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
  }
  .status-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--success-color); box-shadow: 0 0 6px rgba(16, 185, 129, 0.5); }
  .status-label { color: var(--text-secondary); }
  .status-value { font-weight: 700; font-variant-numeric: tabular-nums; }
  .controls { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .input, select {
    border: 1px solid var(--border-color);
    border-radius: var(--radius-md);
    padding: 7px 11px;
    font-size: 13px;
    background-color: var(--bg-secondary);
    color: var(--text-primary);
  }
  .input:focus, select:focus { outline: none; border-color: var(--primary-color); box-shadow: 0 0 0 3px rgba(139, 134, 128, 0.18); }
  .btn {
    border: 1px solid var(--primary-color);
    border-radius: var(--radius-md);
    padding: 7px 12px;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    background-color: var(--primary-color);
    color: var(--primary-contrast);
  }
  .btn:hover { background-color: var(--primary-hover); }
  .notice {
    padding: 10px 14px;
    border-radius: var(--radius-md);
    font-size: 13px;
    display: none;
    background: var(--failure-badge-bg);
    color: var(--failure-badge-text);
    border: 1px solid var(--failure-badge-border);
  }
  .notice.show { display: block; }
  .panel {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: clamp(16px, 2vw, 20px);
    border-radius: 16px;
    border: 1px solid var(--border-color);
    background: color-mix(in srgb, var(--bg-primary) 82%, transparent);
    overflow-x: auto;
  }
  .panel-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding-bottom: 10px;
    border-bottom: 1px solid color-mix(in srgb, var(--border-color) 65%, transparent);
    flex-wrap: wrap;
  }
  .panel-title { margin: 0; font-size: 15px; font-weight: 650; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td {
    text-align: left;
    padding: 7px 8px;
    border-bottom: 1px solid color-mix(in srgb, var(--border-color) 65%, transparent);
    white-space: nowrap;
  }
  th { color: var(--text-tertiary); font-weight: 700; font-size: 11px; letter-spacing: 0.06em; text-transform: uppercase; }
  td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
  tbody tr:hover { background-color: var(--bg-hover); }
  .share { display: flex; align-items: center; gap: 8px; min-width: 160px; }
  .share-track { flex: 1; height: 6px; border-radius: var(--radius-full); background: var(--bg-tertiary); overflow: hidden; }
  .share-fill { height: 100%; background: var(--bar-color); border-radius: var(--radius-full); }
  .share-pct { width: 44px; text-align: right; color: var(--text-secondary); font-variant-numeric: tabular-nums; font-size: 12px; }
  .mono { font-family: ui-monospace, 'SF Mono', 'Cascadia Mono', 'JetBrains Mono', Menlo, Consolas, monospace; font-size: 12px; }
  .subtle { color: var(--text-tertiary); }
  .empty { color: var(--text-tertiary); padding: 18px 8px; }
</style>
</head>
<body>
<main class="page">
  <header class="page-header">
    <h1 class="page-title">Usage Ledger</h1>
    <p class="page-desc">Every model request is recorded with its client key, session and upstream account, so you can see who spent the tokens on a model. Only requests made while this plugin was enabled are counted.</p>
  </header>
  <div class="status-bar">
    <div class="status-pill"><span class="status-dot"></span><span class="status-label">Recorded since</span><span class="status-value" id="since">-</span></div>
    <div class="status-pill"><span class="status-label">Requests</span><span class="status-value" id="requests">0</span></div>
    <div class="status-pill"><span class="status-label">Tokens</span><span class="status-value" id="tokens">0</span></div>
    <div class="status-pill"><span class="status-label">Ledger file</span><span class="status-value mono" id="dataFile">-</span></div>
  </div>
  <div id="notice" class="notice"></div>
  <section class="panel">
    <div class="panel-head">
      <h2 class="panel-title" id="tableTitle">Spend by client key</h2>
      <div class="controls">
        <select id="model" onchange="load()"><option value="">All models</option></select>
        <select id="hours" onchange="load()">
          <option value="1">Last hour</option>
          <option value="24" selected>Last 24 hours</option>
          <option value="168">Last 7 days</option>
          <option value="720">Last 30 days</option>
          <option value="">Everything recorded</option>
        </select>
        <select id="groupBy" onchange="load()">
          <option value="key">By client key</option>
          <option value="session">By session</option>
          <option value="account">By upstream account</option>
          <option value="provider">By provider</option>
          <option value="model">By model</option>
        </select>
        <button class="btn" onclick="load()">Refresh</button>
      </div>
    </div>
    <table>
      <thead>
        <tr>
          <th id="groupHead">Client key</th>
          <th class="num">Requests</th>
          <th class="num">Failed</th>
          <th class="num">Input</th>
          <th class="num">Output</th>
          <th class="num">Cached</th>
          <th class="num">Total</th>
          <th>Share</th>
          <th>Top models</th>
          <th>Last seen</th>
        </tr>
      </thead>
      <tbody id="rows"></tbody>
    </table>
  </section>
</main>
<script>
const SUMMARY_URL = '/v0/management/plugins/usage-ledger/summary';
const GROUP_NAMES = { key: 'Client key', session: 'Session', account: 'Upstream account', provider: 'Provider', model: 'Model' };
const fmt = new Intl.NumberFormat();

function syncThemeWithCPA() {
  const apply = (mode) => document.documentElement.setAttribute('data-theme', mode === 'white' || mode === 'light' ? mode : 'dark');
  if (window.parent === window) {
    apply(window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
    return;
  }
  const parentDoc = window.parent.document.documentElement;
  const sync = () => {
    const attr = parentDoc.getAttribute('data-theme');
    apply(attr === 'dark' || attr === 'white' ? attr : 'light');
  };
  sync();
  new MutationObserver(sync).observe(parentDoc, { attributes: true, attributeFilter: ['data-theme'] });
}

// CPA Management Center persists its auth store as 'enc::v2::' + base64(JSON XOR
// 'cli-proxy-api-webui::secure-storage|' + host). See its src/utils/encryption.ts.
function managementKey() {
  const raw = localStorage.getItem('cli-proxy-auth');
  if (raw === null) throw new Error('Log in to the CPA Management Center first; no stored management key was found.');
  const prefix = 'enc::v2::';
  if (!raw.startsWith(prefix)) throw new Error('cli-proxy-auth is not in the enc::v2:: format this page understands');
  const bin = atob(raw.slice(prefix.length));
  const keyBytes = new TextEncoder().encode('cli-proxy-api-webui::secure-storage|' + window.location.host);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i) ^ keyBytes[i % keyBytes.length];
  return JSON.parse(new TextDecoder().decode(out)).state.managementKey;
}

function esc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function when(ts) {
  return new Date(ts).toLocaleString();
}

function showError(msg) {
  const n = document.getElementById('notice');
  n.textContent = msg;
  n.className = msg ? 'notice show' : 'notice';
}

function fillModels(models) {
  const sel = document.getElementById('model');
  const current = sel.value;
  sel.innerHTML = '<option value="">All models</option>' + models
    .map(m => '<option value="' + esc(m.model) + '">' + esc(m.model) + ' (' + fmt.format(m.total_tokens) + ')</option>')
    .join('');
  if (current && !models.some(m => m.model === current)) {
    sel.insertAdjacentHTML('beforeend', '<option value="' + esc(current) + '">' + esc(current) + ' (0)</option>');
  }
  sel.value = current;
}

function render(data) {
  document.getElementById('since').textContent = data.oldest_entry ? when(data.oldest_entry) : 'nothing yet';
  document.getElementById('requests').textContent = fmt.format(data.requests);
  document.getElementById('tokens').textContent = fmt.format(data.total_tokens);
  document.getElementById('dataFile').textContent = data.data_file;
  fillModels(data.models || []);

  const group = document.getElementById('groupBy').value;
  const model = document.getElementById('model').value;
  document.getElementById('groupHead').textContent = GROUP_NAMES[group];
  document.getElementById('tableTitle').textContent = 'Spend by ' + GROUP_NAMES[group].toLowerCase() + (model ? ' on ' + model : '');

  const rows = data.rows || [];
  const body = document.getElementById('rows');
  if (rows.length === 0) {
    body.innerHTML = '<tr><td class="empty" colspan="10">No requests recorded for this selection.</td></tr>';
    return;
  }
  body.innerHTML = rows.map(r => {
    const pct = data.total_tokens ? (100 * r.total_tokens / data.total_tokens) : 0;
    const label = r.label || '(none)';
    const keyHint = group === 'key' && r.key ? ' <span class="subtle mono">' + esc(r.key) + '</span>' : '';
    return '<tr>' +
      '<td class="mono">' + esc(label) + keyHint + '</td>' +
      '<td class="num">' + fmt.format(r.requests) + '</td>' +
      '<td class="num">' + fmt.format(r.failed) + '</td>' +
      '<td class="num">' + fmt.format(r.input_tokens) + '</td>' +
      '<td class="num">' + fmt.format(r.output_tokens) + '</td>' +
      '<td class="num">' + fmt.format(r.cached_tokens) + '</td>' +
      '<td class="num"><strong>' + fmt.format(r.total_tokens) + '</strong></td>' +
      '<td><div class="share"><div class="share-track"><div class="share-fill" style="width:' + pct.toFixed(1) + '%"></div></div><span class="share-pct">' + pct.toFixed(1) + '%</span></div></td>' +
      '<td class="mono subtle">' + esc((r.top_models || []).join(', ')) + '</td>' +
      '<td class="subtle">' + esc(when(r.last_seen)) + '</td>' +
      '</tr>';
  }).join('');
}

async function load() {
  showError('');
  const params = new URLSearchParams({ group_by: document.getElementById('groupBy').value });
  const model = document.getElementById('model').value;
  const hours = document.getElementById('hours').value;
  if (model) params.set('model', model);
  if (hours) params.set('hours', hours);
  try {
    const resp = await fetch(SUMMARY_URL + '?' + params, {
      headers: { 'Authorization': 'Bearer ' + managementKey() },
      cache: 'no-store'
    });
    const data = await resp.json();
    if (!resp.ok) throw new Error((data && data.error) || ('HTTP ' + resp.status));
    render(data);
  } catch (err) {
    showError('Failed to load usage: ' + err.message);
  }
}

syncThemeWithCPA();
load();
</script>
</body>
</html>
`
