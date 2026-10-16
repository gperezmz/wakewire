# wakewire CLI

Every command, its flags, what it prints and how it exits, as v1 builds
them. Each command's `--help` says the same for that command, with
examples.

## Rules every command keeps

- **`--json`** prints exactly one JSON object on stdout. Without it, stdout is
  text for a person. Messages and progress go to stderr, never stdout.
- **Errors** under `--json` print
  `{"error": {"code": "<code>", "message": "<what>", "hint": "<the fix>"}}`
  and exit non-zero. Every hint names the command or the configuration line
  that fixes it.
- **Never interactive**, except `account add` reading a token from a hidden
  prompt when stdin is a terminal. No pager, and no colour when stdout is not
  a terminal.
- **Safe to repeat**: a command run twice leaves the state it left once.
- **`--dry-run`** on every command that changes state shows what it would do
  and changes nothing.
- **Durations** are Go durations (`90s`, `30m`, `2h`) or a number of days
  (`7d`).
- **`--help`** on every command says what it does, when to use it, its flags,
  examples, exit codes and the related commands.
- **No token is ever printed**, in any output or error, not even a prefix.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Done, including "already in that state" |
| `1` | Usage: an unknown flag, a missing argument, an invalid value |
| `2` | Refused: the request is valid but the state forbids it, such as a watch held by a live session |
| `3` | Not set up: no source section, no matching account, no stored token |
| `4` | A service failed: refused the token, did not answer, or rate-limited |

## Error codes

| Code | Exit | When |
|---|---|---|
| `invalid_value` | 1 | A flag's value is outside its form |
| `no_session` | 3 | No harness session was found: `CLAUDE_CODE_SESSION_ID` is unset and no `--session` was given |
| `no_source` | 3 | No file in the source template's `files` has its section |
| `source_incomplete` | 3 | The section lacks the tracker line or a label row; the message names which |
| `no_account` | 3 | No account covers the repository's host and owner, or the tracker's host |
| `no_stored_token` | 3 | An account says `token = "stored"` and `credentials.toml` has no entry for it |
| `config_invalid` | 3 | `config.toml` or `credentials.toml` does not parse, or two accounts share a name or a host and owner |
| `no_token` | 3 | An account's `env:<VAR>` is empty or unset, or `gh` gives no token for its `gh:<login>` |
| `account_exists` | 2 | `account add` names an account that exists with other settings |
| `account_conflict` | 2 | `account add` claims a host and owner another account already covers |
| `no_such_account` | 2 | `account rename` names no account |
| `held` | 2 | Another live session holds the watch |
| `not_registered` | 2 | The command names a watch that has no registration |
| `token_refused` | 4 | A service answered 401 or 403 |
| `unreachable` | 4 | A service did not answer within its timeout |
| `rate_limited` | 4 | A service's remaining budget is spent; the message names when it resets |

## Commands for agents and people

### `wakewire register`

Makes the calling session the holder of the watch for this repository and
the identity its account reads as. Run from the repository, once per session,
when the work that needs waking starts. Running it again refreshes the
registration and keeps everything waiting.

```text
wakewire register [--harness <claude-code>] [--session <id>] [--repo <host/path>]
                  [--tracker <spec>] [--label <meaning>=<label>]...
                  [--marker <line>] [--take-over] [--dry-run] [--json]
```

| Flag | Default | Meaning |
|---|---|---|
| `--harness` | detected: `claude-code` where `CLAUDE_CODE_SESSION_ID` is set | Which adapter wakes the session |
| `--session` | `$CLAUDE_CODE_SESSION_ID` | The session to register |
| `--repo` | the `origin` remote, as `<host>/<path>` | The repository to watch |
| `--tracker` | the source template's tracker line | `github-issues`, `gitlab-issues` or `jira:<site>/<project key>` |
| `--label` | the source template's table | One label, as `spec=type::spec`; repeatable; meanings are `spec`, `ready`, `in-progress`, `needs-input` |
| `--marker` | `[echo] marker` in `config.toml` | The line that ends every comment the agents write; see [the marker](configuration.md#the-marker) |
| `--take-over` | off | Take the watch from a live holder |

The process id and its start time come from `$CLAUDE_PID`. Without `$CLAUDE_PID` it takes its own
parent process. It prints what it read and from which file and line, with
`--dry-run` as `would register`:

```text
$ wakewire register --dry-run
read AGENTS.md, section "Forge and tracker" (line 5)
  tracker      github-issues  (line 7)
  spec         type::spec     (line 11)
  ready        ready-for-dev  (line 12)
  in-progress  in-progress    (line 13)
  needs-input  needs-input    (line 14)
forge   github.com/gperezmz/wakewire-sandbox as gperezmz (account personal)
tracker github-issues
would register claude-code:3f1c2a9e-7b4d-4e2a-9c1f-5a8b6d0e2f41 (pid 3681747)
adapter: not loaded. Nothing in this session will poll until the wakewire mod is installed and enabled.
```

Where the tracker is Jira, the tracker line reads
`tracker <site>/<KEY> as <display name> (account <name>)`.

Under `--json` it prints `{"outcome": "registered" | "refreshed" | "taken-over", "dryRun", "watch", "identity", "tracker", "forge", "trackerAs", "holder", "pid", "watched": true|false, "notWatched": [{code, text}], "adapter": "loaded" | "not-loaded", "paused": null | "<since>", "source": {file, heading, headingLine, values}, "replaced"}`;
`replaced` names the session a takeover took the watch from.
`adapter: not-loaded` means nothing in this session will poll: the mod is not
installed or not enabled. A live holder exits 2 with `held`, naming the
holder's session, process and when it was last heard from. A session holds
one watch: registering another ends its earlier registration.

### `wakewire unregister`

```text
wakewire unregister [<repo>] [--identity <id>] [--dry-run] [--json]
```

Forgets a watch: its registration, snapshots and undelivered changes. The
ledger keeps what was recorded until it ages out. Ending a session never
needs it; a session's end only makes its registration dormant.

### `wakewire pause` and `wakewire resume`

```text
wakewire pause  [<repo>] [--for <duration>] [--identity <id>] [--dry-run] [--json]
wakewire resume [<repo>] [--identity <id>] [--dry-run] [--json]
```

Pausing keeps polling and storing changes but delivers none. Resuming, or the
end of `--for`, delivers everything held as one wake. Without `<repo>`, the
watch of the current repository. `--identity` picks one of two identities
watching one repository; without it, the watch the calling session holds is
preferred.

### `wakewire status`

```text
wakewire status [<repo>] [--session <id>] [--identity <id>] [--json]
```

Every watch, or one: its holder and whether it is alive, held or dormant,
the last and next poll, each account it reads as and whether that token can
write, the remaining rate-limit budget per account, pause, pending changes and
problems. `--session` answers for the watch a session holds, if any.

```text
github.com/gperezmz/wakewire-sandbox as gperezmz	tracker github-issues
  held by   claude-code:60f9edbc-2694-4e83-9c36-00da6b36cc3c (pid 3921642, heard 15s ago)
  polled    22:58:33, every 30s, next 22:59:03
  forge     GitHub · personal (stored, can write) · 4840/5000 left
  pending   0 · held 0 · paused no
  problems  none
```

A watch whose tracker is Jira has a `tracker` line too. An open problem is
shown as `problem <code> since <time>: <text>`, with a `fix` line under it.

### `wakewire ledger`

```text
wakewire ledger [<repo>] [--since <duration>] [--item <ref>] [--identity <id>] [--json]
```

Every change stored for a watch and what was done with it: delivered (with
the batch), ignored (with the reason), held, or pending. Never what anyone
wrote.

### `wakewire changes`

```text
wakewire changes <batch> [--json]
```

The full batch, in the [wake contract](wake-contract.md)'s format, including
what people wrote. This is how an agent in a harness without the mod's tool
reads a batch.

### `wakewire interval`

```text
wakewire interval [<repo>] <duration|default> [--identity <id>] [--dry-run] [--json]
```

How often a watch is polled, from `30s`; `default` returns it to the
configuration's value. Takes effect on the next tick.

### `wakewire account`

```text
wakewire account add <name> --host <host> [--owner <owner>]... [--email <email>]
                     [--user <identity>] [--token stored|env:<VAR>|gh:<login>]
wakewire account list [--json]
wakewire account rename <old> <new> [--dry-run] [--json]
wakewire account remove <name> [--dry-run] [--json]
```

`add` writes the account to `config.toml`. With `--token stored`, the
default, it reads the token from a hidden prompt, or from stdin when stdin is
not a terminal, never from a flag. It checks whose the token is, that it can
read what the account covers and whether it can write, and stores it in
`credentials.toml` only then. Run again with the same flags, it changes
nothing; run for an existing account with `--token stored` and another
token, it replaces the stored token, which is how a refused token is
replaced. `rename` changes both files together. `remove` deletes the account
and its stored token, and of a name no account has, any stored token left
under it.

### `wakewire doctor`

```text
wakewire doctor [--json]
```

Checks, in order, and names the fix for each failure: the configuration
parses; every account's token resolves, belongs to its identity and can read
what it covers; this repository's source section parses; the binary and the
mod speak the same protocol; no stored token is unused; the rate-limit budget
is not nearly spent; and no watch has a problem open. The first command to
run when anything looks wrong.

It prints each check as `ok`, `warn` or `FAIL` with the fix, or under
`--json` `{"ok": true|false, "checks": [{name, ok, skipped, detail, code, fix}]}`,
and exits 0 when every check passed or only warned, 3 when something is not
set up, and 4 when a service failed.

### `wakewire paths`, `wakewire prune`, `wakewire version`

```text
wakewire paths   [--json]               # where config, credentials and state are
wakewire prune   [--dry-run] [--json]   # the daily clean-up, run now
wakewire version [--json]               # version and protocol number
```

## Commands for adapters

These are called by an adapter, never by an agent. `--help` lists them under
their own heading saying so.

| Command | Does |
|---|---|
| `wakewire adapter hello --session <id> [--protocol <n>]` | Records that an adapter is loaded for a session, with its parent process as the session's process and the protocol it speaks; answers whether the session holds a watch, the binary's protocol and the status line's facts |
| `wakewire adapter next --session <id> [--reader tool\|cli]` | The tick: records the adapter as heard from, polls if due within a 60 s budget, and returns `{"state": "armed" \| "idle" \| "taken-over", "by", "polled", "batch": null \| {id, wake, count}, "status"}`. `--reader` says how the wake names reading the batch: `tool`, the mod's tool, or `cli`, the default, `wakewire changes` |
| `wakewire adapter ack <batch>` | Marks a batch delivered |
| `wakewire adapter ended --session <id> --reason <reason>` | Records that the session ended, making its registration dormant |
