# Configuration

Where wakewire keeps its files, what goes in each, and how a repository's
tracker and labels are read.

## Paths

Everything lives under one folder, `~/.wakewire/`, or under `$WAKEWIRE_HOME`
when it is set. `wakewire paths` prints them.

| Path | Holds | Who writes it | Mode |
|---|---|---|---|
| `config.toml` | Accounts, the poll default, the source template | You, or `wakewire account` | 644 |
| `credentials.toml` | Stored tokens, by account name | `wakewire account` only | 600 |
| `state.db` | Watches, registrations, snapshots, changes, batches, leases; see [state](state.md) | The binary | 600 |
| `cache/` | Lookups fetched again when missing, such as whose a token is | The binary | 700 |

The folder itself is created with mode 700. `credentials.toml` is kept apart
from `config.toml` so that the file people copy, share and commit to their
dotfiles never holds a token.

## `config.toml`

```toml
[poll]
interval = "2m"                      # default for every watch; 30s minimum

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

Every section and key is optional except the accounts a watch needs.

### The marker

A comment is an echo when its last non-empty line, with HTML tags and
Markdown emphasis removed and whitespace trimmed, equals `[echo] marker`. The
default is the line run-fleet's comment templates already end on,
`<kbd>🤖 unattendish agent</kbd>`, which reads as `🤖 unattendish agent` once
the tags are removed. Removing them also lets the line be recognised where a
service strips or converts HTML, as Jira's Atlassian Document Format may.
`register --marker` overrides it for one watch.

### Accounts

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Unique; the key of the account's entry in `credentials.toml`. `account add` takes letters, digits, `-` and `_` |
| `host` | yes | `github.com`, a GitLab host, or a Jira Cloud site. The service is known by the host: `github.com` is GitHub, a host ending `.atlassian.net` is Jira Cloud, any other is GitLab |
| `owners` | forge accounts | The organizations, users or groups whose repositories this account reads |
| `email` | Jira accounts | The email the Jira API token belongs to |
| `token` | yes | Where the token comes from (below) |
| `user` | no | The identity, when it is not the token's owner: a login on GitHub or GitLab, an account ID on Jira |

| `token` | Comes from |
|---|---|
| `stored` | `credentials.toml`, under the account's name |
| `env:<VAR>` | The environment variable `<VAR>` |
| `gh:<login>` | `gh auth token --hostname <host> --user <login>`, run when needed; `gh` 2.40 and later hold several logins per host. The login must be one `gh auth login` stored: a token `gh` only reads from `GH_TOKEN` is not one, and `gh` runs with `GH_TOKEN`, `GITHUB_TOKEN` and their enterprise forms removed, so the named login's own token comes back |

**Choosing an account.** A repository `<host>/<owner>/<repo>` reads through
the account whose `host` matches and whose `owners` lists `<owner>`. A Jira
tracker reads through the account whose `host` is its site. No match is
`no_account`. Two accounts claiming one host and owner make the configuration
invalid, since wakewire never guesses between them.

**Identity.** Each half of a watch has its own identity: the forge's from the
forge account, the tracker's from the tracker account. Left unset, it is
whoever the token belongs to (`GET /user` on GitHub and GitLab,
`GET /rest/api/3/myself` on Jira, cached in `cache/`).

## `credentials.toml`

```toml
[work-jira]
token = "…"
```

Written and rewritten only by `wakewire account add`, `rename` and `remove`.

| Mismatch | Result |
|---|---|
| `token = "stored"` with no entry under that name | That account fails with `no_stored_token` |
| An entry no account names | `doctor` warns, naming `wakewire account remove <name>` |

## The source template

Where in a repository wakewire reads the tracker and the labels. Its default,
built in, reads the `Forge and tracker` section that unattendish's
`set-up-unattendish` writes into `AGENTS.md`:

```toml
[source]
files   = ["AGENTS.md", "CLAUDE.md"]   # the first holding the section wins
heading = "Forge and tracker"
tracker = "Tracker:"                   # the line naming the tracker

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

Tracker: jira:acme.atlassian.net/PROJ

| Meaning | Label |
| --- | --- |
| spec | `type::spec` |
| ready | `ready-for-dev` |
| in progress | `in-progress` |
| needs input | `needs-input` |
```

- The section is found by its heading, at any level, matched without regard
  to case. The tracker line and the table must sit inside it.
- The tracker line's value is `github-issues`, `gitlab-issues` or
  `jira:<site>/<project key>`.
- Table keys match without regard to case; backticks around values are
  stripped.
- A configuration overrides only the keys it sets. Flags on `register`
  override the template.

The `Tracker:` line is new to `set-up-unattendish`, which today names the
tracker only in prose; it has to start writing the line for the default to
read it.

## Fixed in v1

| Setting | Value |
|---|---|
| Tick | 30 s |
| Poll budget | 60 s per `adapter next`; 15 s per request |
| Dormant after | 3 intervals without the adapter heard from |
| Ledger and dormant registrations kept | 30 days |
| Most changes listed in one wake | 20 |
