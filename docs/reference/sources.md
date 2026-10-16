# Sources

What one poll reads from each service, what it costs, and what each service
cannot tell wakewire. The approach is the same for all three: list the
watched sets on one page, ordered by most recently updated; diff each item's
fields against its snapshot; fetch an item's comments, reviews and events only
when its fields or its update time moved. Why contents rather than timestamps:
[how it works](../explanation/how-wakewire-works.md#why-it-diffs-contents-not-timestamps).

The endpoints were checked against each service's documentation on
2026-10-16; what the documentation does not settle is in the last section of
each.

## GitHub

### Each poll

| # | Request | Why |
|---|---|---|
| 1 | `GET /repos/{owner}/{repo}/issues?state=all&assignee=<login>&sort=updated&direction=desc&per_page=100` | Items assigned to the identity; includes pull requests |
| 2 | `GET /repos/{owner}/{repo}/issues?state=all&creator=<login>&sort=updated&direction=desc&per_page=100` | Items the identity filed, which covers its pull requests and its unassigned specs |
| 3 | `GET /repos/{owner}/{repo}/pulls?state=open&sort=updated&direction=desc&per_page=100` | Open pull requests' head commits |

Each carries `If-None-Match` with the last ETag. A `304` does not count
against the rate limit, so an idle poll costs nothing. The URLs never change
between polls, which is what lets the ETag match.

From the lists, wakewire keeps to items with the spec label (from requests 1
and 2, where `assignee` is the identity or nobody) and to pull requests
authored by the identity, and diffs each against its snapshot: `state`,
`labels`, `assignees`, `title`, `body`, `comments` (the count),
`pull_request.merged_at`, `updated_at`, and the head commit from request 3. A
watched item missing from every list is fetched alone, once, to see why.

### Per item that moved

| Request | Gives |
|---|---|
| `GET /repos/{o}/{r}/issues/{n}/timeline?per_page=100` | Who added or removed a label, assigned, closed, reopened, renamed, merged; reviews; commits |
| `GET /repos/{o}/{r}/issues/{n}/comments?since=<last seen>` | Conversation comments, with `user` and `created_at` |
| `GET /repos/{o}/{r}/pulls/{n}/comments?since=<last seen>` | Inline comments, with `path`, `line` and `diff_hunk` |
| `GET /repos/{o}/{r}/pulls/{n}/reviews` | Reviews, with `state` and `submitted_at` |

About 2 requests per issue and 4 per pull request that moved. Comments and
reviews already recorded are skipped by their ids.

### Identity, limits

- Identity: `GET /user` gives `login` and `id`; changes are matched on `id`.
- 5,000 requests an hour per token; `x-ratelimit-remaining` and
  `x-ratelimit-reset` are read on every response.
- Not used: the repository events API, which "is not built to serve real-time
  use cases" (latency from 30 s to 6 h); the search API, at 30 requests a
  minute.

### What GitHub does not say

- Which edits move an issue's `updated_at`. A review, a conversation comment
  and an inline comment were seen to move it; title and body edits and pushes
  were not tested. Diffing contents covers the fields; comments rely on the
  `comments` count and on `updated_at`.
- Who made a plain push: no REST endpoint names the pusher. `pushed` reports
  the new commits' authors, and wakes only when one is not the identity.

## GitLab

### Each poll

| # | Request | Why |
|---|---|---|
| 1 | `GET /projects/:id/issues?assignee_id=<id>&state=all&order_by=updated_at&sort=desc&per_page=100` | Issues assigned to the identity |
| 2 | `GET /projects/:id/issues?author_id=<id>&assignee_id=None&state=all&order_by=updated_at&sort=desc&per_page=100` | Issues the identity filed that nobody is assigned to |
| 3 | `GET /projects/:id/merge_requests?author_id=<id>&state=all&order_by=updated_at&sort=desc&per_page=100` | The identity's merge requests, with `sha`, `merged_at` and `user_notes_count` |

No `updated_after` filter: a label-only change may not move `updated_at`, so
filtering by it could hide the change. Diffed fields: `state`, `labels`,
`assignees`, `title`, `description`, `user_notes_count`, `sha`, `merged_at`,
`updated_at`. About 3 requests per idle poll, against 2,000 a minute on
gitlab.com.

### Per item that moved

| Request | Gives |
|---|---|
| `GET .../issues/:iid/resource_label_events` (or `merge_requests`) | Who added or removed a label, and when |
| `GET .../resource_state_events` | Who closed or reopened |
| `GET .../notes?order_by=updated_at&sort=desc` | Comments; system notes are skipped |
| `GET .../merge_requests/:iid/discussions` | Inline comments (`DiffNote`), with `position` |
| `GET .../merge_requests/:iid/approvals` | Who approved, with `approved_at` |
| `GET .../merge_requests/:iid/commits` | New commits and their authors, when `sha` moved |

### Identity, limits

- Identity: `GET /user` gives `id` and `username`; lists filter on `id`.
- A `read_api` token is enough.
- gitlab.com allows 2,000 authenticated requests a minute; self-managed
  defaults to 7,200 an hour when an administrator enables limits. Headers:
  `RateLimit-Remaining`, and `RateLimit-Reset` or `RateLimit-ResetTime`
  depending on the instance; both are read.

### What GitLab does not say

- Whether a label change, a push or an approval moves `updated_at`; the
  source code reads both ways. Diffing `labels` and `sha` covers it.
- A diff hunk for an inline comment: only the position. `hunk` is left out.
- Who made a push, short of the day-granular events API. `pushed` reports
  the commits' authors, matched on the identity's email.

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

A watched issue that no search returns any more, which means it was deleted
or the account can no longer see it, is reported once as it was and not asked
about again.

### Per issue that moved

| Request | Gives |
|---|---|
| `POST /rest/api/3/changelog/bulkfetch` for every issue that moved, no `fieldIds` filter | Who changed labels, assignee, status and text, oldest first. On failure, `GET /rest/api/3/issue/{key}/changelog` per issue |
| `GET /rest/api/3/issue/{key}/comment?orderBy=created` | Comments, with `author`; bodies are Atlassian Document Format, converted to Markdown. Read only for an issue new to the watch or whose comment total moved |

About 1 request idle and 3 to 5 when something moved.

The bulk request is the one Atlassian marks experimental; it takes at most
1,000 issues and 10 field ids, which is why wakewire sends none. A
changelog item names a status but not its category, so wakewire learns each
status's category, by id and by name, from the issues the search returns, and
reads a status change into Done as `closed` and out of it as `reopened`. A
change from or to a status it has not seen is left to the field diff.

### Identity, limits

- Authentication: Basic, as `email:API token`.
- Identity: `GET /rest/api/3/myself` gives `accountId`. Jira identifies people
  by account ID, and JQL accepts no username or email.
- API-token traffic stays under Jira's burst limits, whose figures Atlassian
  does not publish. A `429` is retried only after its `Retry-After`;
  `X-RateLimit-NearLimit: true`, which Jira sends when less than 20 % of its
  capacity remains, is shown in status.

### What Jira does not say

- Which edits move `updated`; diffing the fields covers all but comments,
  which also rely on the comment total.
- Whether the comment total is reported on `search/jql`. If it is not, a
  comment that moves nothing else is seen only when `updated` moves.
- How a label change is written in the changelog, one item per label or one
  joined string; both are read.
- Whether labels match case-sensitively; wakewire compares them without
  regard to case.
- Whether a status change names the statuses by id or by name in `from` and
  `to`; both are looked up.
- Whether an issue created with labels records them in its changelog. If it
  does not, a spec created already labelled shows no `label_added`.

## Problems

A source becomes a problem, delivered once, when:

| Problem | `code` |
|---|---|
| The service answers 401 or 403 | `token_refused` |
| The service does not answer for 3 polls in a row | `unreachable` |
| The remaining budget is below 5 % | `rate_limited`; the source is skipped until its reset time |

A problem clears on the first poll that succeeds; `status` shows when.
