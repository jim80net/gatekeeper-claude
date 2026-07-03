# claude-gatekeeper (product: agent-gatekeeper)

PreToolUse permission hook for coding agents — Claude Code, OpenAI Codex, xAI grok.
Written in Go for fast startup. The binary/repo name is retained for install compat.

## Architecture — harness-agnostic core + per-harness adapters

One policy (the TOML rules) is evaluated by a harness-agnostic engine over a
canonical tool call; thin per-harness adapters translate each harness's native
hook wire in and out. The target harness is chosen by `--harness`
(claude|codex|grok), the `GATEKEEPER_HARNESS` env var, or defaults to `claude`.

- `cmd/claude-gatekeeper/main.go` — CLI entry; selects the adapter, runs the hook
  pipeline (ParseInput → engine.Evaluate → Encode) with the on_error posture and
  a panic-recover; dispatches migrate/setup/uninstall subcommands.
- `internal/canonical/` — **harness-agnostic core**: `Decision` (Abstain/Allow/Deny),
  `ToolCall`, `Verdict`, canonical tool-name constants, `Debugf`. No harness deps.
- `internal/adapter/` — the `Adapter` SPI (`ParseInput`, `Encode`) + `For(harness)`
  factory; sub-packages `claude/` (reference, byte-compatible with old behaviour),
  `codex/` (Claude-compatible wire), `grok/` (grok-native `{"decision":...}` + exit
  codes; abstain routes through grok's fail-open path).
- `internal/protocol/` — the **Claude/Codex** hook wire (HookInput/HookOutput +
  `ExtractInputString`), reused by the claude and codex adapters.
- `internal/config/` — TOML rules + `on_error` knob; harness-neutral path
  resolution (XDG + `~/.claude` fallback; `.gatekeeper/` + `.claude/` project
  overlay); errors on unparseable config (not on missing).
- `internal/engine/` — compiles PCRE2 regexes (regexp2), evaluates a canonical
  ToolCall, deny-always-wins, returns a `canonical.Verdict` (Abstain on no match).
- `internal/migrate/` — converts `settings.json` glob permissions to TOML rules.
- `internal/setup/` — registers/unregisters the hook per harness: `Install` (claude
  `~/.claude/settings.json`), `InstallGrok` (`~/.grok/hooks/gatekeeper.json`),
  `InstallCodex` (`<project>/.codex/hooks.json`) — each with backup.
- `hooks/hooks.json` — Claude Code plugin hook definition (uses `${CLAUDE_PLUGIN_ROOT}`)
- `.claude-plugin/plugin.json` — Plugin manifest (hooks auto-loaded from `hooks/hooks.json`)

### Adding a harness

Implement `adapter.Adapter` (ParseInput normalises the harness tool taxonomy onto
`canonical.Tool*`; Encode writes the harness wire for allow/deny/abstain), register
it in `adapter.For`, add a `setup.Install<Harness>` writer, and add wire golden +
on_error tests. The engine and config need no changes — that is the point of the seam.

### Ship-gates (codex + grok now LIVE-VERIFIED)

Both variant adapters are live-verified (2026-07-03):
- **codex** (0.142.5): `permissionDecision:"deny"` blocks under `approval_policy=never`;
  silent abstain falls through to native policy; both `~/.codex/hooks.json` (global) and
  project `.codex/hooks.json` are read.
- **grok** (0.2.82): a global `~/.grok/hooks/` PreToolUse hook emitting grok-native
  `{"decision":"deny"}` + exit 2 blocks a tool call under `--permission-mode
  bypassPermissions`; abstain (exit 1) falls through. The verified hook stdin schema is
  camelCase (`toolName`="Shell", `toolInput.command`, `hookEventName`="pre_tool_use") and
  the global hook file is Claude-shaped — see the grok adapter/`InstallGrok`. grok's
  settings-layer `--deny` is a separate mechanism, NOT enforced under `--always-approve`.

See README "Harnesses" for the provenance. The grok schema was corrected from an earlier
(wrong) inference by the live probe — a reminder to verify external wire shapes, not infer.

## Plugin structure

This project is a Claude Code plugin. Key files:
- `.claude-plugin/plugin.json` — manifest (no `hooks` field; `hooks/hooks.json` is auto-loaded)
- `hooks/hooks.json` — hook command using `${CLAUDE_PLUGIN_ROOT}/bin/run.sh`
- `bin/run.sh` — wrapper: runs binary → auto-downloads from GitHub Releases → builds from source
- `bin/run.ps1` — PowerShell wrapper for native Windows (same fallback chain)
- `bin/install.sh` — downloads the correct platform binary from GitHub Releases
- `bin/claude-gatekeeper` — binary (from `make build` or `make download`)

Test as a plugin: `claude --plugin-dir .`

## Key design decisions

- **PCRE2 regex** via `github.com/dlclark/regexp2` (pure Go, no cgo)
- **TOML config** with single-quoted strings for zero-escape regex
- **No baked-in rules** — all rules come from config files; `gatekeeper.toml` auto-copied to `~/.claude/` on first run
- **Deny always wins** across all config layers
- **Abstain on error is the default, configurable** — `on_error = "abstain"` (default) emits no verdict so the harness's native permission system decides; `on_error = "deny"` is the opt-in hard posture. A clean no-rule-match always abstains. The gatekeeper never forces allow OR deny on its error path.
- **Abstain is encoded per harness** — claude/codex write nothing + exit 0; grok routes through its documented fail-open path (no verdict, non-deny exit) so no allow is asserted.
- **stdout is the protocol** — all debug/error output goes to stderr
- **Preconditions** allow shell checks (e.g., `git branch --show-current`) for context-dependent rules

## Build and test

```bash
make build        # → bin/claude-gatekeeper (requires Go)
make download     # Download pre-built binary from GitHub Releases
make test         # Race-enabled tests
make plugin-test  # Show command to test as a plugin
make install      # Build + install to ~/.claude/hooks/ (standalone mode)
```

## Config files

- `gatekeeper.toml` — Shipped default rules (auto-copied to `~/.claude/gatekeeper.toml` on first run)
- `~/.claude/gatekeeper.toml` — User global config (deny destructive ops, allow safe tools)
- `.claude/gatekeeper.toml` — Per-project overrides
