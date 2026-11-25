# CLIProxyAPI Usage Ledger Plugin

<img src="assets/logo.png" width="96" align="right" alt="Usage Ledger logo">

**Usage Ledger** is an ABI v1 plugin for CLIProxyAPI that answers "who spent all the tokens on this model?". It records every model request with its client API key, session, upstream account and token counts to a persistent JSONL ledger, and adds a **Usage Ledger** page to the Management UI that ranks spenders per model over any time window.

CLIProxyAPI's built-in usage queue keeps records for 60 seconds and hands each one to a single reader. Usage Ledger receives its own copy of every record through the `usage_plugin` capability, so it keeps the full history and runs alongside the Management Center's usage page without taking records from it.

> **Companion Plugin:** Made to work alongside **[Safeguard Killer (`safeguard-killer`)](https://github.com/esauvisky/CLIProxyAPI-Safeguard-Killer-Plugin)**. Smart Group requests show up under the model that actually served them.
>
> **Note:** The ledger only covers requests made while the plugin is enabled. It cannot recover usage from before it was installed.

## What it records

One JSON line per model execution:

| Field | Meaning |
|---|---|
| `ts` | When the request was received |
| `model`, `alias`, `provider` | Upstream model, the alias the client asked for, and the provider |
| `key_id`, `key_label` | Short SHA-256 of the client API key, and its label from `key_labels` (or a masked form like `sk-abc…wxyz`) |
| `session_id` | Client session, when the client sends one (Claude Code, Codex) |
| `auth_id`, `account` | The upstream credential that served the request |
| `input_tokens`, `output_tokens`, `reasoning_tokens`, `cached_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `total_tokens` | Token counts reported by the upstream |
| `failed` | Whether the request failed |

Raw client API keys are never written to disk.

## Management UI

Open **Usage Ledger** in the CPA Management Center. Pick a model and a time window (last hour, 24 hours, 7 days, 30 days, or everything recorded), and group by client key, session, upstream account, provider, or model. Each row shows requests, failures, input, output, cached and total tokens, its share of the total, its top models, and when it was last seen. The page uses the management key you are logged in with.

## Management API

```
GET /v0/management/plugins/usage-ledger/summary?model=claude-opus-4-7&hours=24&group_by=key
```

| Query | Default | Values |
|---|---|---|
| `model` | all models | Matches the model or its alias |
| `hours` | everything recorded | Any positive number |
| `group_by` | `key` | `key`, `session`, `account`, `provider`, `model` |

```bash
curl -s -H "Authorization: Bearer $MANAGEMENT_KEY" \
  'http://127.0.0.1:8317/v0/management/plugins/usage-ledger/summary?model=claude-opus-4-7&hours=24' \
  | jq '.rows[] | {label, requests, total_tokens}'
```

For ad-hoc analysis, query the ledger file directly:

```bash
jq -s 'map(select(.model=="claude-opus-4-7")) | group_by(.key_label)
  | map({who: .[0].key_label, tokens: (map(.total_tokens)|add)}) | sort_by(-.tokens)' usage-ledger.jsonl
```

## Build

```bash
make test
```

Produces `build/usage-ledger.so`.

## Install from the management interface

Add this registry URL to `plugins.store-sources`:

```yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/esauvisky/CLIProxyAPI-Usage-Ledger-Plugin/main/registry.json"
```

## Example `config.yaml`

```yaml
plugins:
  enabled: true
  dir: "/CLIProxyAPI/plugins"
  configs:
    usage-ledger:
      enabled: true
      priority: 100
      # Keep this on a persistent volume. In Docker, ./plugins is already mounted.
      data_file: "/CLIProxyAPI/plugins/usage-ledger.jsonl"
      # Entries older than this are compacted out on load. 0 keeps everything.
      retention_days: 90
      # Name your client keys so the UI shows people instead of masked keys.
      key_labels:
        "sk-team-alice-...": "alice"
        "sk-ci-runner-...": "ci"
```

If the ledger file contains a line that is not valid JSON, the plugin refuses to load and reports the line number instead of silently skipping data.
