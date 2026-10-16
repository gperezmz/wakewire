# State

What `~/.wakewire/state.db` holds, the rules that decide who holds a watch,
and how the database is kept small. It is SQLite in WAL mode with a busy
timeout, so several sessions' binaries can read and write it at once.

## Tables

| Table | One row per | Holds |
|---|---|---|
| `watches` | Repository and identity | Tracker, labels as registered, interval, `paused_until` |
| `registrations` | Watch | Harness, session id, process id and start time, `heard_at`, `ended_at` and its reason |
| `adapters` | Session with a loaded adapter | Session id, process id and start time, `hello_at`, `heard_at`, the protocol it speaks; what `register` reads to report `adapter: loaded` and `doctor` to compare protocols |
| `snapshots` | Watched item | The diffed fields, the ids of comments, reviews and events already recorded, `held_edit` while an edit settles |
| `changes` | Change found | Item, kind, by, at, the batch fields, and its status: `pending`, `held`, `delivered` or `ignored` with the reason. This table is the ledger |
| `batches` | Batch | Watch, `offered_at`, `acked_at`; its changes are in `batch_changes` |
| `batch_changes` | Change in a batch | Batch and change, so a batch keeps its changes after another takes its place |
| `problems` | Problem | Watch, source, code, text, the fix, `since`, `delivered_at`, `cleared_at` |
| `source_state` | Watch and source | The source's own state between polls (ETags, the items each list held, pull request heads), unreachable polls in a row, the rate-limit budget, `skip_until` |
| `leases` | Watch being polled | The polling binary's process id and start time, `expires_at` |
| `meta` | — | Schema version, `pruned_at` |

A poll writes a source's changes and its snapshots in one transaction, so a
change is stored exactly when its snapshot moves past it. A test kills a real
process with SIGKILL in the middle of a poll, once while it waits on the
service and once inside that transaction before it commits, and shows the
next poll still finds the change.

The binary migrates `state.db` when it opens it; a database newer than the
binary is refused, naming the fix: update wakewire.

## Batches

At most one batch per watch is offered at a time. A batch the adapter has
not acknowledged is offered again as it is on the next tick. When new changes
arrive before it is acknowledged, a new batch holding everything pending
takes its place, so one wake says it all; the old batch keeps its changes and
can still be read with `wakewire changes`.

## Liveness

A registration **holds** its watch when all of these are true, checked each
time a `register`, `adapter next` or `status` needs to know:

1. The process with its process id is running, with the recorded start time.
   On Linux the start time is field 22 of `/proc/<pid>/stat`; on macOS,
   `kp_proc.p_starttime` from `sysctl kern.proc.pid.<pid>`.
2. No `adapter ended` was recorded for its session.
3. The registering process is not the same process under a newer session id,
   which is what `/clear` leaves.
4. Its adapter was heard from within 3 intervals.

Otherwise it is **dormant**. A `register` by the same identity then takes the
watch over; `register --take-over` does so whatever the answer.

| How the session ended | Known by | Dormant |
|---|---|---|
| `/exit`, Ctrl-C, Ctrl-D, SIGINT, SIGTERM, SIGHUP, `/clear` | `adapter ended` | At once |
| `kill -9`, a crash, a reboot | Rule 1 | At the next check |
| The mod uninstalled or disabled, process alive | Rule 4 | After 3 intervals |

A session that comes back under the same id, as `claude --resume` brings it
in a new process, takes its registration back at the adapter's `hello`:
the registration takes the new process and is held again. It does so only
where the session or its process went away: the session ended with the
reason `prompt_input_exit`, `other`, `logout` or `resume`, or did not end and
its process is gone. It does not where another session took the watch over
meanwhile, where the session was cleared, or where its registration was ended
because the session registered another watch.

## Leases

`adapter next` takes the watch's lease before polling and releases it after.
The lease is held by the binary's own process, not the session's, so two
ticks of one session do not poll together. A lease is free when its expiry,
90 s after it was taken, has passed, or when its process is gone by rule 1. A second `next` that finds the lease taken
returns at once, delivering any pending batch without polling. No database
lock is held across a request, neither a poll's nor the probe `status` makes
of each account, so one slow service never blocks other watches.

## Pruning

At most once a day, the first `adapter next` or `register` after
`meta.pruned_at` is a day old removes:

| What | When |
|---|---|
| Delivered and ignored changes | 30 days after they were found |
| Acknowledged batches | With their changes |
| Dormant registrations, with their watch's snapshots and pending changes | 30 days dormant |
| Cleared problems | 30 days after clearing |

Then `PRAGMA incremental_vacuum` returns the freed pages. `wakewire prune`
runs the same now, and `--dry-run` lists what it would remove.
