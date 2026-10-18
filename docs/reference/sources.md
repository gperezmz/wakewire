# Sources

What one poll reads from each service, what it costs, and what each service
cannot tell wakewire. The approach is the same for all three: list the
watched sets on one page (Jira: up to 5), ordered by most recently updated; diff each item's
fields against its snapshot; fetch an item's comments, reviews and events only
when its fields or its update time moved. Why contents rather than timestamps:
[how it works](../explanation/how-wakewire-works.md#why-it-diffs-contents-not-timestamps).

Each service's last section, Limits, says what the service leaves unsaid and
how wakewire reads around it. Every request has 15 s and a poll 60 s, an
answer is read up to 32 MiB, and wakewire sends only `GET` and `HEAD`, and
Jira's two read-only `POST`s below.

## GitHub

### Each poll

| # | Request | Why |
|---|---|---|
| 1 | `GET /repos/{owner}/{repo}/issues?state=all&assignee=<login>&sort=updated&direction=desc&per_page=100` | Items assigned to the identity; includes pull requests |
| 2 | `GET /repos/{owner}/{repo}/issues?state=all&creator=<login>&sort=updated&direction=desc&per_page=100` | Items the identity filed, which covers its pull requests and its unassigned specs |
| 3 | `GET /repos/{owner}/{repo}/pulls?state=open&sort=updated&direction=desc&per_page=100` | Open pull requests' head commits |

Request 3 is sent first, so the issue lists can carry each pull request's
head. Every request carries `X-GitHub-Api-Version: 2022-11-28`. Each carries `If-None-Match` with the last ETag. A `304` does not count
against the rate limit, so an idle poll costs nothing. The URLs never change
between polls, which is what lets the ETag match. The ETag moves for any
change to a listed item, a label or the body alone included, and the pull
requests list shows a pushed head on the next poll.

From the lists, wakewire keeps to items with the spec label (from requests 1
and 2, where `assignee` is the identity or nobody) and to pull requests
authored by the identity, and diffs each against its snapshot: `state`,
`labels`, `assignees`, `title`, `body`, `comments` (the count),
`pull_request.merged_at`, `updated_at`, and the head commit from request 3. A
watched item missing from every list is read alone, once, with
`GET /repos/{o}/{r}/issues/{n}`, to see why. A
list answered with `304` keeps the items it held last time, so an item is
missing only when no list holds it. Where the tracker is Jira, request 1 is
not sent.

### Per item that moved

| Request | Gives |
|---|---|
| `GET /repos/{o}/{r}/issues/{n}/timeline?per_page=100` | Who added or removed a label, assigned, closed, reopened, renamed, merged; conversation comments with their bodies (`commented`); reviews with `state`, body and `submitted_at` (`reviewed`) |
| `GET /repos/{o}/{r}/pulls/{n}/comments?per_page=100` | Inline comments, with `path`, `line` (or `original_line`) and `diff_hunk` |
| `GET /repos/{o}/{r}/pulls/{n}/commits?per_page=100` | When the head moved: the commits after the head last seen, each with its author's login, or the commit's author email where GitHub links no account. After a force push the head last seen is gone, and every commit listed counts as new |

The timeline carries conversation comments and reviews, so the separate
comments and reviews requests are not needed. Each list is read up to 10
pages, the commit list up to 3 (300 commits, more than a head can move by).
A list longer than that is read from its end, since these lists run oldest
first: page 1 shows from the `last` link how many pages there are, and the
last 10 (or 3) are read, so that a busy item's new activity is never past the
cap; one extra request for such an item. About 1 request per issue and 2 or 3 per pull request
that moved; on the watch's first poll none, since what is there then is no
change. Entries already recorded are skipped by their ids. A review of state
`commented` with no body only carries inline comments, which are read on
their own, so it is skipped.

### Identity, limits

- Identity: `GET /user` gives `login`, or `GET /users/{user}` where the
  account sets `user`; changes are matched on the login.
- 5,000 requests an hour per token; `x-ratelimit-limit`,
  `x-ratelimit-remaining` and `x-ratelimit-reset` are read on every response.
- Not used: the repository events API, which "is not built to serve real-time
  use cases" (latency from 30 s to 6 h); the search API, at 30 requests a
  minute.

### Limits

- GitHub does not document which edits move an issue's `updated_at`, so the
  fields are diffed, and comments are read when the `comments` count or
  `updated_at` moves.
- No REST endpoint names who pushed. `pushed` reports the new commits'
  authors, and wakes only when one is not the identity.
- No timeline event names who edited an issue's body, so a description
  `edited` on GitHub has no `by`.

## GitLab

### Each poll

| # | Request | Why |
|---|---|---|
| 0 | `GET /users?username=<identity>` | The numeric id the list filters take; asked once, then kept in the source's state |
| 1 | `GET /projects/:id/issues?assignee_id=<id>&state=all&order_by=updated_at&sort=desc&per_page=100` | Issues assigned to the identity |
| 2 | `GET /projects/:id/issues?author_id=<id>&assignee_id=None&state=all&order_by=updated_at&sort=desc&per_page=100` | Issues the identity filed that nobody is assigned to |
| 3 | `GET /projects/:id/merge_requests?author_id=<id>&state=all&order_by=updated_at&sort=desc&per_page=100` | The identity's merge requests |
| 4 | `GET /projects/:id/merge_requests/:iid` for each open one, in the lists or in a snapshot | Its current fields, which replace the list's entry |
| 5 | `GET /projects/:id/merge_requests/:iid/approvals` for each open one, except on the watch's first poll | Who approved |

Paths are under `https://<host>/api/v4`. `:id` is the project's path with its slashes escaped, `group%2Fproject`. With
a Jira tracker, requests 1 and 2 are left out.

Requests 1 to 3 carry `If-None-Match` with the last ETag. GitLab sends a weak
ETag on them, it changes when the list's body does, and it answers `304` to
its own tag. Requests 4 and 5 carry none, since each is a single read.

Request 4 is needed because the merge request list keeps each entry until
its `updated_at` moves, and a push, a comment or an approval does not move
it, so the list's `sha` and `user_notes_count` lag while a read of the merge
request alone is current. Every field of an open merge request is taken from
that read. Request 5 is needed because no field of a merge request shows an approval.

No `updated_after` filter: a comment, a push or an approval does not move
`updated_at`, and a label change does not always, so filtering by it would
hide them. Diffed
fields: `state` (`opened` is open; `closed`, `locked` and `merged` are
closed), `labels`, `assignees`, `title`, `description`, `user_notes_count`,
`sha`, `merged_at`, `updated_at`. An idle poll costs 3 requests (1 with a Jira
tracker) plus 2 for each open merge request, against 2,000 a minute on
gitlab.com; the watch's first poll adds request 0.

### Per item that moved

| Request | Gives |
|---|---|
| `GET .../issues/:iid/resource_label_events` (or `merge_requests`) | Who added or removed a label, and when |
| `GET .../resource_state_events` | Who closed, reopened or merged |
| `GET .../notes?order_by=updated_at&sort=desc&per_page=100` | Comments, and for a merge request the inline ones, which carry their `position`; system notes say who edited or reassigned and when an approval happened |
| `GET .../merge_requests/:iid/commits` | New commits and their authors, newest first, when `sha` moved |
| `GET /user` | The token owner's emails, to read the identity's own commits as the identity |

Lists follow `Link: rel="next"` from their first page, up to 10 pages of
100, the commits up to 3. Activities are told apart by id: `le:`
label events, `se:` state events, `n:` notes, `ap:<username>` an approval,
`commit:<sha>`.

### Identity, limits

- Identity: a username, from `GET /user`, or `GET /users?username=<user>`
  where the account sets `user`. The lists filter on the numeric `id`; items
  are compared on `username`.
- A `read_api` token is enough.
- gitlab.com allows 2,000 authenticated requests a minute; self-managed
  defaults to 7,200 an hour when an administrator enables limits. Headers:
  `RateLimit-Limit`, `RateLimit-Remaining`, and `RateLimit-Reset` or
  `RateLimit-ResetTime` depending on the instance; all are read. A
  self-managed instance with limits off sends none, and the rate stays
  unknown.

### Limits

- The approvals list names the approvers, and not always when. The time is
  the list's `approved_at` where the instance gives it, else the system note
  "approved this merge request", else the time of the poll. An approval
  withdrawn and given again is not told from the first, since it keeps the id
  `ap:<username>`.
- A diff hunk for an inline comment: only the position. `hunk` is left out.
- Who made a push, short of the day-granular events API. `pushed` reports
  the commits' authors; a commit carries an email, which is read as the
  identity when it is one of the emails `GET /user` gives for the token's
  owner and the token's owner is the identity.
- A merge request closed or merged is read from the list's entry alone, so a
  comment on it afterwards is missed until its `updated_at` moves.

## Jira Cloud

### Each poll

One request:

```text
POST /rest/api/3/search/jql
jql:    (project = "<KEY>" AND labels = "<spec label>"
         AND (assignee = "<account ID>" OR (reporter = "<account ID>" AND assignee is EMPTY)))
        OR issuekey in (<every watched issue>)
        ORDER BY updated DESC
fields: summary, description, labels, assignee, reporter, status, resolution, comment, updated
maxResults: 100
reconcileIssues: <watched issue ids, at most 50>
```

The `issuekey in (…)` clause is left out while nothing is watched yet. It keeps issues that left the partition in view
until that is recorded. `reconcileIssues` asks for read-after-write
consistency, since the search index can lag by seconds to minutes; it takes
issue ids as numbers, at most 50, the same on every page, which wakewire
keeps from earlier searches. A search that has a `nextPageToken` is followed
for up to 5 pages. Diffed fields: `labels`, `assignee`, `status` (open, or
closed when its category is Done, whatever its `resolution`), `summary`,
`description`, the comment total, `updated`. No time filter: JQL dates have minute precision and a time
zone that depends on settings, and comparing each issue's `updated` with its
snapshot avoids both.

The JQL names the identity by account ID, always, and never `currentUser()`:
the identity is the token's owner unless the account sets `user`, and the
watch keeps the account ID either way. The search requires a bounded query;
the `project` restriction bounds it.

A watched issue that a search run to its end no longer returns, which means
it was deleted or the account can no longer see it, is kept as it was, with
no change reported, and is left out of later searches.

### Per issue that moved

| Request | Gives |
|---|---|
| `POST /rest/api/3/changelog/bulkfetch` for every issue that moved, no `fieldIds` filter | Who changed labels, assignee, status and text, oldest first. On failure, `GET /rest/api/3/issue/{key}/changelog` per issue |
| `GET /rest/api/3/issue/{key}/comment?orderBy=created` | Comments, with `author`; bodies are Atlassian Document Format, converted to Markdown. Read only for an issue new to the watch, whose comment total moved, or whose search gave no total |

Each paged answer is read up to 5 pages of 100. Comments and an issue's own
changelog are asked for from the start only to learn their total; one longer
than 500 is then read from its last 500, so a busy issue's new activity is
never past the cap. The bulk changelog pages by token and cannot be asked for
its end: one longer than 5 pages is read issue by issue instead.

An idle poll is 1 request. One where issues moved adds the bulk changelog,
and a comment read for each issue whose comment total moved, more where they
page or the changelog is read issue by issue.

The bulk request is the one Atlassian marks experimental; it takes at most
1,000 issues and 10 field ids, which is why wakewire sends none. A
changelog item names a status but not its category, so wakewire learns each
status's category, by id and by name, from the issues the search returns, and
reads a status change into Done as `closed`, whatever it came from, and out
of Done into a status it has seen as `reopened`. A change out of Done to a
status it has not seen is left to the field diff.

### Identity, limits

- Authentication: Basic, as `email:API token`.
- Identity: `GET /rest/api/3/myself` gives `accountId`, or the account's
  `user`, taken as an account ID as given. Jira identifies people by account
  ID, and JQL accepts no username or email.
- API-token traffic stays under Jira's burst limits, whose figures Atlassian
  does not publish. A `429` is retried only after its `Retry-After`;
  `X-RateLimit-NearLimit: true`, which Jira sends when less than 20 % of its
  capacity remains, is shown in status.

### Limits

- Jira does not document which edits move `updated`, so the fields are
  diffed, and comments are read when the `comment` field's `total` moves.
- Labels are compared without regard to case. A label change is one
  changelog item whose `fromString` and `toString` join the labels with
  spaces.
- A status change's `from` and `to` are looked up by id and by name.
- Labels an issue is created with are in no changelog. A spec new to the
  watch with nothing in its timeline that shows how it came in is reported
  as the spec label added, with no `by`.

## Problems

A source becomes a problem, delivered once, when:

| Problem | `code` |
|---|---|
| The service answers 401, or 403 that is not a rate limit; 404 or 410 for the repository; on Jira, a search answered with a status that is neither 2xx nor 5xx | `token_refused` |
| For 3 polls in a row, the service does not answer within 15 s, the poll's 60 s run out, it answers 5xx or another status wakewire does not expect, redirects to another host, or cuts its answer off; any other failure starts the count again | `unreachable` |
| The remaining budget is below 5 % of a limit the service states, the service answers 429, or 403 with `Retry-After` or no budget left | `rate_limited`; the source is skipped until its reset time, where the service gives one |
| No account covers the repository or the site, or its token cannot be had | `no_account`, `no_stored_token`, `no_token`; a missing Jira account is a problem on the tracker alone, and the forge polls as usual |

A problem clears on the first poll that succeeds; `status` shows when.
