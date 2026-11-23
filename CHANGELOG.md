# Changelog

All notable changes to tuon. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- Drag over a message or the draft to select and copy text; double-click takes a word, triple-click a line.
- Group chats show ⁂ after their name; encrypted chats show 🔒 in the list and header.
- `:hush` hides a chat from every view (muted, out of Unread and folders); `:hushed` (`:H`) lists them, `:unhush`
  brings one back. `:delete` removes a chat and its messages from tuon after a second `:delete` to confirm; it
  comes back if they message you.
- `@everyone` in group chats (encrypted groups tag each current member).
- Reactions sit in a pill under the message; picking a message shows who reacted.
- In Terminal.app, ❤ and a few other symbols show as color emoji.
- A notice in every encrypted chat with someone whose security key changed, plus a warning in the header.
- `TUON_KEYTRACE=1` records key input (typed characters masked); `kill -USR1` writes goroutine stacks to `tuon.log`.

### Changed
- No "Seen" while tuon is locked or the terminal window is in the background; unlocking or coming back sends it.
  Placeholders and tuon's own notices are never sent as read.
- Passphrase mode: 6-character minimum; after a wrong passphrase the wait hides and drops what you type.
- Real names over group nicknames when tagging and in who reacted.

### Fixed
- A chat you read on your phone (or the web) now shows as read in tuon too, also with read receipts off on the
  phone. Before, it stayed unread until you opened it in tuon.
- A chat you replied to from tuon no longer comes back unread after you quit and reopen tuon.
- Freeze when scrolling fast; fast scrolling no longer falls seconds behind or types mouse reports into the box,
  also when `esc` is pressed mid-scroll.
- Emoji cursor codes no longer push rows right after truncation.
- People known only by nickname were missing from `@` suggestions.
- `tuon history` missed removals, key changes and 1:1 merges.

### Security
- tuon-app ignores `HTTP(S)_PROXY` / `ALL_PROXY` (the header says when one was ignored); `tuon update` runs git, go
  and swiftc without proxy settings.
- Logout `--wipe`, account switch and relink overwrite the session, `vault.json`, old photo cache and login profile
  with zeros before deleting them. Encrypting an old plain `tuon.db` zeroes the plain file too.
- Arc dropped from the login browsers.
- Go 1.27.4 (GO-2026-6859, net/http header race).
- The sandboxed app gets only the environment it needs: no SSH agent socket, tokens or other tools' variables. It
  can't connect to Unix sockets (SSH agent, Docker) either. `tuon sandbox-test` checks both (37 checks).
- `./scripts/check.sh` runs tests and govulncheck in a sandbox (`scripts/devsandbox`), since they run dependency
  code: no home folder, SSH agent, sockets or repo writes, and tests get no internet. It fails if the release signing
  key is loaded in the SSH agent.
- The sandboxed app can only reach Meta's servers: the launcher runs a gate (a localhost proxy) that lets through only
  Meta's hosts on https, and the sandbox refuses every other connection, including DNS lookups. Refusals go to
  `~/Library/Caches/tuon/net.log`, the header, and a note when tuon quits. `tuon sandbox-test` checks it (40 checks).
- The sandboxed app can read only tuon's folders and Downloads, Desktop, Documents and Pictures in your home; not
  your projects, dotfiles or `~/.config`. `@~/` completes those folders. `ctrl+f` attaches any other file through the
  macOS Open dialog, run by the launcher, which hands over only the file you pick; a file copied in Finder is read by
  the launcher too. `tuon sandbox-test` checks it (45 checks).
- `./scripts/deps-review.sh` lists every module a bump changes (not only watched ones) with its age, whether tuon
  links it and a diff link, and refuses versions younger than 14 days unless allowed with `--allow mod@ver`.
- Unusual-upload alarm: the launcher's gate counts what tuon uploads to Meta and warns (header, `net.log`, on quit)
  when more than 1 MB goes up within 10 minutes while you've been away from the keyboard for 5+ minutes, or more
  than 200 MB in one run. Each run's upload/download totals are logged, also when the terminal is closed or tuon is
  killed, plus a line every 30 minutes.
- The launcher filters the app's output before your terminal sees it: links (OSC 8), titles, notifications, iTerm
  file downloads, kitty images from files, clipboard reads and DCS strings are dropped; drawing codes and what tuon
  uses pass. Dropped codes are counted in `net.log`.
- Tests that need a pseudo-terminal (the relay tests) and the launcher's dependency check ran as "skipped" inside the
  test sandbox; both run again.
- The launcher treats what the app can write as untrusted: it pins `vault.json` (refuses one rewritten behind its
  back, e.g. downgraded from Touch ID to a passphrase), its key trace never writes through a link, saved files are
  moved into Downloads by the launcher with the downloaded-from-the-internet mark (the app can no longer write
  Downloads), and the app can write only its own temp folder, not other apps' caches next to it.
- Interception alarm: for each Meta host it lets through, the gate checks once per run that the certificate chains to
  DigiCert (Meta's authority); anything else the Mac trusts (a corporate proxy, a malware-installed CA) shows a
  warning in the header, `net.log` and on quit.
- Scan 2026-11-23: govulncheck finds nothing tuon calls. One module-level advisory, GO-2026-5932
  (`golang.org/x/crypto/openpgp`, unmaintained): tuon doesn't use that package; the fix is only in an untagged
  x/crypto build, so tuon waits for the next tagged release.

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
