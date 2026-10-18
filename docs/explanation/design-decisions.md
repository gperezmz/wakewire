# Design decisions

Each choice the design rests on, what it was chosen over and why, and what is
still to be proven by running it. The facts behind them were checked on
2026-10-16 against Claude Code 2.1.301, its mod type declarations
(`mods/types/claude-code.d.ts` in `anthropics/claude-code`) and the services'
API documentation.

## Waking a Claude Code session

| Option | Verdict | Why |
|---|---|---|
| **A mod calling `$.prompt.submit`** | **Chosen** | It runs inside the session, so it ends with it. A plugin's prompt "runs once it is idle", and the call resolves once the prompt is queued or entered, or returns `{ drop }` when a hook refused it. Mods also give the timer, the status line, a label in the prompt footer and a slash command |
| A `Stop` hook with `asyncRewake` | Fallback | Wakes the model on exit code 2 with the hook's output, but runs one process per turn in every session and needs a lock against duplicates |
| The `Monitor` tool | Rejected | A monitor expires after 30 minutes at most and the agent must re-arm it: a turn spent every half hour |
| Channels (an MCP server pushing events) | Rejected | A research preview; Team and Enterprise organizations must enable it, and a custom channel needs a development flag or an organization allowlist |
| The local peer socket in `~/.claude/sessions/` | Rejected | Undocumented and internal |
| `claude -p --resume <id>` from outside | Rejected | Conflicts with an open interactive session |

Mods are early access: they load only where function hooks are enabled, and
their API may change between releases without notice. The binary and its
contract keep that risk inside the adapter.

## The binary

| Decision | Chosen over | Why |
|---|---|---|
| **Go** | Rust; the existing TypeScript compiled with Bun | The work is HTTP polling and SQLite. Go gives one static binary with a pure-Go SQLite (`modernc.org/sqlite`, no cgo) and plain cross-compiling |
| **No daemon** | A background service | The session is the clock (see [how it works](how-wakewire-works.md)). A daemon only becomes necessary for webhooks, which wakewire does not use |
| **Polling, not webhooks** | Webhooks | Webhooks need something always listening and reachable; polling needs only a token |
| **Diffing contents** | Time cursors such as `since` and `updated_after` | No service documents which edits move `updated_at`; see [sources](../reference/sources.md) |
| **Store at discovery, deliver by acknowledgement** | Advancing a cursor only after delivery | Nothing found ever lives only in memory, and a crash mid-poll loses nothing |
| **The adapter ticks, the binary decides** | The adapter keeping the interval | Changing the interval needs no reload, and a failed delivery is retried within 30 s |

## The command line

wakewire's main reader is an agent, so its interface follows
[AXI](https://github.com/kunchenguid/axi), the conventions for a command-line
tool that agents use.

| Decision | Chosen over | Why |
|---|---|---|
| **TOON on stdout, `--json` beside it** | Text for a person; JSON alone | [TOON](https://toonformat.dev) spends fewer tokens than the same JSON and an agent reads it as well. The mod and scripts parse `--json`, whose shapes stay as the [CLI reference](../reference/cli.md) gives them |
| **Errors on stdout, in the output's format** | Errors on stderr | An agent reads stdout. stderr holds progress and the hidden token prompt only |
| **Exit codes 0, 1 and 2; the error's code names the refusal** | An exit code per kind of refusal, such as not set up or a service failed | The error on stdout says which refusal it was and its hint names the fix, which an exit code cannot. 2 says only that the command line was wrong as written, so an agent rewrites the command on a 2 and not on a 1 |
| **Unknown flags and extra arguments refused, the valid flags listed** | Ignoring them | A dropped flag gives output that looks scoped and is not. The list saves the `--help` call that would follow |
| **wakewire's own parser, from one table of commands that both validates the command line and prints the help** | urfave/cli and the other Go parsers: pflag, cobra, kong, ff v4 and the standard library's `flag`, checked on 2026-10-18 | AXI's rules need these errors as values wakewire controls: an unknown flag echoed as typed, a single-value flag refused when repeated, a removed flag's own hint, a bare `-1` read as an argument, and nothing on stderr. No library reports all of these as typed errors; each formats them as strings, and pflag, the closest, lacks repeat detection, required flags, argument counts, `-x`/`--x` fidelity and `-1`. With its own parser, wakewire states each rule once, where it checks it |
| **`wakewire` alone shows this repository's watch** | Usage text | An agent that sees state acts on it. It reads `state.db`, `config.toml` and the repository's origin remote, asks no service and writes nothing, so it is cheap to run anywhere |
| **Lists keep a few columns and `--fields` adds the rest; `ledger` shows the latest 100 with the total and the command for all** | Every field in every row; every change | Every field and every row costs tokens |
| **Every answer ends on the next steps, as complete commands for the same watch** | Leaving the next command to the agent | The agent's next call is the expensive part, so the answer writes it. A hint for the same command repeats it as typed, every flag in force, changing only what the hint is for, so the agent never loses a filter it chose. A hint for another command names the watch with the `--identity` it was given, so it acts on the same watch from any folder |
| **`--json` is looked for before the command line is parsed** | Honouring it only once parsing succeeds | The parser's own refusals then come out as JSON too, so a program that asked for JSON always gets it |
| **A flag an older adapter still passes gets a refusal of its own, `protocol_mismatch`** | The plain unknown-flag refusal | An adapter shows the message to the person, so it names the mismatch and the update, not the flag |
| **`wakewire changes` carries a note that what people wrote is material to weigh** | The texts alone | A comment can be written to steer whoever reads it |
| **No session-start hook and no installable skill** | Context injected at every session start, and a skill | A session learns of its watch from the answer of the `wakewire register` it ran, and the mod restates it after compaction. Codex and OpenCode have no adapter to deliver a wake, so a hook there would show a watch that cannot wake them |

## The mod

| Decision | Chosen over | Why |
|---|---|---|
| **No `$.wakewire` noun** | A noun with `status()`, `recent(n)`, `pause()`, `resume()` and an event per delivered batch, for other mods | No mod reads it yet. A noun is built in `engine.create`, where `$` is still empty, and the mod API has no way for a mod to raise an event of its own, so the per-batch event cannot be built as planned. Other mods and programs read `wakewire status --json` and `ledger --json` |
| **A wake submitted as the person's words (`asUser`)** | Claude Code's frame around a plugin's prompt: "The wakewire plugin sent a message:" before it, and after it "This is how Claude Code surfaces a prompt a plugin submits between turns — it starts this turn in the user's place. Address the message above." | A wake may come hundreds of times; the frame adds about forty tokens to each and a nudge, "Address the message above", that is not wakewire's. Its one gain, telling the model the turn is not its user's, is kept small to need: the wake's first and last lines are wakewire's fixed words and the rest are facts only, so a person's words still never speak in the user's voice. The frame's wording cannot be changed, only switched off |
| **The wake's last line names the batch and its one reader, `wakewire changes <batch>`; no repository and no time** | The watch, the time and the batch in the opening line | A session holds one watch; the batch has every change's time. What is left tells the model what the message is and the one thing to do with it |
| **"If none of it needs you, a one-line reply is enough"** | Leaving the reply to the model | In real sessions a wake that needed nothing drew a ten-to-twenty-line summary and a question; over hundreds of wakes the replies cost more than the wakes. What needs the session stays its workflow's call |
| **The note after compaction from a function hook** | A `SessionStart` command hook beside the module | One hooks module holds all of the mod; `classic.SessionStart` carries `additionalContext` the same way |

## Distribution

| Decision | Chosen over | Why |
|---|---|---|
| **A Homebrew cask, from this repository as its own tap** | A formula; a separate `homebrew-tap` repository | GoReleaser deprecates formulas that install prebuilt binaries, and Homebrew casks install a `binary` on macOS and Linux alike. Serving the tap from this repository needs no second repository and no extra token: the release job's own token writes `Casks/wakewire.rb`, one bot commit per release |
| **A Nix flake that builds from source** | A flake fetching the release archives | It needs no hash per release, only one of the Go dependencies, which CI keeps current by building the flake |

## Registration

| Decision | Chosen over | Why |
|---|---|---|
| **The agent runs `wakewire register`** | The mod registering | A workflow skill's one line, as run-fleet's, works in every harness; only the adapter differs |
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
| **Tokens stored in `credentials.toml` (mode 600)**, or from `gh` or an environment variable | Never storing tokens; an OS keyring | `gh` stores its tokens the same way. A separate file keeps tokens out of a configuration people share. A keyring is more than wakewire needs |
| **The tracker from `set-up-unattendish`'s Services table** | A `Tracker:` line of wakewire's own | The skill names the tracker and the forge in a table with fixed values; a line of wakewire's own would be a second copy that drifts. The line is read where there is no table |
| **No `glab` token source** | Borrowing `glab`'s login | `glab` 1.114.0 has no command that prints a token, and keeps it in the OS keyring by default |
| **The marker is run-fleet's existing closing line, configurable** | A marker of wakewire's own | run-fleet's templates already end every comment on `<kbd>🤖 unattendish agent</kbd>` and its review rounds rely on it; a second marker would change run-fleet for nothing |
| **A fixed tick, poll budget, lease, dormancy, retention and wake length** | Making them configurable | Nothing yet calls for changing them; the poll interval alone is configurable, in `[poll]` and per watch with `wakewire interval` |

## What a wake carries

| Decision | Chosen over | Why |
|---|---|---|
| **Facts in the wake, content in the batch** | Comment text in the wake | A wake enters as its user's turn; content belongs in command output |
| **The batch is read with `wakewire changes <batch>` in every harness** | The mod's own MCP tool; a file the agent reads | One reader everywhere, and the way to a whole text is a command the agent runs. The tool's one gain, running with no permission prompt in manual mode, the mod keeps by answering the permission ask for that command. A file read with `Read` asks permission for a path outside the project |
| **The mod allows the exact `wakewire changes` command when Claude Code would ask** | An allow rule for `Bash(wakewire changes *)` that each person adds to their settings | The plugin brings the permission, so no install step is left to forget, and an unattended session never waits on a prompt for a wake's batch. It allows only the command as a wake names it, a batch, a change and wakewire's own flags with nothing chained, and only on an `ask`, so a rule that denies the command still denies it |
| **The batch list carries no text; one change carries its text, cut at 1000 characters, whole with `--full`** | A 1000-character preview of every text in the list | The list keeps a few fields per change, as AXI's minimal schemas ask: `n`, `item`, `kind`, `by` and `url`, and the url opens the change on its service without another call. A preview of a long text, such as a spec edit's diff or a long review, neither answers the agent nor spares it the read, so a batch of several costs thousands of characters and every one is read again. The agents' own comments never reach a batch, since they end with the marker, so no preview is needed to recognise them. `--json` carries every text whole, for programs |
| **Texts are stored whole** | Cutting them at storage | A text that was not stored cannot be read later, and a stored one costs no tokens until it is asked for. The services' own caps and the 30-day prune bound `state.db` |
| **wakewire's own change format** | The services' JSON | One GitHub comment object carries about 40 fields, mostly URLs; Jira bodies are Atlassian Document Format; three shapes for one thing |
| **Ignore the in-progress and needs-input labels entirely** | Ignoring by actor | When agents act as the identity, a label change cannot be told apart by who made it, and only agents set those two |
| **Pull requests by author** | A run-fleet label on pull requests | The agents open pull requests as the identity, so authorship already marks them |
| **Spec edits wake, held until stable** | Ignoring edits | A spec edited mid-build is a steer; a burst of saves becomes one change |
| **No CI checks** | Waking on checks | They are frequent, and the agent reads them when it reads a pull request |
| **Comment edits and deletions ignored** | Waking on them | Rare, and none of the services records them reliably |

## Proven by running it

Claims the design rests on, with what a run showed. The Claude Code claims were run in interactive Claude Code 2.1.302
sessions on Linux, first with a spike mod and then with the wakewire mod
loaded by `claude --plugin-dir`; the service claims against the sandboxes.

| Claim | Found |
|---|---|
| A wake submitted by a mod enters an idle session and starts a turn | It does. `$.prompt.submit` from a `$.clock.after` timer resolved within 50 ms with `origin: { kind: 'plugin' }`, and the model began its turn about 2 s later with no keystroke. The model reads it framed as "The wwspike plugin sent a message:", followed by the text and a line saying a plugin's prompt starts the turn in the user's place |
| The binary takes the session's process id from its parent when the mod runs it | It can. A child started by `$.process.run` has the `claude` process as its parent, the same process `CLAUDE_PID` names in a Bash command |
| `/clear` is seen by the mod | `session.end` fires with `reason: 'clear'` and the old session id, and later Bash commands see the new `CLAUDE_CODE_SESSION_ID` while `CLAUDE_PID` stays the same |
| `adapter next` every 30 s is unnoticeable in the session | It is. Over two sessions of about fifteen minutes the ticks added no transcript row and no prompt; the status line alone changed |
| Plain pushes to a pull request change its head in the list responses on time | On GitHub, yes: the pull requests list showed the pushed head on the next poll. On GitLab 17.11, no: the merge requests list kept a merge request's old `sha` and comment count for over ten minutes after a push or a comment, so the GitLab source reads each open merge request alone on every poll; see [sources](../reference/sources.md#gitlab) |
| Jira's `comment` field reports a total on `search/jql` | It does: `comment.total` comes with every issue, `maxResults` 0, so comments are read only when the total moved |
| GitLab lists answer conditional requests | They do: weak ETags that change with the body, and `304` to their own tag |
| GitLab's `updated_at` moves for every change a poll must see | It does not: a comment, a push and an approval left it where it was every time they were tried, and a label change moved it once in four, so lists are not filtered by it and open merge requests are read alone |
| A Jira issue created with labels records them in its changelog | It does not, so a spec new to the watch with no trace of how it came in is reported as the spec label added |
| How Jira writes a label change in the changelog | As one item whose `fromString` and `toString` join the labels with spaces |
| GitHub's list responses change their ETag when only a label or the body changes | They do: a label-only change and a body-only edit each gave a `200` with a new ETag on the next poll, and idle polls answered `304`, so conditional requests stay |
| The mod's permission answer lets a wake's `wakewire changes <batch>` run with no prompt in manual permission mode | It does, checked on 2026-10-18 against Claude Code 2.1.302 with `claude -p … --permission-mode manual --setting-sources project` from an empty folder: without the mod, `wakewire changes 1` was refused for approval; with `--plugin-dir mod` it ran and printed the binary's `error: no batch 1`. `wakewire changes 1; echo chained` was still refused with the mod loaded |

## Not proven until it runs

Each of these is a claim about a harness or a service that only a run
settles, with what changes if it fails:

| Claim | If false |
|---|---|
| Who made a plain push can be named | Report the commits' authors instead, as wakewire does |
| Mods load inside bb's Claude Code sessions | The bb adapter is a bb plugin, as planned anyway |
