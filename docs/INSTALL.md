# Installing basa

## For operators

```bash
curl -fsSL https://raw.githubusercontent.com/Basa-Futura/basa-cli/main/scripts/install.sh | bash
```

That is the whole thing. It works out your platform, downloads the matching binary, verifies its
SHA-256 against the published checksums, and puts it in `~/.local/bin/basa`. If that directory is not
on your `PATH` it tells you the exact line to add.

Then:

```bash
basa auth login --env staging --url https://staging.basa.example
```

### Why `curl` and not a download link

macOS quarantines anything a browser, Slack, Mail, or AirDrop writes to disk, and Gatekeeper then
refuses to run it — for a non-engineer that is a hard stop, and clearing it means either Terminal
commands or a code-signing certificate.

**A file fetched with `curl` carries no quarantine attribute.** So piping the installer sidesteps
Gatekeeper entirely, with no code signing and no Apple Developer account. And since using a CLI means
opening Terminal anyway, pasting one line there costs nothing extra.

This is the same approach the Basecamp CLI uses (`curl -fsSL https://basecamp.com/install-cli | bash`).

### Options

| Variable | Effect |
|---|---|
| `BASA_INSTALL_DIR` | Where to put the binary (default `~/.local/bin`) |
| `BASA_VERSION` | Install a specific version instead of the latest |

---

## Why the repository is public

**Settled: `Basa-Futura/basa-cli` is public, and publishes releases.** The reasoning is kept here
because it is the kind of decision someone will want to re-litigate later.

The installer authenticates with nothing, which is what makes it a single pasteable line. Both
halves need public access:

- `raw.githubusercontent.com/.../scripts/install.sh` — fetching the script itself
- `github.com/.../releases/download/...` — fetching the binary and checksums

A private repository returns 404 for both. There is no way around that short of putting a GitHub token
on the operator's laptop, which is a long-lived secret on an unmanaged device — precisely what the
short token lifetime elsewhere in this design exists to avoid.

**Making `Basa-Futura/basa-cli` public** was a much easier case to make than it would be for the
application repository:

- It contains no business logic, no domain code, no credentials, and no customer data. It is a thin
  HTTP client, audited against that claim rather than against its size (see AUDIT.md) — the audit runs
  commands, which a quoted line count cannot.
- The only thing it reveals is the shape of an internal API — which requires a valid token *and* the
  `feature-api-tokens` flag on the account before it returns anything at all.
- This is the normal arrangement. Basecamp, HEY, and Fizzy all ship public CLIs against authenticated,
  non-public APIs.

The application repository stays private either way. Nothing here changes that.

Both halves are now true, so the one-line install above works. The installer still fails with a clear
message rather than a stack trace if it ever cannot resolve a release — which is what you would see
during the window between a tag being pushed and its release workflow finishing.

## Publishing a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

`.github/workflows/release.yml` runs `make check`, cross-compiles for macOS and Linux on arm64 and
amd64, and attaches the binaries plus `checksums.txt` to the GitHub release. The installer reads that
release. Asset names in the workflow and in `scripts/install.sh` have to stay in step.

**Limit of the checksum check.** `checksums.txt` is published in the same release as the binary, so it
proves the download was not corrupted or altered in transit — not that the release itself is genuine.
Anyone who could publish a release could publish matching checksums. Closing that would need signed
releases (cosign or minisign) and a pinned public key in the installer. The Basecamp CLI installer has
the same property; noting it so nobody mistakes the check for more than it is.

A tag never ships without passing its own tests — `make check` runs first and fails the release.

---

## For engineers: building from source

Needs Go 1.26, which `.tool-versions` pins for asdf and mise.

```bash
asdf plugin add golang && asdf install golang 1.26.6

git clone https://github.com/Basa-Futura/basa-cli.git
cd basa-cli
make install          # builds and copies to ~/.local/bin/basa
basa version
```

Check `~/.local/bin` is on your `PATH`:

```bash
echo $PATH | tr ':' '\n' | grep -q "$HOME/.local/bin" && echo "on PATH" || \
  echo 'add to your shell profile: export PATH="$HOME/.local/bin:$PATH"'
```

### Building for someone else

```bash
make build-all
```

Writes `dist/basa-{darwin,linux}-{arm64,amd64}` and `checksums.txt`. Apple Silicon needs
`darwin-arm64`; Intel Macs need `darwin-amd64`.

**Handing one of these over directly re-introduces the Gatekeeper problem** — the recipient's browser
or Slack will quarantine it. Prefer the installer.

## Uninstalling

```bash
rm ~/.local/bin/basa
rm -rf ~/.config/basa          # config, and credentials if the keychain was unavailable
```

If credentials went to the macOS keychain rather than a file, remove them in Keychain Access by
searching for **basa**.
