# Dependencies

Tuon runs other people's code with access to your Messenger account and encryption keys. A dependency update
is where a supply-chain attack would get in, so updates are never automatic and always reviewed.

Assume one of them will turn malicious some day. Every module linked into the chat app runs with the same rights,
not only the watched ones below, so a bump is reviewed as a whole, and the sandbox limits what a bad one can do
(SECURITY.md "Limits").

## Watched modules

These handle the network, your session or encryption. Read their diffs line by line. Every other changed module:
skim its diff, and never take a version younger than 14 days.

| Module | Why it matters | Reviewed version |
|---|---|---|
| `go.mau.fi/mautrix-meta` | Talks to Messenger, holds the session | v0.2610.0 |
| `go.mau.fi/whatsmeow` | End-to-end encryption, device keys | v0.0.0-20261014094007-f16f9ca30d6a |
| `go.mau.fi/libsignal` | Signal protocol (message encryption) | v0.2.3 |
| `github.com/beeper/req/v3` (replaces `imroc/req`) | HTTP client used by mautrix-meta | v3.0.0-20260925135712-b49593750e56 |
| `github.com/beeper/poly1305`, `github.com/beeper/argo-go` | Crypto / protocol helpers | d4eede7bbf3c / v1.1.2 |
| `golang.org/x/crypto` | Vault encryption, Argon2 | v0.58.0 |
| `github.com/ncruces/go-sqlite3` (+ `go-sqlite3-wasm`, `julianday`) | Encrypted `keys.db` | v0.36.0 / v6.4.35401 / v1.0.0 |
| `lukechampine.com/adiantum` | The cipher that encrypts `keys.db` | v1.1.1 |
| `github.com/zalando/go-keyring` | Only to move old sessions out of the keychain | v0.2.8 |

## Updating a dependency

1. Update one thing at a time: `go get <module>@<version>` then `go mod tidy`.
2. Run `./scripts/deps-review.sh`. It lists **every** module that changed in `go.mod` (a bump usually drags others
   along), whether tuon links it, whether it's watched, its age and a link to its diff (or a local `diff -ru`
   command). It **refuses any version younger than 14 days**: a hijacked maintainer account usually ships a fresh
   release, and most are caught within days. For a fix to a vulnerability tuon is actually hit by, pass
   `--allow module@version`. The age is the module proxy's commit time, which the author sets, so it stops a rushed
   release, not a carefully backdated one. Then it checks checksums and scans for known vulnerabilities.
3. Read the diffs: **watched** modules line by line, the rest skimmed. Look for new network calls, new places
   that read keys, cookies or files, `init()` code, obfuscated or generated blobs, or a new maintainer. A brand-new
   module gets read in full.
4. Update the "Reviewed version" column above in the same commit, and say what you checked in the commit message.
5. Run `./scripts/check.sh` (tests run sandboxed), build, and test tuon on your account before releasing. Run any
   `TUON_LIVE` test through `go run ./scripts/devsandbox --live -- go test …`: a new version's code runs there first.

Every module linked into the chat app runs with the same rights, not only the watched ones: see SECURITY.md
"Limits" for what a bad one can reach.

Never add a `replace` directive or a new watched module without the same review. `tuon update` shows users
every `go.mod` change and asks before building it.
