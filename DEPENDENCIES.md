# Dependencies

Tuon runs other people's code with access to your Messenger account and encryption keys. A dependency update
is where a supply-chain attack would get in, so updates are never automatic and always reviewed.

## Watched modules

These handle the network, your session or encryption. Read the diff of every update to them.

| Module | Why it matters | Reviewed version |
|---|---|---|
| `go.mau.fi/mautrix-meta` | Talks to Messenger, holds the session | v0.2610.0 |
| `go.mau.fi/whatsmeow` | End-to-end encryption, device keys | v0.0.0-20261014094007-f16f9ca30d6a |
| `go.mau.fi/libsignal` | Signal protocol (message encryption) | v0.2.2 |
| `github.com/beeper/req/v3` (replaces `imroc/req`) | HTTP client used by mautrix-meta | v3.0.0-20260925135712-b49593750e56 |
| `github.com/beeper/poly1305`, `github.com/beeper/argo-go` | Crypto / protocol helpers | d4eede7bbf3c / v1.1.2 |
| `golang.org/x/crypto` | Vault encryption, Argon2 | v0.58.0 |
| `github.com/ncruces/go-sqlite3` (+ `go-sqlite3-wasm`, `julianday`) | Encrypted `keys.db` | v0.36.0 / v6.4.35401 / v1.0.0 |
| `lukechampine.com/adiantum` | The cipher that encrypts `keys.db` | v1.1.1 |
| `github.com/zalando/go-keyring` | Only to move old sessions out of the keychain | v0.2.8 |

## Updating a dependency

1. Update one thing at a time: `go get <module>@<version>` then `go mod tidy`.
2. Run `./scripts/deps-review.sh`. It lists every module that changed, flags watched ones, checks checksums
   (`go mod verify`) and scans for known vulnerabilities.
3. For each **watched** module that changed: read the diff between the old and new version
   (`https://github.com/<owner>/<repo>/compare/<old>...<new>`). Look for new network calls, new places that
   read keys or cookies, obfuscated code, install-time scripts, or a new maintainer.
4. Update the "Reviewed version" column above in the same commit, and say what you checked in the commit message.
5. Run `./scripts/check.sh`, build, and test tuon on your account before releasing.

Never add a `replace` directive or a new watched module without the same review. `tuon update` shows users
every `go.mod` change and asks before building it.
