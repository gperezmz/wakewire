# State

What `~/.wakewire/state.db` holds, the rules that decide who holds a watch,
and how the database is kept small. It is SQLite in WAL mode with a busy
timeout, so several sessions' binaries can read and write it at once.

## Tables

| Table | One row per | Holds |
|---|---|---|
| `watches` | Repository and identity | Tracker, labels as registered, interval, `paused_until` |
| `registrations` | Watch | Harness, session id, process id and start time, `heard_at`, `ended_at` and its reason |
| `adapters` | Session with a loaded adapter | Session id, process id, `hello_at`; what `register` reads to report `adapter: loaded` |
| `snapshots` | Watched item | The diffed fields, the ids of comments, reviews and events already recorded, `held_edit` while an edit settles |
| `changes` | Change found | Item, kind, by, at, the batch fields, and its status: `pending`, `held`, `delivered` or `ignored` with the reason. This table is the ledger |
| `batches` | Batch | Watch, the changes in it, `offered_at`, `acked_at` |
| `problems` | Problem | Watch, source, code, `since`, `delivered_at`, `cleared_at` |
| `leases` | Watch being polled | Process id and start time, `expires_at` |
| `meta` | — | Schema version, `pruned_at` |

A poll writes a source's changes and its snapshots in one transaction, so a
change is stored exactly when its snapshot moves past it.

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

## Leases

`adapter next` takes the watch's lease before polling and releases it after.
A lease is free when its expiry, 90 s after it was taken, has passed, or when
its process is gone by rule 1. A second `next` that finds the lease taken
returns at once, delivering any pending batch without polling. No database
lock is held across a poll's requests, so one slow service never blocks other
watches.

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
