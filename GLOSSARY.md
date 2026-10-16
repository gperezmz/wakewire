# wakewire

A command-line bridge that wakes an agent session when something it waits on
changes on GitHub, GitLab or Jira, and the words it is designed in.

## Language

### Watching

**Watch**:
What wakewire keeps an eye on for one identity in one repository: the
operator's partition on the tracker and the operator's pull requests on the
forge. A watch is keyed by repository and identity, so two people on one
repository have two watches.
_Avoid_: Fleet (in wakewire's own code and docs), subscription

**Identity**:
The account a watch reads as and the person whose work it watches, on each
service: a login on GitHub or GitLab, an account ID on Jira. It defaults to
whoever the token belongs to.
_Avoid_: Operator (in wakewire's own code and docs), user

**Partition**:
The items a watch treats as the identity's own: items assigned to the
identity, and items the identity filed that nobody is assigned to.
_Avoid_: My issues, scope

**Forge**:
The service hosting the repository and its pull requests: GitHub or GitLab.
_Avoid_: Code host, SCM

**Tracker**:
The service holding the specs: the forge's own issues, or a Jira project.
_Avoid_: Issue tracker, backlog

**Source template**:
The setting that says where in a repository wakewire reads a watch's tracker
and labels, by file, section and table columns, so a team's own layout is read
without a per-repository file.
_Avoid_: Parser, project config

**Account**:
One entry in the configuration that pairs a host, the owners it covers and a
token source with an optional identity override. A watch reads each service
through the account that matches the repository's host and owner.
_Avoid_: Credential, login, profile

### Noticing

**Poll**:
One pass over a watch's sources that compares what each service shows now
with the snapshots and records every difference as a change.
_Avoid_: Fetch, sync, refresh

**Tick**:
The harness adapter's timer firing, every 30 seconds, to ask wakewire whether
a poll is due and whether anything is waiting to be delivered. Most ticks run
no poll.
_Avoid_: Heartbeat (a tick carries one, but is not one)

**Snapshot**:
What wakewire last saw of one watched item: its state, labels, assignees,
title, description, head commit, and the ids of the comments, reviews and
events already recorded. A poll diffs against it.
_Avoid_: Cursor, checkpoint, since-timestamp

**Change**:
One difference a poll found on one item, such as a label added or a comment
written, with who made it and when. Every change is stored the moment it is
found, whether it will wake anyone or is ignored, and the stored changes are
the ledger.
_Avoid_: Event (for wakewire's own record), notification, update

**Echo**:
A change the watched session's own side made, which must never wake it: a
comment ending with the marker, or any change to the in-progress or
needs-input label.
_Avoid_: Self-event, own action

**Marker**:
The fixed line that ends every comment an agent writes on a watched item, by
which wakewire knows the comment is an echo.
_Avoid_: Signature, footer, suffix

**Ledger**:
Every change wakewire has stored, with what it did with each: delivered in a
wake, ignored with the reason, or held while paused. It is how anyone answers
why a wake came or did not.
_Avoid_: Log, history, audit trail

**Problem**:
A condition that stops a watch from seeing its sources, such as a refused
token or a service that has not answered for three polls. A problem is
delivered once, as a change of its own, and shown in status until it clears.
_Avoid_: Error (for this), alert

### Waking

**Wake**:
The message that tells a session what changed: one line per change, facts
only, never anything a person wrote. It names the batch that holds the rest.
_Avoid_: Notification, ping, nudge

**Batch**:
The changes one wake reports, under one id. A batch stays undelivered until
the adapter acknowledges it; an unacknowledged batch is offered again.
_Avoid_: Bundle, payload, digest

**Harness**:
The program the agent session runs in: Claude Code now, bb later.
_Avoid_: Host, runtime, client

**Adapter**:
The part of wakewire that lives in one harness: it runs the ticks, delivers
wakes into the session and reports when the session ends. For Claude Code it
is a mod.
_Avoid_: Plugin (for this role), driver, connector

**Mod**:
A Claude Code plugin whose behaviour is a TypeScript hooks module running
inside the session process. wakewire's Claude Code adapter is one.
_Avoid_: Extension

### Holding

**Registration**:
The record that one session holds a watch: the harness, the session id, the
process id and its start time, and when its adapter was last heard from.
_Avoid_: Subscription, claim

**Holder**:
The session whose registration a watch is under. A watch has at most one
holder at a time.
_Avoid_: Owner, manager (in wakewire's own code and docs)

**Dormant**:
Said of a registration whose session ended, whose process is gone, or whose
adapter has not been heard from for three intervals. A dormant registration
keeps its watch's snapshots and undelivered changes, and any session of the
same identity may take the watch over.
_Avoid_: Stale, dead, expired

**Takeover**:
A new session registering for a watch whose registration is dormant, or with
`--take-over`, and so becoming its holder and inheriting everything waiting.
_Avoid_: Steal, handover

**Lease**:
A short claim, with an expiry, that one process is polling a watch now, so a
second process does not poll it at the same time. A lease whose process is
gone or whose expiry passed is free.
_Avoid_: Lock (for this), mutex

**Pause**:
A watch's state in which polls still run and store changes but no wake is
delivered; resuming delivers everything held as one wake.
_Avoid_: Mute, snooze, suspend

## Flagged ambiguities

- "Fleet", "operator" and "manager" are run-fleet's words. wakewire watches
  for an identity on behalf of whichever session holds the watch, and its own
  code and docs use watch, identity and holder; the docs say fleet only where
  they speak of run-fleet itself.
- "Event" is what the services' APIs call their timeline entries. What
  wakewire records from them is a change.
