# wakewire

wakewire wakes an agent session when something it waits on changes on
GitHub, GitLab or Jira: a review on its pull request, an answer on its spec,
a label that says build it. It exists because an agent waiting on people
moves only when a person writes to its session, and nothing tells it that
someone answered elsewhere. It is shaped as a command-line binary that knows
the services and keeps the state, plus a small adapter inside each harness
that keeps time and delivers the wake, so that nothing runs in the
background, nothing found is ever lost, and a new harness costs only an
adapter. Its first user is unattendish's run-fleet, in Claude Code, the one
harness with an adapter.

```mermaid
flowchart LR
  Services["GitHub · GitLab · Jira"] -- "read only, polled" --> Binary["wakewire binary<br/>watches · snapshots · changes"]
  Binary <--> State[("~/.wakewire")]
  Adapter["Adapter in the harness<br/>(Claude Code mod)"] -- "tick: adapter next" --> Binary
  Adapter -- "wake" --> Session["Agent session"]
  Session -- "wakewire register" --> Binary
  Session -- "wakewire changes" --> Binary
```

How it works and why, every command and file, and the wake contract are in
the [docs](docs/README.md).

## Install

wakewire is two pieces: the `wakewire` binary, and the mod that wakes Claude
Code sessions. Install both.

### The binary

From a release, for Linux or macOS on amd64 or arm64; pick the archive for
your system on the [releases page](https://github.com/gperezmz/wakewire/releases):

```sh
VERSION=2.0.1
OS=linux ARCH=amd64        # or darwin, and arm64
curl -fsSLO "https://github.com/gperezmz/wakewire/releases/download/v$VERSION/wakewire_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/gperezmz/wakewire/releases/download/v$VERSION/checksums.txt"
sha256sum --check --ignore-missing checksums.txt   # on macOS: shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf "wakewire_${VERSION}_${OS}_${ARCH}.tar.gz" wakewire
install -m 755 wakewire ~/.local/bin/wakewire      # any folder on PATH
wakewire version
```

With Homebrew, on macOS or Linux; this repository is its own tap, which
Homebrew asks you to trust once:

```sh
brew tap gperezmz/wakewire https://github.com/gperezmz/wakewire
brew trust gperezmz/wakewire
brew install --cask wakewire
```

With Nix, built from source at a release's tag:

```sh
nix run github:gperezmz/wakewire/v2.0.1 -- version
nix profile install github:gperezmz/wakewire/v2.0.1
```

Without the tag it builds the default branch, which reports the last
release's version beside its own commit.

Or with Go 1.27.2 or later:

```sh
go install github.com/gperezmz/wakewire/v2/cmd/wakewire@latest
```

### The Claude Code mod

```sh
claude plugin install wakewire --marketplace gperezmz/wakewire
```

After a release, `claude plugin update wakewire@wakewire` brings the mod up
to it; a session takes it when it next starts.

The mod is a Claude Code plugin whose hooks module runs inside the session;
it needs function hooks enabled, and the `wakewire` binary on `PATH`. In a
session, the footer shows `↯` while the session holds a watch, with a few
words beside it when something needs you, and `/wakewire status` shows its
details. [The mod](docs/reference/claude-code-mod.md) says what each looks
like.

## Set up an account

An account says which token reads which repositories or Jira site. The token
is checked before anything is written; a stored one goes to
`~/.wakewire/credentials.toml` (mode 600), never to `config.toml`.

```sh
# GitHub, with the token in an environment variable
wakewire account add personal --host github.com --owner <login or org> --token env:GH_TOKEN

# GitLab, with a read_api token typed at the hidden prompt
wakewire account add gitlab --host <gitlab host> --owner <group or user>

# Jira Cloud, with an API token typed at the hidden prompt
wakewire account add jira --host <site>.atlassian.net --email <the token's email>

wakewire account list
wakewire doctor
```

`--token stored`, the default, reads the token from a hidden prompt, or from
stdin when stdin is not a terminal. `--token gh:<login>` asks `gh` for a login
`gh auth login` holds.

wakewire only ever reads from the services; a token that can write works,
but none is needed.

## Register a session

The repository says where its specs live and which labels mean what, in the
`Forge and tracker` section that unattendish's `set-up-unattendish` writes
into its `AGENTS.md`:

```markdown
## Forge and tracker

### Services

| Role | Service | Scope | Tool |
| --- | --- | --- | --- |
| tracker | GitHub | `acme/shop` | `gh` |
| forge | GitHub | `acme/shop` | `gh` |

### Labels

| Meaning | Label |
| --- | --- |
| spec | `type::spec` |
| ready | `ready-for-dev` |
| in progress | `in-progress` |
| needs input | `needs-input` |
```

The tracker is the forge's own issues (`GitHub` or `GitLab`, with the
repository as its scope) or `Jira`, with `<site>/<PROJECT KEY>` as its scope;
[the source template](docs/reference/configuration.md#the-source-template)
says what else it reads. Then, from a Claude Code session in the
repository, the agent runs:

```sh
wakewire register
```

From then on, a change on the identity's specs or pull requests wakes the
session with a message such as:

```text
[wakewire] New activity:
- PR #41: review, changes requested, by alice
Read batch 193 with `wakewire changes 193` before acting on it. If none of it needs you, a one-line reply is enough.
```

`wakewire status` shows every watch, `/wakewire pause` and `/wakewire resume`
hold and release wakes, and `wakewire ledger` says why a wake came or did not.

## Read the output

Every command prints [TOON](https://toonformat.dev) on stdout, errors
included, and `--json` prints one JSON object instead; `--help`, on any
command, and `--version` print plain text. stderr carries only progress. Exit codes are
three: 0 done, 1 refused (the error's `code` says why), 2 usage. Run alone,
`wakewire` shows the binary, this repository's watch and the next steps, and
asks no service:

```text
$ wakewire
bin: ~/.local/bin/wakewire
description: "Wakes an agent session when what it waits on changes on GitHub, GitLab or Jira"
watch:
  watch: github.com/wwuser/wakewire-sandbox
  identity: wwuser
  holder: dormant
  pending: 2
  problems: 0
  polled: 32h ago
  held: 0
  paused: no
watches: 2
accounts: 1
help[3]: Run `wakewire ledger` to see what changed and what was done with it,Run `wakewire status` for every watch,Run `wakewire doctor` when anything looks wrong
```

[The CLI reference](docs/reference/cli.md) has every command's output.

## Develop

`make check` runs what CI runs: gofmt, go vet, golangci-lint, govulncheck,
the tests with `-race`, GoReleaser's check, and the mod's typecheck,
`claude plugin validate .` and `claude plugin test mod`. CI runs the Go tests
on Linux and macOS and the mod's tests with Claude Code from npm, on every
push and pull request. A semver tag makes CI publish a release; the version
in `mod/.claude-plugin/plugin.json` is set to the tag's first, since the mod
and the Nix flake take theirs from it, and the release fails where they
differ.
