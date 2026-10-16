# Design decisions

Each choice the design rests on, what it was chosen over and why, and what is
still to be proven by running it. The facts behind them were checked on
2026-10-16 against Claude Code 2.1.301, its mod type declarations
(`mods/types/claude-code.d.ts` in `anthropics/claude-code`) and the services'
API documentation.

## Waking a Claude Code session

| Option | Verdict | Why |
|---|---|---|
| **A mod calling `$.prompt.submit`** | **Chosen** | It runs inside the session, so it ends with it. A plugin's prompt "runs once it is idle", and the call resolves once the prompt is queued or entered, or returns `{ drop }` when a hook refused it. Mods also give the timer, the status line, a slash command and a tool of their own |
| A `Stop` hook with `asyncRewake` | Fallback | Wakes the model on exit code 2 with the hook's output, but runs one process per turn in every session and needs a lock against duplicates |
| The `Monitor` tool | Rejected | A monitor expires after 30 minutes at most and the agent must re-arm it: a turn spent every half hour |
| Channels (an MCP server pushing events) | Rejected for v1 | A research preview; Team and Enterprise organizations must enable it, and a custom channel needs a development flag or an organization allowlist |
| The local peer socket in `~/.claude/sessions/` | Rejected | Undocumented and internal |
| `claude -p --resume <id>` from outside | Rejected | Conflicts with an open interactive session |

Mods are early access: they load only where function hooks are enabled, and
their API may change between releases without notice. The binary and its
contract keep that risk inside the adapter.

## The binary

| Decision | Chosen over | Why |
|---|---|---|
| **Go** | Rust; the existing TypeScript compiled with Bun | The work is HTTP polling and SQLite. Go gives one static binary with a pure-Go SQLite (`modernc.org/sqlite`, no cgo) and plain cross-compiling |
| **No daemon** | A background service | The session is the clock (see [how it works](how-wakewire-works.md)). A daemon only becomes necessary for webhooks, which v1 does not use |
| **Polling, not webhooks** | Webhooks | Webhooks need something always listening and reachable; polling needs only a token |
| **Diffing contents** | Time cursors such as `since` and `updated_after` | No service documents which edits move `updated_at`; see [sources](../reference/sources.md) |
| **Store at discovery, deliver by acknowledgement** | Advancing a cursor only after delivery | Nothing found ever lives only in memory, and a crash mid-poll loses nothing |
| **The adapter ticks, the binary decides** | The adapter keeping the interval | Changing the interval needs no reload, and a failed delivery is retried within 30 s |

## The mod

| Decision | Chosen over | Why |
|---|---|---|
| **No `$.wakewire` noun in v1** | A noun with `status()`, `recent(n)`, `pause()`, `resume()` and an event per delivered batch, for other mods | No mod reads it yet. A noun is built in `engine.create`, where `$` is still empty, and the mod API has no way for a mod to raise an event of its own, so the per-batch event cannot be built as planned. Other mods and programs read `wakewire status --json` and `ledger --json` |
| **The note after compaction from a function hook** | A `SessionStart` command hook beside the module | One hooks module holds all of the mod; `classic.SessionStart` carries `additionalContext` the same way |

## Registration

| Decision | Chosen over | Why |
|---|---|---|
| **The agent runs `wakewire register`** | The mod registering | The skill's one line works in every harness; only the adapter differs |
| **Session id and process id from the environment** | The agent passing them; the mod rewriting the command | Claude Code sets `CLAUDE_CODE_SESSION_ID` (updated on `/clear`) and `CLAUDE_PID` in every Bash command |
| **A watch keyed by repository and identity** | Keyed by repository | Two people's partitions never overlap, so their watches must not refuse each other |
| **Refuse a live holder, offer `--take-over`** | Silently taking over; refusing forever | Two holders would wake two sessions for one partition; a stuck holder needs a way out |
| **`/clear` makes the registration dormant** | Waking the cleared session | A cleared session no longer knows it holds a watch, and would improvise on a wake |

## Configuration

| Decision | Chosen over | Why |
|---|---|---|
| **`AGENTS.md` as the project's configuration, through a source template** | A per-repository `.wakewire.toml` | The labels already live in `AGENTS.md`; a second copy would drift. The template lets another layout be read without code |
| **Global configuration only, plus flags** | A per-repository file | No repository needs one yet |
| **`~/.wakewire/`, moved by `WAKEWIRE_HOME`** | The XDG directories | One folder, like `~/.claude` and `~/.bb` |
| **Tokens stored in `credentials.toml` (mode 600)**, or from `gh` or an environment variable | Never storing tokens; an OS keyring | `gh` stores its tokens the same way. A separate file keeps tokens out of a configuration people share. A keyring is more than v1 needs |
| **No `glab` token source** | Borrowing `glab`'s login | `glab` 1.114.0 has no command that prints a token, and keeps it in the OS keyring by default |
| **The marker is run-fleet's existing closing line, configurable** | A marker of wakewire's own | run-fleet's templates already end every comment on `<kbd>🤖 unattendish agent</kbd>` and its review rounds rely on it; a second marker would change run-fleet for nothing |
| **Fixed poll defaults** | Configurable wake settings | Nothing yet calls for changing them |

## What a wake carries

| Decision | Chosen over | Why |
|---|---|---|
| **Facts in the wake, content in the batch** | Comment text in the wake | A wake enters as its user's turn; content belongs in tool output |
| **The batch served by the mod's own tool** | A file the agent reads | `Read` asks permission for a path outside the project; a mod can register its own tool (`$.tool.register`) |
| **wakewire's own change format** | The services' JSON | One GitHub comment object carries about 40 fields, mostly URLs; Jira bodies are Atlassian Document Format; three shapes for one thing |
| **Ignore the in-progress and needs-input labels entirely** | Ignoring by actor | When agents act as the identity, a label change cannot be told apart by who made it, and only agents set those two |
| **Pull requests by author** | A fleet label on pull requests | The agents open pull requests as the identity, so authorship already marks them |
| **Spec edits wake, held until stable** | Ignoring edits | A spec edited mid-build is a steer; a burst of saves becomes one change |
| **No CI checks in v1** | Waking on checks | They are frequent, and the agent reads them when it reads a pull request |
| **Comment edits and deletions ignored in v1** | Waking on them | Rare, and none of the services records them reliably |

## Proven by running it

Claims the design rested on that a run has since settled, with what it
showed. The Claude Code claims were run in interactive Claude Code 2.1.302
sessions on Linux, first with a spike mod and then with the wakewire mod
loaded by `claude --plugin-dir`; the service claims against the sandboxes.

| Claim | Found |
|---|---|
| A wake submitted by a mod enters an idle session and starts a turn | It does. `$.prompt.submit` from a `$.clock.after` timer resolved within 50 ms with `origin: { kind: 'plugin' }`, and the model began its turn about 2 s later with no keystroke. The model reads it framed as "The wwspike plugin sent a message:", followed by the text and a line saying a plugin's prompt starts the turn in the user's place |
| Calling the mod's tool needs at most one "always allow" | It needs none. A tool registered with `$.tool.register` and answered by the mod's own `tool.call` hook ran with no permission prompt, in the default (manual) permission mode, with no allow rule in any settings file. Its result must be a string: an object is refused as not matching the tool's output shape |
| The binary takes the session's process id from its parent when the mod runs it | It can. A child started by `$.process.run` has the `claude` process as its parent, the same process `CLAUDE_PID` names in a Bash command |
| `/clear` is seen by the mod | `session.end` fires with `reason: 'clear'` and the old session id, and later Bash commands see the new `CLAUDE_CODE_SESSION_ID` while `CLAUDE_PID` stays the same |
| `adapter next` every 30 s is unnoticeable in the session | It is. Over two sessions of about fifteen minutes the ticks added no transcript row and no prompt; the status line alone changed |
| Plain pushes to a pull request change its head in the list responses on time | On GitHub, yes: the pull requests list showed the pushed head on the next poll. On GitLab 17.11, no: the merge requests list kept a merge request's old `sha` and comment count for over ten minutes after a push or a comment, so the GitLab source reads each open merge request alone on every poll; see [sources](../reference/sources.md#gitlab) |
| Jira's `comment` field reports a total on `search/jql` | It does: `comment.total` comes with every issue, `maxResults` 0, so comments are read only when the total moved |
| GitHub's list responses change their ETag when only a label or the body changes | They do: a label-only change and a body-only edit each gave a `200` with a new ETag on the next poll, and idle polls answered `304`, so conditional requests stay |

## Not proven until it runs

Each of these is a claim about a harness or a service that only a run
settles, with what changes if it fails:

| Claim | If false |
|---|---|
| Who made a plain push can be named | Report the commits' authors instead, as v1 does |
| Mods load inside bb's Claude Code sessions | The bb adapter is a bb plugin, as planned anyway |
