# agent-gatekeeper

A fast PreToolUse permission hook for coding agents — [Claude Code](https://claude.com/claude-code), [OpenAI Codex CLI](https://github.com/openai/codex), and [xAI grok](https://x.ai) — that replaces glob-based permission arrays with PCRE2-compatible regex rules. One policy, enforced across every harness.

> The product is **agent-gatekeeper**; the binary and repository keep the `claude-gatekeeper` name for install compatibility.

## Why

Agent permission globs (`Bash(git add:*)`) can't match env-prefixed commands like `FOO=bar git commit`, pipe chains, `HEAD:main` refspecs, or complex argument patterns. Regex can.

**agent-gatekeeper** evaluates every tool call against a layered set of regex rules and returns `allow`, `deny`, or **abstain** (no opinion — the harness's native permission system decides). **Deny always wins.** When a tool call is denied, the agent sees the reason and can adjust.

## Harnesses

One binary serves three harnesses, selected by `--harness` (or the `GATEKEEPER_HARNESS` env var; defaults to `claude`). Tool names are normalised to a canonical taxonomy inside each adapter, so the **same `gatekeeper.toml` rules apply verbatim** to all three.

| Harness | Wire | Status |
|---------|------|--------|
| `claude` | `hookSpecificOutput.permissionDecision` (exit 0) | **Stable.** Byte-for-byte compatible with prior releases. |
| `codex` | Same Claude-compatible `hookSpecificOutput` wire | **Built + unit-tested against the documented wire.** Ships after live verification post-`codex login` — that `permissionDecision:"deny"` blocks, the global-vs-project hook location, and whether an empty response defers to `approval_policy` (see below). |
| `grok` | grok-native `{"decision":"deny","reason":...}` (exit 2 deny / exit 0 allow) | **Built + unit-tested against the documented wire.** Ships after one live probe confirms a blocking hook / explicit deny fires under grok `--always-approve`, and how grok treats a silent exit-0 (allow vs defer). |

The codex and grok adapters are complete and covered by wire-shape golden tests, but are **not** yet claimed as live-verified — the remaining checks require an authenticated codex session and a (paid) grok inference probe respectively. Do not treat them as load-bearing on an auto-approve agent until those probes pass; pair the hook with a server-side control (e.g. GitHub branch protection).

Register the hook per harness:

```bash
gatekeeper setup --harness claude                    # writes ~/.claude/settings.json (default)
gatekeeper setup --harness grok                       # writes ~/.grok/hooks/gatekeeper.json
gatekeeper setup --harness codex --project-dir .      # writes ./.codex/hooks.json
```

Grok requires the project folder to be `/hooks-trust`ed; codex requires persisted hook trust (or `--dangerously-bypass-hook-trust` for vetted automation).

## Install

### From a marketplace

```shell
/plugin marketplace add jim80net/claude-plugins
/plugin install claude-gatekeeper@jim80net-plugins
```

Pre-built binaries for Linux, macOS, and Windows (amd64/arm64) are auto-downloaded from GitHub Releases on first run. Default rules are auto-copied to `~/.claude/gatekeeper.toml` on first run.

**Windows (PowerShell)**: If you're on native Windows without Git Bash, edit `hooks/hooks.json` and change the command to:
```
powershell -NoProfile -ExecutionPolicy Bypass -File ${CLAUDE_PLUGIN_ROOT}/bin/run.ps1
```

### Local development

```bash
git clone https://github.com/jim80net/claude-gatekeeper.git
cd claude-gatekeeper
make build
claude --plugin-dir .
```

### From a GitHub release

Download a pre-built archive from [Releases](https://github.com/jim80net/claude-gatekeeper/releases), extract it, and point Claude Code at the extracted directory:

```bash
claude --plugin-dir /path/to/claude-gatekeeper
```

## How it works

1. The harness invokes the gatekeeper before each tool call, sending JSON on stdin.
2. The `--harness` adapter parses that harness-native JSON into a canonical tool call (normalising the tool name, e.g. grok's `run_terminal_cmd` → `Bash`).
3. On first run, the shipped `gatekeeper.toml` is auto-copied to `~/.claude/gatekeeper.toml` if no global config exists.
4. Rules are loaded and layered (see [Config layering](#config-layering)).
5. Each rule has a `tool` regex (matched against the canonical tool name) and an `input` regex (matched against the command/file path/URL).
6. **Deny always wins**: if any deny rule matches, the call is blocked and the agent is told why.
7. If any allow rule matches (and no deny), the call is auto-approved.
8. If nothing matches (or no config exists), the gatekeeper **abstains** — it writes no verdict and the harness's native permission system decides.
9. On any gatekeeper *error*, the [`on_error`](#error-behaviour-on_error) posture decides (default: abstain).

## Default rules

The shipped `gatekeeper.toml` (auto-installed to `~/.claude/gatekeeper.toml` on first run) **denies**:

| Category | Examples |
|----------|----------|
| Destructive git | `git reset --hard`, `git clean -f`, `git push --force`, `git commit --amend`, `git branch -D` |
| Push to main/master | A boundary regex covering `origin main`, `-u origin main`, `HEAD:main`, `main:main`, `:main` (delete), `+main`, `refs/heads/main`, `git -C <path> push origin main`, and implicit (on main branch, bare `git push`) — while allowing branches merely named `main-feature`/`mainline` |
| Recursive delete | `rm -r`, `rm -rf` |
| sed/awk | Forces the Edit tool instead |
| Destructive SQL | `DROP`, `TRUNCATE`, `DELETE FROM` |
| npm | Use pnpm instead (commented out by default — uncomment to enable) |
| Credential files | `.env`, `.envrc`, `*key.json`, `id_rsa`, `.pem`, `credentials` |

And **allows**:

| Category | Examples |
|----------|----------|
| Version control | `git`, `gh` |
| Containers | `docker`, `docker-compose` |
| Python | `python`, `uv`, `pip`, `pytest` |
| Go | `go build`, `go test`, `golangci-lint` |
| JavaScript/TypeScript | `node`, `npx`, `pnpm`, `eslint`, `vitest` |
| Build systems | `make`, `cargo`, `gradle`, `mvn` |
| Infrastructure | `terraform`, `kubectl`, `helm`, `aws`, `gcloud` |
| Shell utilities | `ls`, `find`, `mkdir`, `curl`, `diff`, `wc`, `jq`, `openssl`, `timeout` |
| Non-Bash tools | `Read`, `Edit`, `Write`, `Glob`, `Grep`, `Agent`, `WebFetch` |

## Configuration

### Rule format

```toml
[[rules]]
tool     = 'Bash'                        # PCRE2 regex matching tool_name
input    = 'git\s+reset\s+--hard'        # PCRE2 regex matching the primary input
decision = "deny"                        # "allow" or "deny"
reason   = "Destructive: git reset"      # Shown to Claude on deny
```

### Preconditions (shell checks)

For rules that need runtime context (e.g., checking the current git branch):

```toml
[[rules]]
tool              = 'Bash'
input             = '\bgit\s+push\b(?!.*\b(main|master)\b)'
precondition      = 'git branch --show-current'
precondition_match = '^(main|master)$'
decision          = "deny"
reason            = "Implicit push to main/master"
```

The `precondition` command runs only when `tool` and `input` both match. It has a 5-second timeout.

### Env-prefix aware variants

Commands like `FOO=bar git commit` bypass anchored patterns. The defaults include commented-out variants:

```toml
# Default (anchored):
input = '(?:^|[|;&]\s*)git\s'

# Env-prefix aware (uncomment to enable):
# input = '(?:^|(\w+=\S+\s+)*)git\s'
```

### Config layering

Paths are harness-neutral, with a back-compatible `~/.claude` fallback. For each layer the first path that exists is used:

| Layer | Path (first that exists) | Scope |
|-------|--------------------------|-------|
| Global | `${XDG_CONFIG_HOME:-~/.config}/gatekeeper/gatekeeper.toml`, then `~/.claude/gatekeeper.toml` | All projects. `~/.claude` is also the first-run write target, so existing installs are undisturbed. |
| Project | `<cwd>/.gatekeeper/gatekeeper.toml`, then `<cwd>/.claude/gatekeeper.toml` | Per-project (rules appended to global; scalar knobs like `on_error` overridden). |

Deny always wins across all layers. If no config files exist, the gatekeeper abstains on everything.

### Error behaviour (`on_error`)

A top-level knob controls what the gatekeeper emits when it *itself* fails — unparseable stdin, missing/unparseable config, a bad rule regex, an evaluate error, or a recovered panic. A clean "no rule matched" is **not** an error and always abstains.

```toml
on_error = "abstain"   # default — emit NO verdict; the harness's native permission system decides.
# on_error = "deny"    # opt-in hard posture — on any error, emit an explicit deny.
```

The default is `abstain`: the gatekeeper never decides *for* the permission system on its error path (it forces neither allow nor deny). Each harness encodes abstain natively — Claude/Codex write nothing and exit 0 (their native flow runs); grok has no first-class defer, so its adapter routes abstain through grok's documented fail-open path (no verdict, non-deny exit) so no allow is ever asserted.

> On an auto-approve agent (grok `--always-approve`, codex `approval_policy=never`) the *native* decision is auto-run, so under the default `abstain` a gatekeeper error is a no-op there. If you need a hard floor in that setup, set `on_error = "deny"` **and** keep a server-side control (e.g. GitHub branch protection) — the hook alone cannot defend against not being invoked.

### Security: config trust boundaries

- **Global config** (`~/.claude/gatekeeper.toml`) — trusted, controlled by you.
- **Project config** (`.claude/gatekeeper.toml`) — comes from the repository. A malicious repo could add allow rules or precondition commands that execute shell commands. Review project configs before trusting them. Precondition commands run with a 5-second timeout.

## Migrating from settings.json

If you have existing `permissions.allow` / `permissions.deny` globs in your settings:

```bash
claude-gatekeeper migrate
```

This reads `~/.claude/settings.json` and `settings.local.json`, converts permission globs to regex rules, and writes `~/.claude/gatekeeper.toml`. A backup is created if the output file already exists.

Options:
```bash
claude-gatekeeper migrate --settings /path/to/settings.json --output /path/to/output.toml
```

Review the generated TOML — some globs may need manual refinement.

## Debugging

Run with `--debug` to see rule evaluation on stderr:

```bash
# Test manually:
echo '{"tool_name":"Bash","tool_input":{"command":"git push --force"},"cwd":"/tmp"}' | claude-gatekeeper --debug

# Enable in the plugin by editing hooks/hooks.json:
"command": "${CLAUDE_PLUGIN_ROOT}/bin/claude-gatekeeper --debug"
```

Debug output goes to stderr (visible in Claude Code verbose mode via `Ctrl+R`).

## Development

```bash
make build        # Build from source to ./bin/claude-gatekeeper
make download     # Download pre-built binary from GitHub Releases
make test         # Run all tests with race detector
make lint         # Run golangci-lint
make plugin-test  # Show command to test as a plugin
make clean        # Remove build artifacts
```

## License

MIT
