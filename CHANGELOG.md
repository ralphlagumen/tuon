# Changelog

All notable changes to tuon. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.4.0] - 2026-11-09

**Upgrading from 0.3.0:** run `tuon update` twice. The first run installs the new launcher; the second adds the chat
app (`tuon-app`) next to it.

### Added
- Read receipts: a chat you open in tuon is marked read on Messenger, so your phone and the web show it read (people
  see "Seen"). `:seen off` (`:r off`) turns it off.
- `esc` `esc` clears the message box. One `esc` with text in the box only warns; `tab` goes back to the chats keeping
  the draft (`esc` still does on an empty box).
- `tuon version` shows which release you have (`dev` for a build that isn't a release tag).

### Changed
- The login window keeps its own browser profile (in `~/Library/Caches/tuon`) instead of a fresh one each time, so
  Facebook recognizes the device and a password manager extension you add there stays. Still logged in there, the
  window opens and closes by itself. tuon only takes the session once Messenger has opened in the window (never mid
  2-factor), and `tuon logout` deletes the profile.
- `tuon login` opens tuon when it's done.
- First run asks for Touch ID (or a new passphrase) once: the launcher sets up the lock and hands the key over.
- New logo: an open enso ring shaped like a chat bubble, with a dot at its centre; the header reads `tuʘn`.
- Releases, `install.sh` and `tuon update` build with the same settings, so an updated tuon matches the published
  `SHA256SUMS`.

### Fixed
- Login loop: right after logging in, tuon said "Messenger logged you out" again. Facebook now redirects
  `www.facebook.com/messages` to `web.facebook.com`, and the login cookies were dropped on that redirect.
- Scrolling fast, or pressing `esc` while a scroll still coasts, no longer types `[<65;…M` into the message box.
- Sending a file Messenger refuses (such as a .zip in a chat that isn't end-to-end encrypted) says so, instead of
  "Messenger didn't return a file ID".
- `tuon history` no longer prints "zerolog: could not write event … file already closed".
- `tuon logout` works inside the sandbox.

### Security
- tuon is now two programs. `tuon` is a small launcher (unlock, login browser, update) with none of the Messenger,
  encryption or database libraries; `tuon-app` holds them and on macOS always runs in the sandbox, so their code never
  starts outside it. Login, setup, history and relink run inside the sandbox too.
- The sandboxed app can no longer start `open` or `osascript`: both could ask macOS to start a program outside the
  sandbox (shown with a probe app). The launcher opens links, photos (in Preview only) and saved files (shown in
  Finder) and reads pasted images and files for it, checking each request.
- `tuon update` and the login browser work in `~/Library/Caches/tuon`, which the sandboxed app can neither read nor
  write; the build tools' temp files go there too.
- The chat app runs on a terminal of its own, which the launcher relays to yours, so it can't type into your shell
  after tuon quits (TIOCSTI). When it exits, the launcher stops anything it left running in its session.
- `tuon sandbox-test` checks all of the above (33 checks), including escape probes through `open` and `osascript`.
- Smaller hardening: bounded Argon2 settings in `vault.json`, no lock file through links, no switching the sandbox off
  through an inherited environment variable (and tuon always says when it runs unsandboxed), an allowlist for the
  launcher's dependencies, and a build made from source asks before `tuon update` installs a release.
- brotli 1.2.4 → 1.2.7 (GO-2026-6888) and klauspost/compress 1.20.0 → 1.20.2 (GO-2026-6865).
- Known gap: a copy of the app that starts its own session can outlive tuon (still sandboxed); see SECURITY.md.

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

[Unreleased]: https://github.com/ralphlagumen/tuon/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/ralphlagumen/tuon/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/ralphlagumen/tuon/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ralphlagumen/tuon/releases/tag/v0.2.0
