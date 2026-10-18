# Configuration

Where wakewire keeps its files, what goes in each, and how a repository's
tracker and labels are read.

## Paths

Everything lives under one folder, `~/.wakewire/`, or under `$WAKEWIRE_HOME`
when it is set. `wakewire paths` prints them.

| Path | Holds | Who writes it | Mode |
|---|---|---|---|
| `config.toml` | Accounts, the poll default, the source template | You, or `wakewire account` | 644 when `wakewire account` creates it; an edit keeps the file's mode |
| `credentials.toml` | Stored tokens, by account name | `wakewire account` only | 600 |
| `state.db` | Watches, registrations, snapshots, changes, batches, leases; see [state](state.md). SQLite keeps `state.db-wal` and `state.db-shm` beside it | The binary | 600 |
| `cache/` | Lookups fetched again when missing, such as whose a token is | The binary | 700 |

The folder itself is created with mode 700. `credentials.toml` is kept apart
from `config.toml` so that the file people copy, share and commit to their
dotfiles never holds a token.

## `config.toml`

```toml
[poll]
interval = "2m"                      # default for every watch, as 90s, 2m or 1d; 30s minimum

[echo]
marker = "🤖 unattendish agent"      # the line ending every comment the agents write

[[accounts]]
name   = "work-github"             # required, unique: the key into credentials.toml
host   = "github.com"
owners = ["acme"]              # the orgs or groups this account reads
token  = "gh:jdoe"          # stored | env:<VAR> | gh:<login>
# user = "jdoe"             # identity override; defaults to the token's owner

[[accounts]]
name   = "personal-github"
host   = "github.com"
owners = ["gperezmz"]
token  = "gh:gperezmz"

[[accounts]]
name   = "client-gitlab"
host   = "gitlab.client.com"
owners = ["platform"]
token  = "env:CLIENT_GITLAB_TOKEN"

[[accounts]]
name   = "work-jira"
host   = "acme.atlassian.net"
email  = "jdoe@example.com"         # required for Jira: Jira Cloud tokens authenticate as email + token
token  = "stored"
```

Every section is optional. Each `[[accounts]]` block needs `name`, `host`
and `token`, and one invalid block makes the whole file `config_invalid`, for
every command that reads the accounts. A key wakewire does not know is ignored
without a warning, so a misspelt key reads as a missing one.

### The marker

A comment is an echo when its last non-empty line, with HTML tags and
Markdown emphasis removed and whitespace trimmed, equals `[echo] marker`. The
default is the line run-fleet's comment templates already end on,
`<kbd>🤖 unattendish agent</kbd>`, which reads as `🤖 unattendish agent` once
the tags are removed. Removing them also lets the line be recognised where a
service strips or converts HTML, as Jira's Atlassian Document Format may.
An empty marker means the default. The marker is stored on the watch at each
`register`: `register --marker` sets it for one watch until its next
`register`, and a changed `[echo] marker` reaches a watch when it next
registers.

### Accounts

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Unique; the key of the account's entry in `credentials.toml`. `account add` and `account rename` take letters, digits, `-` and `_`, starting with a letter or digit, up to 64 characters |
| `host` | yes | `github.com`, a GitLab host, or a Jira Cloud site, bare, with no scheme or port, matched without regard to case. The service is known by the host: `github.com` is GitHub, a host ending `.atlassian.net` is Jira Cloud, any other is GitLab. GitLab and Jira are called at `https://<host>`, GitHub at `api.github.com`, so a GitHub Enterprise host is read as GitLab |
| `owners` | forge accounts, at least one; refused on Jira accounts | The organizations, users or groups whose repositories this account reads, matched without regard to case against the first segment of the repository's path: on GitLab, the top-level group |
| `email` | Jira accounts; refused on others | The email the Jira API token belongs to |
| `token` | yes | Where the token comes from (below) |
| `user` | no | The identity, when it is not the token's owner: a login on GitHub or GitLab, an account ID on Jira |

| `token` | Comes from |
|---|---|
| `stored` | `credentials.toml`, under the account's name |
| `env:<VAR>` | The environment variable `<VAR>`, a name of letters, digits and `_` not starting with a digit; the value is trimmed of surrounding whitespace |
| `gh:<login>` | `gh auth token --hostname <host> --user <login>`, run when needed; `gh` 2.40 and later hold several logins per host. The login must be one `gh auth login` stored: a token `gh` only reads from `GH_TOKEN` is not one, and `gh` runs with `GH_TOKEN`, `GITHUB_TOKEN` and their enterprise forms removed, so the named login's own token comes back. Each run has 10 s |

**Choosing an account.** A repository `<host>/<owner>/<repo>` reads through
the account whose `host` matches and whose `owners` lists `<owner>`. A Jira
tracker reads through the account whose `host` is its site. No match is
`no_account`. Two accounts claiming one host and owner make the configuration
invalid, since wakewire never guesses between them.

**Identity.** Each half of a watch has its own identity: the forge's from the
forge account, the tracker's from the tracker account. Left unset, it is
whoever the token belongs to (`GET /user` on GitHub and GitLab,
`GET /rest/api/3/myself` on Jira). With `user` set, that login is looked up
instead (`GET /users/<login>` on GitHub, `GET /users?username=<login>` on
GitLab); a Jira account ID is taken as given. The answer is cached in
`cache/`, keyed by the account and a hash of its host, user and token, so a
new token is looked up again.

## `credentials.toml`

```toml
[work-jira]
token = "…"
```

Written and rewritten only by `wakewire account add`, `rename` and `remove`,
each time whole, so a comment or an edit made by hand is lost.

| Mismatch | Result |
|---|---|
| `token = "stored"` with no entry under that name, or an entry whose token is empty | That account fails with `no_stored_token` |
| An entry no account names, its token empty or not | `doctor` warns, naming `wakewire account remove <name>`, which removes it |

## The source template

Where in a repository wakewire reads the tracker and the labels. Its default,
built in, reads the `Forge and tracker` section that unattendish's
`set-up-unattendish` writes into `AGENTS.md`:

```toml
[source]
files   = ["AGENTS.md", "CLAUDE.md"]   # the first holding the section wins
heading = "Forge and tracker"
tracker = "Tracker:"                   # a line naming the tracker, where no Services table does

[source.services]                      # the table naming the tracker and the forge
role    = "Role"
service = "Service"
scope   = "Scope"
tracker = "tracker"                    # the Role of each row
forge   = "forge"

[source.table]
key   = "Meaning"
value = "Label"

[source.labels]                        # which key means what to wakewire
spec        = "spec"
ready       = "ready"
in_progress = "in progress"
needs_input = "needs input"
```

What the default reads:

```markdown
## Forge and tracker

### Services

| Role | Service | Scope | Tool |
| --- | --- | --- | --- |
| tracker | Jira | `acme.atlassian.net/PROJ` | its MCP server |
| forge | GitHub | `acme/shop` | `gh` |

### Labels

| Meaning | Label |
| --- | --- |
| spec | `type::spec` |
| ready | `ready-for-dev` |
| in progress | `in-progress` |
| needs input | `needs-input` |
```

- The section is found by its heading, a Markdown `#` heading at any level,
  matched without regard to case, HTML tags or emphasis. Its tables sit inside
  it, subtitles under it included, up to the next heading of its level or
  above. Lines inside code fences are skipped. The files are read from the
  repository's root, and not at all when `--tracker` and every `--label` are
  given.
- A table is found by its header row, which holds the template's columns;
  other columns may sit beside them. Header cells match without regard to
  case and nothing else, so `**Meaning**` is not `Meaning`. Roles and keys
  also match without regard to HTML tags or emphasis, and the first row for
  each role or meaning wins. Backticks around values are stripped.
- The tracker row gives the tracker: Service `GitHub` or `GitLab` reads that
  forge's issues, and only where it is the watched repository's own forge and
  Scope is its `<owner>/<repo>`; `Jira` reads the project its Scope names as
  `<site>/<PROJECT KEY>`, the key in capitals, at least two characters. Any
  other service is refused, naming the row.
- The forge row, where there is one, has to name the repository watched and
  its forge; `none` is refused, since wakewire watches a forge.
- Where there is no tracker row, a line can name the tracker:
  `Tracker: github-issues`, `gitlab-issues` or `jira:<site>/<project key>`,
  as a list item too, with backticks or emphasis around the value ignored.
  The tracker row wins over the line, and `--tracker` over both.
- No file holding the section is `no_source`; a section with no tracker, a
  missing label row, or a row refused above is `source_incomplete`.
- A configuration overrides only the keys it sets, and `files` replaces the
  whole list. An empty `[source.services] role` turns the Services table off,
  and an empty `[source] tracker` the line. The keys under `[source.labels]`
  are `in_progress` and `needs_input`; the meanings `--label` and the output
  name are `in-progress` and `needs-input`. Flags on `register` override the
  template.

## Fixed

| Setting | Value |
|---|---|
| Tick | 30 s |
| Poll interval | 2 m by default, 30 s at least |
| Poll budget | 60 s per `adapter next`; 15 s per request |
| Poll lease | 90 s |
| Dormant after | More than 3 intervals without the adapter heard from |
| Unreachable reported after | 3 unreachable polls in a row |
| Ledger and dormant registrations kept | 30 days |
| Clean-up | At most once a day, by the first `register` or `adapter next` |
| Most lines in one wake | 20; see [the wake](wake-contract.md#the-wake) |
