# CPA Policy

Source and release assets are hosted in [CLIProxyAPI/cpa-policy](https://github.com/CLIProxyAPI/cpa-policy); the separate store registry is in [CLIProxyAPI/Plugins-Store](https://github.com/CLIProxyAPI/Plugins-Store).

A customized fork of `haowang02/cpa-plugin-key-billing` 1.4.2, version **1.4.5**. The independent plugin ID is now **`cpa-codex-controls`**; the management menu remains **Account Controls**.

Removed: downstream API Key management/self-service, analytics, request events, subscription plans, routing rules, model pricing, and their backend/frontend/storage implementations. Plugin billing, subscription enforcement, price downloads, and scheduler hooks are gone. CPA still owns its access authentication and upstream credentials.

Retained:

- Auth Files and AI Providers show all supported CPA credential types, with search, enabled-only filtering, native request health, quota/balance queries, and per-row settings. Provider enable/disable controls preserve existing configuration; OpenAI-compatible providers toggle as a group.
- Per-account concurrency limits default to 0, bypassing limits and queuing. Positive limits use the global per-account queue capacity and timeout. Queue capacity 0 rejects excess requests immediately. Streaming slots last until completion; upstream provider keys have independent limits.
- Opt-in location/timezone overrides. Individual account settings take precedence over the global default; explicit account-off preserves the original request. Detection fills country, region, city, and timezone through the account's configured proxy.
- Content Audit defaults to off and supports keywords/regular expressions, a Moderations-compatible API, model filters, category thresholds, observe/block modes, account overrides, draft tests, and redacted records. See [CONTENT-AUDIT.md](CONTENT-AUDIT.md).
- Upstream error events and plugin logs in an independent SQLite store.
- Management-key login, English/Chinese, themes, privacy masks, and responsive layouts.

## Installation and configuration

Add this source in the CPA plugin store:

```text
https://raw.githubusercontent.com/CLIProxyAPI/Plugins-Store/main/registry.json
```

See [INSTALL.md](INSTALL.md) for installation and migration, and [TIMEZONE.md](TIMEZONE.md) for configuration, behavior, and limitations. Content auditing covers user text. Request-body privacy replacement, automatic bans, and notifications are not included.

Requires a plugin-enabled CPA supporting schema 6; local validation uses CPA 7.3.7. Build with Go 1.24+ and a C compiler:

```sh
CGO_ENABLED=1 go build -buildvcs=false -trimpath -ldflags='-s -w -X cpa-codex-controls/internal/plugin.GitHubRepository=https://github.com/CLIProxyAPI/cpa-policy' \
  -tags cshared -buildmode=c-shared \
  -o dist/cpa-codex-controls.so ./cmd/cpa-codex-controls
```

Use `.dylib` on macOS. Stop CPA, put the library in `plugins.dir`, enable `plugins.configs.cpa-codex-controls.enabled`, and restart. Keep one loadable library per ID. See [INSTALL.md](INSTALL.md) for custom stores and migration from the old custom build.

Use `CPA_PLUGIN_REPOSITORY=CLIProxyAPI/cpa-policy sh install.sh` to download ZIP assets and checksums from this repository. Release binaries include this repository in their metadata; the store uses the download URLs in the registry. Upstream links remain attribution only.

## Diagnostic history

`state_file` is now only a read-only source for importing old error events and plugin logs. The source defaults to empty, so independent installs do not read another plugin's data. Explicitly set it to the original billing database to import history. The separate destination `diagnostics_file` defaults to `plugins/cpa-codex-controls-diagnostics.db`.

Import is transactional and recorded once per source path, so restarts and clearing logs do not duplicate or resurrect records. The old database is unchanged. Keys, plans, pricing, routing, and successful request events are not copied. Incompatible diagnostic schemas fail with rollback. The new store retains diagnostics for 30 days; the original archive remains intact. Source and destination must be different databases.

Known retired YAML settings are tolerated but ignored. Copy existing `custom_location`, `concurrency`, and `credential_policies` into the new ID's configuration to retain settings. The old ID's configuration is not loaded automatically. For timezone.13 diagnostic continuity, explicitly reuse its `diagnostics_file` after disabling the old custom plugin; do not use its diagnostics database as a `state_file` import source. Timezone rewriting covers structured Codex/Responses contexts and search-tool locations; it does not change host/OS or other plugins' clocks, nor guarantee final precedence over later interceptors.

## Verification

Run `gofmt -l .`, `go vet ./...`, `go test -race ./...`, `go mod tidy -diff`, `npm test --prefix scripts`, and `node scripts/format_ui.mjs --check`. Use `scripts/frontend_dummy_backend.py --port 18766` with management key `demo-management-key` for a memory-only UI demo. `scripts/e2e_proxy_location.py --cpa /path/to/cli-proxy-api --plugin /path/to/library` exercises a real local CPA with dummy upstreams.

See [NOTICE](NOTICE) and `licenses/` for attribution and licenses.
