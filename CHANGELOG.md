# Changelog

All notable changes to tuon. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed (login)
- The login window keeps its own browser profile (in `~/Library/Caches/tuon`) instead of a fresh one each time, so
  Facebook recognizes the device and a password manager extension you add there stays. tuon only takes the session
  once Messenger has opened in the window (not mid 2-factor). `tuon logout` deletes the profile. Still logged in
  there, the window opens and closes by itself: nothing to type.
- `tuon login` opens tuon when it's done.
- First run asks for Touch ID (or a new passphrase) once: the launcher sets up the lock and hands the key over.

### Fixed
- `tuon history` no longer prints "zerolog: could not write event … file already closed" as it exits.
- `tuon logout` works inside the sandbox again (it stopped at a keychain error before `--wipe`).
- Login loop: right after logging in, tuon said "Messenger logged you out" again. Facebook now redirects
  `www.facebook.com/messages` to `web.facebook.com`, and the login cookies were dropped on that redirect. tuon now
  keeps them on redirects that stay on facebook.com (https only).

### Security
- tuon is now two programs. `tuon` is a small launcher (unlock, login browser, update) with none of the Messenger,
  encryption or database libraries; `tuon-app` holds them and on macOS always runs in the sandbox, so their code no
  longer starts outside it. Login, setup, history and relink now run inside the sandbox too. The sandbox still has
  known gap (a copy of the app that starts its own session can outlive it; see SECURITY.md).
- `tuon update` and the login browser work in `~/Library/Caches/tuon`, which the sandboxed app can't write, so it
  can't change what an update builds or the browser's profile. Go's, git's and swiftc's temp files go there too. `tuon update` from 0.3.0 installs only the launcher; run
  `tuon update` once more to get `tuon-app`. `tuon sandbox-test` also checks that the app can't start the launcher.
- The sandboxed app can no longer start `open` or `osascript`: both could ask macOS to start a program outside the
  sandbox (shown with a probe app; `tuon sandbox-test` now checks both). The launcher opens links, photos (in
  Preview only) and saved files (shown in Finder) and reads pasted images and files for it, checking each request.
- The chat app runs on a terminal of its own, which the launcher relays to yours, so it can't type into your shell
  after tuon quits (TIOCSTI). When it exits, the launcher stops anything it left running in its session.
- Smaller hardening: Argon2 settings in `vault.json` are bounded (a planted file can't crash the launcher); the lock
  file is never opened through a link; an inherited `TUON_SANDBOXED` can't switch the sandbox off, and tuon always
  says when it runs unsandboxed (also when `tuon-app` is started directly); the launcher's dependency test is an
  allowlist; a `tuon update` from a build made from source asks before installing a release.
- brotli 1.2.4 → 1.2.7 (GO-2026-6888) and klauspost/compress 1.20.0 → 1.20.2 (GO-2026-6865): both were reachable
  from Messenger's compressed HTTP responses.

### Changed
- New logo: an open enso ring shaped like a chat bubble, with a dot at its centre.
- The header shows the logo in the name: `tuʘn`.
- Releases, `install.sh` and `tuon update` build with the same settings (`CGO_ENABLED=0`, `-buildvcs=false`), so an
  updated tuon matches the published `SHA256SUMS`.

## [0.3.0] - 2026-11-06

### Added
- Shared links, posts and videos from Messenger now show, live and from the backup. Kinds tuon can't show become a
  note instead of vanishing.
- Colon command shortcuts: `:l` lock, `:s` sound, `:m` mute, `:i` images, `:p` previews, `:h` help.
- Diagnostics: real decrypt errors and unknown backup entries go to `tuon.log` (no message content); live tests can
  check keys, devices and a self-decrypt round trip.

### Changed
- All of `tuon.db` is encrypted page by page (Adiantum, like `keys.db`). An older plain file is converted on first
  start, with row counts checked before the swap.
- Photos go to Ghostty/kitty as data, never as a file on disk. The old plain photo cache is deleted on start.
- Tighter macOS sandbox: only the programs tuon uses may start, no Apple Events, a wider read block list, and writes
  limited to tuon's folder, Downloads and the temp folder.
- `tuon update` fetches the official repo fresh into a temp folder, ignores local git config and hooks, builds exactly
  the signed tree, and skips tags signed under another name.
- The Touch ID helper has a fixed prompt, and tuon only uses the helper next to it (no `PATH` lookup).
- SECURITY.md and README corrected where they claimed more than tuon does.

### Fixed
- Messages from phones that add newer fields no longer fail with "mismatching MAC" (libsignal v0.2.3).
- No passphrase check on a Touch ID vault (it caused a panic).
- Cookies are only taken from exact facebook.com domains.
- A plain session file planted after setup is ignored and removed.
- `logout --wipe` keeps the source-folder pointer that `tuon update` needs.
- Throwaway browser profiles left by an interrupted login are deleted.
- The decrypt warning clears once messages decrypt again.

## [0.2.0] - 2026-11-05

First release.

### Added
- tuon, a calm terminal client for Messenger: chat list, folders, unread view, search, reactions, photos, files and
  sound.
- Browser login in a separate throwaway window, plus email/password and pasted-cookie logins.
- End-to-end encrypted chats, with history restored from the PIN backup.
- Recovery for encrypted messages that fail or skip live delivery: session reset, backup retries, and placeholders
  that give up cleanly.
- Unsent drafts kept per chat, received files saved to Downloads, help screen, login lock.
- Vault behind the Secure Enclave and Touch ID (passphrase on other systems), idle lock and `:lock`.
- Message text, media, previews, drafts and keys encrypted at rest.
- Sandboxed chat app on macOS; logins run in the unsandboxed launcher.
- `:previews off` for shoulder-surfing.
- `tuon update` (signed release tags only) and `tuon relink`.

[Unreleased]: https://github.com/ralphlagumen/tuon/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/ralphlagumen/tuon/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ralphlagumen/tuon/releases/tag/v0.2.0
