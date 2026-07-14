# Grok PreToolUse input goldens

These minimal hook envelopes are derived from Grok 0.2.101's shipped
`10-hooks.md` contract (camelCase envelope; `toolName` is the real native tool
name) and tool schemas captured in the read-only local session state. They are
static-source fixtures, not new live probes.

`pre_tool_use_shell_live.json` is the older verbatim Grok 0.2.82 live capture.
The other fixtures pin the primary matchable field published by each captured
0.2.101 schema. WebSearch has no fixture because its primary input field could
not be authoritatively verified without live probing.
