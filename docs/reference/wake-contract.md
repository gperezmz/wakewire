# Wake contract, protocol 1

What wakewire delivers into a session, what a batch holds, and which changes
wake. This page is the one authority for it: an adapter, a skill such as
run-fleet, and any reader of `wakewire changes` rely on it by protocol number.
Changing a field, a kind or the wake's shape is a new protocol;
`wakewire version --json` reports the one the binary speaks.

## What is watched

For one watch, two sets, both decided by its [identity](../../GLOSSARY.md):

| Set | On | Is |
|---|---|---|
| **Specs** | The tracker | Items in the identity's partition carrying the spec label: assigned to the identity, or filed by it and assigned to nobody |
| **Pull requests** | The forge | Pull requests (merge requests on GitLab) the identity opened in the repository |

An item stays watched after it leaves its set, so that leaving is seen, and
is dropped once that change is recorded.

## What wakes

| On | Change | `kind` |
|---|---|---|
| Spec | The ready label added or removed | `label_added`, `label_removed` |
| Spec | The spec label added or removed | `label_added`, `label_removed` |
| Spec | A comment written | `comment` |
| Spec | Closed or reopened | `closed`, `reopened` |
| Spec | Assigned away from the identity, or to it | `reassigned` |
| Spec | Title or description edited, reported once the text holds still for one poll | `edited` |
| Pull request | A comment written in the conversation | `comment` |
| Pull request | A comment written on a line of the diff | `inline_comment` |
| Pull request | A review submitted: approved, changes requested, commented | `review` |
| Pull request | An approval given (GitLab) | `review` |
| Pull request | Merged | `merged` |
| Pull request | Closed without merging, or reopened | `closed`, `reopened` |
| Pull request | Its head moved with commits authored by someone other than the identity | `pushed` |
| Watch | A problem began | `problem` |

## What never wakes

| Change | Why |
|---|---|
| A comment ending with the [marker](configuration.md#the-marker), by default run-fleet's `<kbd>🤖 unattendish agent</kbd>` | An echo: the holding session's own comment. Agents acting as the identity cannot be told apart from it by author, so every comment an agent writes on a watched item ends with this line |
| Any change to the in-progress or needs-input label | An echo: only agents set them, and an agent acting as the identity cannot be told apart from the identity |
| Items outside both sets | Not the watch's |
| Comment edits and deletions | Not recorded in v1 |
| CI checks | Not watched in v1 |

Ignored changes are still stored, with the reason, in the ledger.

## The wake

One prompt per batch. Its first and last lines are wakewire's own fixed
words; the lines between are facts only: the item, the kind, the login,
never a title or anything a person wrote. As the binary writes it for the
Claude Code mod:

```text
[wakewire] New activity:
- PR #41: review, changes requested, by alice
- PR #41: 2 inline comments by alice
- PROJ-12: label "ready-for-dev" added by jdoe
Read batch 193 with mcp__wakewire__changes before acting on it. If none of it needs you, a one-line reply is enough.
```

- It opens with `[wakewire]`, so a skill tells a wake from its user typing.
  It names no repository, since a session holds one watch, and no time,
  since the batch has each change's `at`.
- The last line names the batch once and the one way to read it, which the
  adapter chooses with `adapter next --reader`: `tool` names the mod's
  `mcp__wakewire__changes`, `cli` names `wakewire changes 193`. It asks for
  the batch to be read before anything is done about it, and a short reply
  when nothing in it needs the session; what needs it is the session's own
  workflow to judge.
- Changes to one item and kind are counted on one line.
- A reassignment reads `reassigned` or `unassigned`, without who from and
  to: Jira names people by account ID, no word for a wake. The batch has
  `from` and `to`.
- At most 20 lines; past that, the lines become counts per item, then
  `- and N more`, and the batch holds them all.
- A wake of problems alone is one line per problem, naming the command that
  shows its fix:

```text
[wakewire] Not watching GitHub: GitHub refused the token (401). Run `wakewire status` for the fix.
```

## The batch

What `mcp__wakewire__changes` and `wakewire changes <batch> --json` return.
Illustrative values:

```json
{
  "protocol": 1,
  "batch": 193,
  "watch": "github.com/acme/x",
  "identity": "jdoe",
  "since": "2026-10-16T09:02:00Z",
  "until": "2026-10-16T09:14:00Z",
  "changes": [
    {
      "item": "PR #41",
      "url": "https://github.com/acme/x/pull/41",
      "kind": "review",
      "by": "alice",
      "at": "2026-10-16T09:05:12Z",
      "state": "changes_requested",
      "body": "Two things before this can land: …",
      "truncated": false
    },
    {
      "item": "PR #41",
      "url": "https://github.com/acme/x/pull/41#discussion_r1847",
      "kind": "inline_comment",
      "by": "alice",
      "at": "2026-10-16T09:05:12Z",
      "path": "src/register.go",
      "line": 88,
      "hunk": "@@ -84,6 +84,9 @@\n if repo == \"\" {\n+    return nil",
      "body": "This should return an error, not nil.",
      "truncated": false
    },
    {
      "item": "PROJ-12",
      "url": "https://acme.atlassian.net/browse/PROJ-12",
      "kind": "edited",
      "by": "jdoe",
      "at": "2026-10-16T10:41:07Z",
      "field": "description",
      "diff": "@@ Acceptance criteria @@\n- Export as CSV\n+ Export as CSV and XLSX"
    }
  ]
}
```

### Fields

Every change has `item`, `url`, `kind` and `at`; the rest depend on the kind.

| Field | On | Meaning |
|---|---|---|
| `item` | all | The service's own reference: `#12` and `PR #41` on GitHub, `#12` and `!41` on GitLab, `PROJ-12` on Jira |
| `url` | all | Where the change can be read in full |
| `kind` | all | One of the kinds above |
| `at` | all | When it happened, UTC |
| `by` | all but `problem` | The login or Jira display handle that made it; absent where the service does not say |
| `body` | `comment`, `inline_comment`, `review` | What was written, as Markdown; Jira's Atlassian Document Format is converted |
| `truncated` | with `body` | `true` when `body` was cut at 4,000 characters; `url` leads to the rest |
| `state` | `review` | `approved`, `changes_requested`, `commented` or `dismissed` |
| `path`, `line` | `inline_comment` | The file and line commented on |
| `hunk` | `inline_comment` | The diff lines around it, where the service gives them; GitLab does not |
| `label` | `label_added`, `label_removed` | The label |
| `from`, `to` | `reassigned` | The identities assigned before and after |
| `field`, `diff` | `edited` | `title` or `description`, and a unified diff from before the first edit to after the last |
| `head`, `commits` | `pushed` | The new head commit, and each new commit's id and author |
| `code`, `text` | `problem` | One of the [CLI's error codes](cli.md#error-codes), and a sentence for a person |

## What a reader may rely on

- A change may appear in two batches, when a batch was offered and not
  acknowledged. A change found is never dropped.
- A batch is a snapshot of when it was polled. An act that cannot be undone,
  such as a merge, reads the live item first.
- Items are named by the service's own reference, so `gh`, `glab` or the URL
  reach them directly.
