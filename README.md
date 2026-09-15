# basa

A command-line client for Basa. It reads the same data you can see in the browser, as you, with the
same permissions.

**Internal and unstable.** No compatibility guarantees. Commands and output may change without
notice.

- [Getting set up](#getting-set-up)
- [Using it](#using-it)
- [Output](#output)
- [Exit codes](#exit-codes)
- [Rate limits](#rate-limits)
- [Sessions and security](#sessions-and-security)
- [Development](#development)

## What it deliberately cannot do

It has no administrative capability, and it never will. No database access, no running arbitrary
code, no looking at other teams' data, no migrations, no deploys, no cloud resources. Everything it
can reach, you could already reach by logging into Basa in a browser.

If you need something it refuses to do, that is a conversation with an engineer, not a missing flag.

---

## Getting set up

Three steps, in order. Steps 1 and 2 need someone else; 3 is yours.

### 1. Get API access turned on

Someone at Basa enables **API access** for your account — it is a per-user setting in the Basa admin.
Until that happens, every command will tell you access is not enabled.

### 2. Install it

Paste this into Terminal:

```bash
curl -fsSL https://raw.githubusercontent.com/Basa-Futura/basa-cli/main/scripts/install.sh | bash
```

It works out your platform, verifies the download against the published checksums, and installs to
`~/.local/bin/basa`. If that is not on your `PATH` it tells you the one line to add.

```bash
basa version          # check it worked
```

> **This works because the repository is public and publishes releases** — the installer
> authenticates with nothing, which is what makes it one line. Re-running it is also how you
> upgrade: it overwrites the binary in place, and there is deliberately no self-update. See
> [docs/INSTALL.md](docs/INSTALL.md) for the from-source path and the reasoning.

### 3. Log in

```bash
basa auth login --env staging --url https://staging.basa.example
```

That prints a link to an approval page in Basa and opens it for you. Approve there, and Basa mints
the token and shows it **once**, with a copy button. Copy it, come back to the terminal, and paste it
at the prompt.

The token can read the same things you can already see in Basa, and nothing else. The approval page
decides that, so there is nothing to pick and nothing to get wrong. If you lose the token, run the
command again — a fresh one costs nothing, and the old one dies on its own.

It is not echoed when you paste it, and it is never accepted as a command argument — that would
leave it sitting in your shell history.

Somewhere a browser cannot open — over SSH, say — the link still prints, and `--no-browser` skips
the attempt:

```bash
basa auth login --env staging --no-browser
```

The token goes into your macOS keychain. If no keychain is available it falls back to a file at
`~/.config/basa/credentials.json` readable only by you, and tells you it did that.

Check it worked:

```bash
basa auth status --env staging
```

---

## Using it

Run `basa` on its own at any time to see the available commands.

| Command | Answers |
|---|---|
| `basa projects list` | What campaigns are active |
| `basa projects show <id>` | Everything about one campaign |
| `basa deals list` | What is outstanding right now |
| `basa deals show <id>` | Where one deal stands |
| `basa contracts list` | What is awaiting signature |
| `basa contracts show <id>` | Where one contract stands |
| `basa me` | Who am I, what teams can I see, when does my token expire |
| `basa auth login` | Store a token for an environment |
| `basa auth status` | Same as `me`, phrased as a health check |
| `basa auth logout` | Remove the stored token from this machine |
| `basa version` | Which build this is |
| `basa licenses` | Third-party licence notices, in full |

### Which environment — always required

**Every command that talks to Basa needs `--env` (or `-e`). There is no default, on purpose.** A
tool that quietly assumes production is one typo away from trouble, so `basa` would rather ask than
guess — even when only one environment is configured. `basa version` and `basa --help` are the
exceptions: they never reach a server, so they never ask which one.

```bash
basa deals list --env staging
basa deals list -e staging

export BASA_ENV=staging      # or set it once for your shell
basa deals list
```

Your environments live in `~/.config/basa/config.json`. Tokens do not — they are never written
there.

### Which team

If you belong to one team, `basa` uses it. If you belong to several, pass `--team` (or `-t`) — by
name, by a unique part of the name, or by id:

```bash
basa deals list -e staging --team "Acme Agency"
basa deals list -e staging --team acme          # a unique fragment is enough
```

A fragment matching two of your teams is an error, not a coin flip. The team in use is printed above
every result, so you can always see which one you are looking at.

### Projects

A project is one campaign: a brand, a set of roles, and the deals run under it.

```
$ basa projects list -e staging --archived all
staging · Acme Agency
ID                                    NAME             BRAND              TYPE       NDA  ARCHIVED  UPDATED
9f2c0001-2222-3333-4444-555566667701  Autumn Launch    Northwind Trading  social     yes  no        2026-08-19
9f2c0002-2222-3333-4444-555566667702  Spring Campaign  Acme               social     no   no        2026-08-17
9f2c0003-2222-3333-4444-555566667703  Holiday Push     Glow & Co          affiliate  no   yes       2026-06-02
```

**Archived projects are hidden unless you ask**, the same way the Basa web app hides them. The
`--archived` flag has three settings rather than being on/off, because "only the archived ones" and
"both" are different questions:

| Flag | Effect |
|---|---|
| `--archived` | `false` for active only (the default), `true` for archived only, `all` for both |
| `--search` | Only projects whose name, or whose brand's name, contains this |
| `--limit`, `-n` | How many to show, 1–100 (default 25) |

Columns that would say the same thing on every row are said once in the heading instead — so a page
that is all one brand loses the **BRAND** column and names the brand above the table, and the
**ARCHIVED** column appears only when a page actually holds both. **NDA** is whether the campaign
gates outreach behind an NDA.

```bash
basa projects list -e staging --archived all
basa projects list -e staging --search northwind
basa projects show 9f2c0001-2222-3333-4444-555566667701 -e staging
```

`projects show` adds the fields a list has no room for — among them the name talent sees on the
outreach surface, which is often unset, in which case they see the internal name instead.

**This is where a project id for `basa deals list --project` comes from.**

### Deals

```
$ basa deals list -e staging
staging · Acme Agency · Spring Campaign (Northwind)
2 deals · 1 Awaiting signature · 1 Outreach sent
ID     STATUS              STAGE        COUNTERPARTY  ROLE          ASSIGNED   UPDATED
EfhxL  Awaiting signature  Contracting  Sam Rivera    Lead Creator  Dana Reed  2026-08-19
gbHJd  Outreach sent       Outreach     Jordan Lee    Lead Creator  —          2026-08-19
```

Three things about that layout. When every deal on the page belongs to one project, the project and
brand are said once in the heading rather than repeated down two columns — the columns come back the
moment a second project appears. Rows sharing a status sit together, the group touched most recently
first, so a block of thirty parked deals reads as a block rather than being interleaved with live ones.
And the tally line above the table gives the shape of the list before the rows do. Heading and tally
go to stderr with the other context, so a pipeline never sees them.

**`STAGE` and `STATUS` are different things.** Stage is the coarse pipeline position and is what
`--stage` filters on. Status is the finer lifecycle the Basa web app shows, so it is the column to read
when you want the answer a colleague sees in the browser.

| Flag | Effect |
|---|---|
| `--stage` | `outreach`, `negotiation`, `contracting`, `execution` |
| `--project` | Only this project (a project id) |
| `--limit`, `-n` | How many to show, 1–100 (default 25) |
| `--all` | Every page, not just the first. Cannot be combined with `--limit` |

```bash
basa deals list -e staging --stage contracting
basa deals show EfhxL -e staging
```

If more deals match than are shown, it says so — a partial list is never left looking complete — and
`--all` fetches the rest, walking every page at the server's largest page size.

### Contracts

"What is awaiting signature" is `--status ready_for_signature`.

```
$ basa contracts list -e staging --status ready_for_signature
staging · Acme Agency
ID     STATUS               NAME                       RECIPIENT   DEAL   UPDATED
EfhxL  Ready for Signature  Spring Campaign agreement  Sam Rivera  VqXmZ  2026-08-19
```

| Flag | Effect |
|---|---|
| `--status` | `draft`, `ready_for_signature`, `signed`, `declined`, `voided` |
| `--limit`, `-n` | How many to show, 1–100 (default 25) |

A contract in the **DEAL** column reading `standalone` is not missing data — it is a contract created
without a deal attached, which is a normal shape.

### A note on ids

Deal and contract ids are short strings like `EfhxL`. **The same string can be a valid id for more
than one kind of thing** — a deal and a contract can share one. That is fine as long as you use it
with the command it came from: `basa contracts show EfhxL` and `basa deals show EfhxL` are both valid
and will show you different things. If you get an unexpected result, check you are using the right
command for the id.

**Project ids are the exception: they are long UUIDs** like
`9f2c0001-2222-3333-4444-555566667701`, so they cannot be confused with anything else, and the
warning above does not apply to them. A project id copied out of a Basa web URL works too.

---

## Output

A table by default, for reading. JSON for scripting, with `--json`:

```bash
basa deals list -e staging --json | jq -r '.data[].counterparty.name'
```

**Only the table or the JSON goes to stdout.** Every message, warning, and error goes to stderr — so
a pipeline keeps working even when a command fails, and the JSON stays parseable either way.

## Exit codes

| Code | Meaning | What to do |
|---:|---|---|
| `0` | It worked | — |
| `1` | Something about the command was wrong | Read the message; it says what to fix |
| `2` | That thing does not exist, or you cannot see it | Check the id |
| `3` | You are not logged in, or your session ended | `basa auth login --env <name>` |
| `4` | You are not allowed to do that | Ask a Basa administrator for access |

`3` and `4` are separate because the fix is different: one you can do yourself, the other needs a
person.

## Rate limits

The API allows **60 requests a minute, per token**. Past that it refuses, and `basa` exits `1` telling
you how long to wait:

```
Too many requests.
Wait 34 seconds and try again.
```

That number comes from the server rather than a guess, so it is the real one. If the server declines to
say, the message falls back to "wait a minute" instead of inventing a figure.

**`basa` never retries on its own.** A tool that quietly sleeps and tries again is one you cannot
reason about from a script, and a loop that backs off invisibly hides the fact that it is being
throttled at all. In `--json` mode the wait is in `error.hint` on stdout, so a script can read it and
decide for itself.

## Sessions and security

**Sessions are short — currently 8 hours**, so you will log in roughly twice a day. That is deliberate:
the token sits on your laptop, and a short life limits the damage if the laptop goes missing. The exact
length is set by the server and can change, so `basa auth status` is the authority, not this page.

`basa auth logout` removes the copy on your machine. It does **not** revoke the token on the server.
If you think a token has been exposed, delete it in Basa under **API Tokens** — the ones `basa auth
login` made are named **Basa CLI (paired)**, so they are easy to pick out. If the whole account
is a concern, have an administrator turn off your API access — that kills every token you hold at
once.

Token issuance, use, and revocation are all logged on the server, by user and time. Token values are
never logged.

### Environment variables

| Variable | Effect |
|---|---|
| `BASA_ENV` | The environment to use, instead of `--env` |
| `BASA_TOKEN` | Use this token and ignore stored credentials entirely. This is what scripts and CI use |
| `BASA_NO_KEYRING` | Set to anything to force file storage instead of the keychain |
| `XDG_CONFIG_HOME` | Move the config directory off `~/.config` |

---

## Development

Requires Go 1.26. `.tool-versions` pins it for asdf and mise.

```bash
make check        # fmt-check + vet + test — run this before committing
make test-race
make build
make build-all    # cross-compile to dist/, with checksums
make help         # list every target
```

### How it fits together

This repository is only the client. The API it talks to lives in the **basa-web** repository, and
the two meet at HTTP and nowhere else — this module imports nothing from the application.

| Where | What |
|---|---|
| `basa-web` → `docs/cli/api.md` | The API reference: endpoints, gates, status codes |
| `basa-web` → `docs/cli/scope.md` | Scope of record for both halves: what is in, what is cut, and why |
| here → `docs/INSTALL.md` | Install, upgrade, and why the repository is public |

### Layout

```
cmd/basa/          entry point
internal/cli/      command tree; the one place an error becomes a message and an exit code
internal/commands/ one file per resource — flags, prompts, and formatting only
internal/client/   HTTP client for /api/v1; maps status codes onto the exit-code contract
internal/config/   environments and credential storage
internal/output/   table and JSON rendering; owns the stdout/stderr split
internal/fail/     the error type and the exit codes
```

Commands never build HTTP requests, and the client never formats output. Keeping that separation is
what stops domain logic leaking into a Go binary that has no business holding any.

## Licence

**Proprietary — source-visible, not open source.** Copyright (c) 2026 Basa Futura, all rights reserved.
Published so the people who run it can read and verify it, and so it can be installed without
authenticating to a private repository. See [LICENSE](LICENSE).

Third-party components are used under their own permissive licences (MIT, Apache-2.0, BSD) — none of
which requires this software to be open source. See [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md),
or run `basa licenses` to print every notice in full from the binary itself.
