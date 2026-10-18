# State

What `~/.wakewire/state.db` holds, the rules that decide who holds a watch,
and how the database is kept small. It is SQLite in WAL mode with a 10 s
busy timeout, so several sessions' binaries can read and write it at once,
created with mode 600.

## Tables

| Table | One row per | Holds |
|---|---|---|
| `watches` | Repository and identity | The forge account; the tracker with its identity and account; the labels and marker as registered; the interval; `paused_since`, `paused_until`, `polled_at` |
| `registrations` | Watch | Harness, session id, process id and start time, `registered_at`, `heard_at`, `ended_at` and its reason, and `replaced`, the session a takeover took the watch from, which `adapter next` answers as `taken-over` |
| `adapters` | Session with a loaded adapter | Session id, process id and start time, `hello_at`, `heard_at`, the protocol it speaks; what `register` reads to report `adapter: loaded` and `doctor` to compare protocols |
| `snapshots` | Watched item | As JSON: the diffed fields, the ids of comments, reviews and events already recorded, the time it was first seen from, and any edit held while it settles |
| `changes` | Change found | Source, item, kind, `at`, `found_at`, its status (`pending`, `held`, `delivered` or `ignored` with the reason), its batch, and the change in the [batch's format](wake-contract.md#the-batch). This table is the ledger |
| `batches` | Batch | Watch, `offered_at`, `acked_at`; its changes are in `batch_changes` |
| `batch_changes` | Change in a batch | Batch and change, so a batch keeps its changes after another takes its place |
| `problems` | Problem | Watch, source, code, text, the fix, `since`, `delivered_at`, `cleared_at` |
| `source_state` | Watch and source | The source's own state between polls (ETags, the items each list held, pull request heads), failed polls in a row, the rate-limit budget, `skip_until`, and `ok_at`, when the source last answered |
| `leases` | Watch being polled | The polling binary's process id and start time, `expires_at` |
| `meta` | — | Schema version, `pruned_at`, and the highest watch and batch id given out, so a deleted watch's id or a pruned batch's is never given again |

A poll writes a source's changes and its snapshots in one transaction, so a
change is stored exactly when its snapshot moves past it. A test kills a real
process with SIGKILL in the middle of a poll, once while it waits on the
service and once inside that transaction before it commits, and shows the
next poll still finds the change.

`state.db` is at schema version 4, kept in `meta`. The binary migrates an
older one when a command that is not a dry run opens it; a dry run, and so
the bare `wakewire`, migrates inside its transaction and rolls it back, so an
older `state.db` keeps its schema until then. The step from 3 to 4 removes
`"truncated": false` from the stored changes and keeps `"truncated": true`
on the bodies wakewire 1.0.3 cut at 4000 characters. A database newer than
the binary is refused as `state_newer`, naming the fix: install the release
that wrote it, or a later one. So wakewire 1.0.3 refuses a version 4
`state.db`, and going back to it means moving `state.db` aside.

## Batches

At most one batch per watch is offered at a time, and only while it has
pending changes; a paused watch is offered none. A batch the adapter has not
acknowledged is offered again as it is on the next tick. When new changes
arrive before it is acknowledged, a new batch holding everything pending
takes its place, so one wake says it all; the old batch keeps its changes and
can still be read with `wakewire changes`.

## Liveness

A registration **holds** its watch when none of these is true, checked in
this order each time a `register`, a `status` or the daily clean-up needs to
know; `status` names the first that is, as the dormant holder's `why`:

| Rule | Dormant when | `why` |
|---|---|---|
| 1 | An `adapter ended` was recorded for its session | `its session ended` |
| 2 | The process with its process id is not running with the recorded start time, or is a zombie. On Linux the start time is field 22 of `/proc/<pid>/stat`; on macOS, `kp_proc.p_starttime` from `sysctl kern.proc.pid.<pid>` | `its process is gone` |
| 3 | The registering process went on under a newer session id, which is what `/clear` leaves | `its session was cleared` |
| 4 | Its adapter has not been heard from for more than 3 intervals | `its adapter has not been heard from for 3 intervals` |

`adapter next` does not ask: it answers for the session whose registration
is the watch's current one and has not ended.

Otherwise it is **dormant**. A `register` by the same identity then takes the
watch over; `register --take-over` does so whatever the answer.

| How the session ended | Known by | Dormant |
|---|---|---|
| `/exit`, Ctrl-C, Ctrl-D, SIGINT, SIGTERM, SIGHUP, `/clear` | Rule 1, from `adapter ended` | At once |
| `kill -9`, a crash, a reboot | Rule 2 | At the next check |
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

`adapter next` takes the watch's lease before polling and releases it after,
and asks for it only when the watch's interval has passed. The poll gets at
most 60 s.
The lease is held by the binary's own process, not the session's, so two
ticks of one session do not poll together. A lease is free when its expiry,
90 s after it was taken, has passed, or when its process is gone, as rule 2
judges a process. A second `next` that finds the lease taken
returns at once, delivering any pending batch without polling. No database
lock is held across a request, neither a poll's nor the probe `status` makes
of each account, so one slow service never blocks other watches.

## Pruning

At most once a day, the first `register` or `adapter next` of a session
holding a watch after `meta.pruned_at` is a day old removes, with
`register --dry-run` running none of it:

| What | When |
|---|---|
| Delivered and ignored changes | 30 days after they were found |
| Acknowledged batches | 30 days after they were acknowledged |
| Cleared problems | 30 days after clearing |
| A watch whose registration is dormant, with its registration, snapshots, problems, source state and pending and held changes | 30 days after it was last heard from, or after its session ended |

A watch's delivered and ignored changes stay until they age out themselves.

Texts are stored whole, so this clean-up and the services' own caps on a
text, a GitHub comment at 65,536 characters, bound the size of `state.db`.

Then `PRAGMA incremental_vacuum` returns the freed pages. `wakewire prune`
runs the same clean-up at once, and `--dry-run` counts what it would remove
and records no clean-up.
