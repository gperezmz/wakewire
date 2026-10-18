# How wakewire works

wakewire is two pieces: a command-line binary that knows the services and
keeps the state, and an adapter inside the harness that keeps time and wakes
the session. Neither runs in the background on its own: the session is the
clock, and when no session holds a watch, nothing runs at all.

```mermaid
flowchart LR
  subgraph Session["Claude Code session"]
    Agent["Agent"]
    Mod["wakewire mod<br/>(adapter)"]
  end
  subgraph Binary["wakewire binary (runs, then exits)"]
    CLI["CLI"]
    Core["Core: watches, polls,<br/>snapshots, changes"]
    Sources["Sources:<br/>GitHub · GitLab · Jira"]
  end
  State[("~/.wakewire/state.db")]
  Services["GitHub / GitLab / Jira"]

  Agent -- "wakewire register" --> CLI
  Mod -- "every 30 s: wakewire adapter next" --> CLI
  Mod -- "$.prompt.submit(wake)" --> Agent
  Agent -- "wakewire changes" --> CLI
  CLI --> Core
  Core --> State
  Core --> Sources
  Sources -- "read only" --> Services
```

## The flow of one change

```mermaid
sequenceDiagram
  participant You as Reviewer on GitHub
  participant Mod as wakewire mod
  participant Bin as wakewire binary
  participant DB as state.db
  participant Agent as Agent in the session

  You->>You: submits a review on PR #41
  Mod->>Bin: adapter next (tick)
  Bin->>Bin: poll due? yes
  Bin->>DB: take the watch's lease
  Bin->>You: list the partition and PRs (read)
  Bin->>DB: diff against snapshots, store change, update snapshot (one transaction)
  Bin->>DB: release the lease
  Bin-->>Mod: batch 193: "- PR #41: review, changes requested, by alice"
  Mod->>Agent: $.prompt.submit(wake)
  Note over Mod,Agent: the wake's turn starts
  Mod->>Bin: adapter ack 193
  Bin->>DB: mark batch 193 delivered
  Agent->>Bin: wakewire changes 193
  Bin-->>Agent: the batch's changes, one row each, no text
  Agent->>Bin: wakewire changes 193 1
  Bin-->>Agent: the review, with what alice wrote, as command output
```

## Why the session is the clock

A daemon would have to be installed, started at login, kept alive and told
which sessions exist. wakewire has none of that: the adapter's 30-second tick
runs `wakewire adapter next`, which polls only when the watch's interval has
passed, and returns. A watch nobody holds is not polled, and that costs
nothing, because a change has nowhere to go until a session holds the watch
again, and the snapshots make the first poll then find everything that moved
in between.

The binary owning the interval, rather than the adapter, means
`wakewire interval` takes effect on the next tick without reloading the
adapter, and an undelivered batch is offered again within 30 seconds.

## Why nothing is lost

A change is stored in the same transaction that updates the snapshot it was
diffed against. From then on it exists only in `state.db`, never only in
memory, and its delivery is tracked separately:

| A change is | Until |
|---|---|
| **pending** | it goes out in a batch the adapter acknowledges |
| **held** | the watch is resumed, or its `pause --for` runs out; then it is pending |
| **delivered** | pruned with the ledger after 30 days |
| **ignored** | pruned with the ledger; the reason is kept with it |

A batch the adapter never acknowledges, because the session quit between the
wake and the acknowledgement, leaves its changes pending, so the next holder
gets them again. **A change may be delivered twice and is never lost**,
short of the watch itself being forgotten by `unregister` or pruned after 30
days dormant. A repeat is harmless: the session reads the item and finds the
change already handled.

A process killed in the middle of a poll loses only the transaction it was
in, which leaves no trace; the next poll diffs the same items again.

## Why it diffs contents, not timestamps

None of the three services documents which edits move an item's
`updated_at`. A GitLab comment or push does not, and a label change may not;
a GitHub push may not; Jira's search index can lag by minutes. A poll therefore reads each item's current
labels, state, assignees, title, description and head commit, and compares
them with its snapshot; a timestamp only decides whether to also fetch the
item's comments and reviews. Per source, what a poll reads and costs is in
[sources](../reference/sources.md).

## Who holds a watch

One session at a time holds a watch. Whether a registration still holds is
worked out each time someone asks, from facts a crash cannot leave stale,
never from a flag someone has to clear:

```mermaid
flowchart TD
  A["Did its adapter report<br/>the session ended?"] -- yes --> F["Dormant: take over"]
  A -- no --> B["Is the holder's process running,<br/>with the same start time?"]
  B -- no --> F
  B -- yes --> C["Is the registering process the same,<br/>under a newer session id? (/clear)"]
  C -- yes --> F
  C -- no --> D["Heard from its adapter<br/>within 3 intervals?"]
  D -- yes --> H["Held: refuse, unless --take-over"]
  D -- no --> F
```

The start time matters because an operating system reuses process ids: a
matching id with another start time is another program. The full rules are in
[state](../reference/state.md).

## What crosses into the session, and how

A wake enters the session as a turn in its user's own words: the mod
submits it as the person's, without the frame Claude Code would put around a
plugin's prompt. So **a wake carries facts only**: what changed, on which
item, by which login. It never carries anything a person wrote, not even a
title, since anyone who can comment on the repository could otherwise write
into the session in its user's voice. Its only other words are wakewire's
own: a first line that says what it is, and a last line that asks for the
batch to be read before anything is done about it. A wake that reports only
problems names `wakewire status` instead.

What people wrote reaches the agent only when it runs
`wakewire changes <batch> <n>` itself, in every harness:
`wakewire changes <batch>` lists the batch's changes without their texts, and
`<n>` shows one change with what its person wrote, each text cut at 1000
characters unless `--full` is given. It then arrives as
command output, which the model weighs as material rather than obeys as
instruction.

## Built for more than one harness

The binary never wakes anyone. It answers `adapter next` with what changed and
leaves delivery to the adapter, and it works out whether a registration holds
the same way for every harness. That keeps the harness-specific part small: a
clock, a way to deliver, and a report when the session ends. For Claude Code
these are the mod's 30-second tick, `$.prompt.submit` and `session.end`;
`claude-code` is the only harness `register` accepts, and the
[Claude Code mod](../reference/claude-code-mod.md) the only adapter.

## Why wakewire is not the fleet

wakewire knows nothing of fleets, manifests, builders or grants: it watches
an identity's work and wakes whoever holds the watch. What run-fleet adds
(the manifest, a board of what waits on the operator) is a separate project,
which can read wakewire through its documented outputs: `status --json`,
`ledger --json` and the [wake contract](../reference/wake-contract.md). The
mod publishes nothing for other mods; the
[design decisions](design-decisions.md#the-mod) say why.
