# Security

Tuon logs in to Messenger as you, keeps your encryption keys, and shows text and photos other people send. This page covers how it protects those and what it can't protect.

## Your login

- **Your password is never stored.**
  - With the browser login, you type it into Facebook's own login page, in a normal browser window where you can check the address.
  - With the password login, it goes straight to Meta and is then dropped.
- **Only the session is kept, encrypted.** It's the cookies Meta gives back after you log in, stored in `login.enc` under tuon's own key (see below). Other apps can't read it: it is no longer kept in the keychain, where any program running as you could read it without a prompt.
- **The browser login uses a throwaway profile.** It never reads your normal browser's cookies, and the temporary profile is deleted afterwards. Tuon talks to that window over a private pipe, not a network port, so no other program on your computer can connect to it and read its cookies.
- **Your Messenger PIN is never stored.** `tuon history` uses it once to join Messenger's encrypted backup. The backup keys it gets back are stored encrypted.
- **You can remove it.** `tuon logout` deletes the session. `tuon logout --wipe` also deletes all local data.

## The lock and tuon's key

- **Tuon has its own key, and opening tuon asks for Touch ID or your Mac password.** The key is wrapped by a key inside your Mac's Secure Enclave, which never leaves the chip; only a small helper (`tuon-keyhelper`, built from source by `install.sh`) asks the chip to use it. On Linux and Windows, or a Mac without a Secure Enclave, a passphrase (stretched with Argon2id) protects the key instead, with longer waits after wrong tries.
- **You unlock once per start.** A small launcher asks for Touch ID and hands the key to the chat app over a private pipe, never through a file or an environment variable.
- **Tuon locks itself after 10 minutes without input**, or when you type `:lock`. The screen is cleared until you unlock.
- **`:previews off`** hides message text in the chat list and while browsing, for when someone can see your screen.
- **If you lose the passphrase** (or the Mac's Secure Enclave key, e.g. after resetting the Mac), the data can't be opened: log in again and restore older chats with your Messenger PIN.

## The sandbox (macOS)

The chat app runs inside a macOS sandbox, so even a bug or a bad dependency inside tuon is fenced in:

- **It can't read your other secrets:** SSH and cloud keys (`~/.ssh`, `~/.aws`, …), keychains, browser profiles and cookies, password managers, shell history, Mail and Messages.
- **It can only write** to its own folder, Downloads (files you save) and the temp folder. It can't change your shell's startup files or replace the tuon program.
- Logging in and updating run outside it, in the launcher, because they open a browser or rebuild tuon.
- `tuon sandbox-test` shows the checks passing. `TUON_NO_SANDBOX=1 tuon` turns it off for troubleshooting. Linux and Windows don't have it yet.

## Data on your computer

- Tuon creates its folder (`~/.config/tuon`) and files readable only by you, and fixes the permissions of older files on startup.
- **Encrypted with tuon's key:** your session (`login.enc`), message text and attachments, chat previews, drafts, backup keys (in `tuon.db`), and every key for encrypted chats: this device's private keys and your sessions with contacts (`keys.db`, encrypted page by page). Deleted data is zeroed on disk.
- **Not encrypted:** chat and contact names, folders, times. Keep full-disk encryption on (FileVault, BitLocker) for those.
- Photos opened in your photo viewer go to a temporary folder that's deleted when Tuon starts and quits.
- The log holds warnings only. `TUON_DEBUG=1` logs much more, which can include message details. Use it only for troubleshooting, and delete `tuon.log` after.

## What other people send you

Everything shown in Tuon (names, messages, link titles, reactions, even error text) comes from other people, so Tuon treats it as untrusted:

- **Terminal control codes are removed.** Without this, a message could carry hidden codes that change your window title, write to your clipboard or clear the screen. Text that would display backwards to disguise itself (for example `photo‮gpj.exe`), and invisible characters that make look-alike names, are also cleaned up.
- **Links open only if they're `http` or `https`**, and only when you click them. A link can't launch an app or open a local file. On Windows, links never go through `cmd.exe`.
- **A link card shows the real domain**, not just the title the sender picked. Links that are plain `http`, a bare IP address, look-alike letters (punycode), a `name@` prefix or an odd port need a second click, with the reason shown.
- **Files you save are marked as downloaded** (macOS quarantine, Windows Mark of the Web), so your system warns before running one. Tuon never opens or runs them itself.
- **Copied messages leave the clipboard after a minute**, unless you've copied something else since.
- **Photos load only from Meta's media servers, over https,** with a size limit (25 MB) and a time limit. Images that claim absurd dimensions are refused before they're decoded, so they can't exhaust memory.

## Dependencies and updates

Tuon builds on code from other people (mautrix-meta, whatsmeow and about 140 other modules). That code runs with your rights, so:

- **Every dependency is pinned by checksum** (`go.sum`, checked against Go's public checksum database). A published version can't be swapped silently.
- **`tuon update` installs only signed releases.** It takes the newest version tag signed with the maintainer's key (built into tuon) and skips, by name, any tag that isn't. Someone who takes over the GitHub repo or account can push code, but can't sign it.
- **It shows what changed before building.** If an update changes dependencies, it lists them and needs you to type `yes`. A checksum change without a matching dependency change is refused. Modules are re-verified (`go mod verify`) before the build.
- **Dependency updates are reviewed by hand.** [DEPENDENCIES.md](DEPENDENCIES.md) lists the modules that touch the network, your session or encryption, with the version last reviewed, and `./scripts/deps-review.sh` flags changes to them.
- **Builds are reproducible.** The same tag built with the same Go version gives the same file, so anyone can compare against the published `SHA256SUMS`.
- **Checks before every release:** `./scripts/check.sh` verifies checksums, builds, tests and scans for known vulnerabilities (govulncheck). Tuon requires Go 1.27.3, which fixes a certificate-checking flaw in 1.27.2.
- **The repository** requires signed commits and pull requests on `master`, with no bypass.

## If your laptop is stolen, or you think your session leaked

1. **On your phone, log tuon out:** in Messenger's Accounts Centre (Password and security, Where you're logged in), remove the session tuon used. It may show as a browser, "Chrome" or a Mac.
2. **Remove tuon's linked device** for encrypted chats in Messenger's security settings (End-to-end encrypted chats; the menu names change between app versions).
3. **Change your Facebook password** and turn on two-factor authentication if it isn't on.
4. **If you still have the computer,** run `tuon logout --wipe`.

Without your Touch ID or passphrase, whoever has the laptop can't open tuon's session, messages or keys: they're encrypted.

Keep FileVault (macOS) or BitLocker (Windows) on: it's what protects tuon's data when the laptop is off or locked. Tuon warns at the top of the screen if FileVault is off.

## Limits you should know

- **Meta's terms.** Tuon uses Messenger the way Beeper does, through an unofficial route (mautrix-meta). Meta could ask you to verify your account or lock it.
- **Linked device.** For encrypted chats, tuon registers as one of your devices. You can see and remove it in Messenger's security settings. If one contact's messages keep failing to decrypt (their phone kept outdated keys for tuon), `tuon relink` registers a fresh device; those messages still arrive meanwhile through your backup.
- **Your computer.** The lock and encryption stop other apps and anyone holding a copy of your files from reading tuon's data. They can't stop malware already running as you while tuon is unlocked: it could, given time, watch your screen and keyboard. Keep your computer clean and up to date.

## Use at your own risk

Tuon is a personal project, provided as is with no warranty (see the [license](LICENSE)). There's no security support or response process. You're responsible for your account and the computer you run it on.
