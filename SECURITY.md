# Security

Tuon logs in to Messenger as you, keeps your encryption keys, and shows text and photos other people send. This page covers how it protects those and what it can't protect.

## Your login

- **Your password is never stored.**
  - With the browser login, you type it into Facebook's own login page, in a normal browser window where you can check the address.
  - With the password login, it goes straight to Meta and is then dropped.
- **Only the session is kept, encrypted.** It's the cookies Meta gives back after you log in, stored in `login.enc` under tuon's own key (see below). It is no longer kept in the keychain, where any program running as you could read it without a prompt; reading it now takes your Touch ID or passphrase. A program running as you could still ask for that unlock itself (see "Limits").
- **The browser login uses its own profile.** It never reads your normal browser's cookies. The login window keeps a profile of its own in `~/Library/Caches/tuon` (so Facebook knows the device, and a password manager extension you add stays), which the sandboxed app can neither read nor write; it holds a Facebook login, encrypted by Chrome with your keychain, and `tuon logout` deletes it. Tuon talks to that window over a private pipe, not a network port, so no other program on your computer can connect to it and read its cookies.
- **Your Messenger PIN is never stored.** `tuon history` uses it once to join Messenger's encrypted backup. The backup keys it gets back are stored encrypted.
- **You can remove it.** `tuon logout` deletes the session and the login window's browser profile. `tuon logout --wipe` also deletes all local data.

## The lock and tuon's key

- **Tuon has its own key, and opening tuon asks for Touch ID or your Mac password.** The key is wrapped by a key inside your Mac's Secure Enclave, which never leaves the chip; only a small helper (`tuon-keyhelper`, built from source by `install.sh`) asks the chip to use it. On Linux and Windows, or a Mac without a Secure Enclave, a passphrase (stretched with Argon2id) protects the key instead. Tuon waits longer after each wrong try, but only within one start: against a copied `vault.json`, only a strong passphrase helps.
- **You unlock once per start.** A small launcher asks for Touch ID and hands the key to the chat app over a private pipe, never through a file or an environment variable.
- **Tuon locks itself after 10 minutes without input**, or when you type `:lock`. The screen is cleared until you unlock.
- **`:previews off`** hides message text in the chat list and while browsing, for when someone can see your screen.
- **If you lose the passphrase** (or the Mac's Secure Enclave key, e.g. after resetting the Mac), the data can't be opened: log in again and restore older chats with your Messenger PIN.

## The sandbox (macOS)

tuon is two programs. `tuon`, the command you type, is a small launcher: it unlocks, opens the login browser, installs updates and starts `tuon-app`. `tuon-app` holds everything else (Messenger, encryption, the database and their libraries) and on macOS always runs inside a sandbox that limits what it can touch. The launcher contains none of those libraries, so their code doesn't start outside the sandbox. The sandbox still has known gaps (the last point below), so treat it as a strong safety net, not yet a sealed fence:

- **It can read only a few folders in your home:** tuon's own folder, its program folder, and Downloads, Desktop, Documents and Pictures (so `@~/` attachments work there). Everything else is refused: your projects, dotfiles, `~/.config`, SSH and cloud keys, keychains, browser profiles, password managers, other apps' data. To attach a file from anywhere else, press `ctrl+f`: the launcher shows the macOS Open dialog and hands over only the file you pick. A file you copy in Finder and paste is read by the launcher too.
- **It can only write** to its own folder and its own temp folder (not the temp folder's parent, which holds other apps' caches). Files you save are moved into Downloads by the launcher, which marks them as downloaded from the internet, so macOS warns before one runs; the app can't drop an unmarked file there. It can't write `~/Library/Caches/tuon`, where the launcher builds updates and runs the login browser.
- **The launcher doesn't trust what the app can write.** The app holds tuon's key while it runs and can write tuon's folder, so it could rewrite `vault.json` (for example as a passphrase vault with a passphrase of its choosing, which would open your data without Touch ID). The launcher keeps a fingerprint of `vault.json` in `~/Library/Application Support/tuon`, out of the app's reach, and refuses to unlock a vault that changed behind its back. Its own logs there never follow links.
- **It can only start a few programs:** the sound player, the text clipboard tools (`pbcopy`, `pbpaste`), the FileVault check, its Touch ID helper and another copy of itself. Not `open` or `osascript`: both can ask macOS to start any program, and what macOS starts runs outside the sandbox (`tuon sandbox-test` tries both and expects a refusal). Shells and anything else are refused, and it can't send Apple Events to other apps.
- **The launcher opens things for it, after checking:** web links (http and https only), photos (only image files from tuon's photo folder, and only in Preview, never by their default app), saved files (only shown in Finder, never opened), and pasted images and files from the clipboard.
- **It can only reach Meta.** The launcher runs a small gate (a proxy on localhost) and the sandbox refuses every other connection: other servers, bare IP addresses, local services, even DNS lookups (a lookup can carry data out in the name). The gate lets through only Meta's servers (facebook.com, messenger.com, fbcdn.net, fbsbx.com, whatsapp.net and a few more), on https. What it refuses is written to `~/Library/Caches/tuon/net.log`, where the app can't write, shown in the header while tuon runs, and repeated when tuon quits.
- **Interception raises an alarm.** Meta's servers all present certificates from one authority (DigiCert). The first time each run that the gate lets tuon reach a Meta host, it makes its own connection there and checks which authority vouches for it. If your Mac trusts the certificate but it comes from someone else (a corporate proxy, a VPN or security tool that inspects traffic, or a certificate authority malware installed), the header warns, `net.log` records it and tuon says so when it quits. It warns rather than blocks, so whatever is in between still sees that connection until you act. Encrypted chats have a second check of their own: the server must prove a key built into whatsmeow, so an interceptor can't read them even with a trusted certificate.
- **Unusual uploads raise an alarm.** tuon's whole history and keys are a few MB, so a theft through Meta itself (sent to someone's account) is small; what gives it away is timing. The launcher knows when you last typed, clicked or scrolled. If tuon uploads more than 1 MB within 10 minutes while you've been away for 5+ minutes, or more than 200 MB in one run, the header warns, `net.log` records it, and tuon says so when it quits. Every run's upload and download totals go to `net.log`. It can't block a few spam messages sent as you, only notice bulk or quiet uploads.
- **It gets only the environment it needs** (terminal type, language, home, temp, tuon's own settings). Your SSH agent socket, API tokens and other tools' variables stay with the launcher. It also can't connect to Unix sockets (the SSH agent, Docker, other local tools), except the one macOS uses for name lookups.
- **Everything that talks to Messenger runs inside it,** including `tuon setup`, `tuon login`, `tuon history` and `tuon relink`. Only the launcher's own jobs run outside: the Touch ID unlock, the login browser window (a browser can't run in the sandbox), and `tuon update`, which has to replace tuon's files. The chat app can't start the launcher directly.
- **Its own terminal.** The app runs on a terminal of its own that the launcher relays to yours, so it can't type into your shell after tuon quits (the TIOCSTI trick); when it exits, anything it left running in its session is stopped.
- **Escape codes are filtered on the way to your terminal.** Your terminal runs outside the sandbox, so the launcher drops codes that would reach past it: clickable links (a click opens any kind of URL), window titles and desktop notifications (spoofing), iTerm file downloads, kitty images read from a file on disk, clipboard reads, and DCS strings that can tunnel other codes. Ordinary drawing codes pass, plus the few tuon uses (color queries, clipboard writes, inline kitty images). The count of dropped codes goes to `net.log`.
- **Known gap:** a copy of the app that starts its own session can keep running after tuon quits (still sandboxed, with no terminal).
- `tuon sandbox-test` shows the checks passing. `TUON_NO_SANDBOX=1 tuon` turns it off for troubleshooting. Windows doesn't have it yet.

## The sandbox (Linux)

The same two programs and the same limits, built from what the kernel offers: Landlock and a seccomp filter. The launcher sets them in a small step of its own just before it starts `tuon-app`, so they hold before any library's code runs, and nothing the app does can lift them.

- **Files:** the same reach as on macOS. It reads system files, tuon's folder, its program folder and Downloads, Desktop, Documents and Pictures; nothing else in your home (`~/.ssh`, `~/.gnupg`, browser profiles, dotfiles). It writes only tuon's folder and a temp folder of its own (`$XDG_RUNTIME_DIR/tuon-tmp`, or `/tmp/tuon-<uid>`), not the shared `/tmp`.
- **Programs:** it can start none, not even the launcher next to it. The launcher opens links (`xdg-open`), photos and folders, reads and writes the clipboard (`wl-clipboard`, `xclip` or `xsel`), shows the file picker (`zenity` or `kdialog`) and plays sounds for it, after checking each request as on macOS.
- **Network:** TCP only, and only to the launcher's gate (Meta, as on macOS). No UDP (so no DNS), no Unix sockets (no SSH agent, D-Bus, X11 or Docker), no netlink or raw sockets.
- **The rest of your session:** it can't read other programs' memory or environment in `/proc`, signal the launcher, open your other terminals or type into its terminal (TIOCSTI). It runs on a terminal of its own, and escape codes are filtered on the way to yours, as on macOS.
- **The lock's fingerprint** lives in `~/.local/share/tuon`, out of the app's reach.
- **Older kernels:** Landlock needs Linux 5.13; its network rules need 6.7. With less, tuon starts with what the kernel can enforce and says what's missing at the top of the screen. The seccomp filter covers x86-64 and arm64.

## Data on your computer

- Tuon creates its folder (`~/.config/tuon`) and files readable only by you, and fixes the permissions of older files on startup.
- **Encrypted with tuon's key:** your session (`login.enc`); all of `tuon.db`, page by page (chat and contact names, folders, messages, reactions, times, settings), with message text, attachment details, previews, drafts and backup keys sealed a second time inside it; and every key for encrypted chats (`keys.db`, also page by page). The first start after updating converts an older, unencrypted `tuon.db`. Deleted database rows are overwritten, and so are the files tuon deletes (session, keys, vault, temp photos and files); on SSDs and APFS nothing is guaranteed gone, so FileVault is what covers old copies (the unencrypted `tuon.db` an older version left behind included).
- **Not encrypted:** `tuon.log` (warnings: IDs, no message text), the size and dates of tuon's files, and `vault.json` (tuon's key, wrapped: useless without your Touch ID or passphrase). Keep full-disk encryption on (FileVault, BitLocker) as well.
- **Photos shown in the chat** (Ghostty, kitty, WezTerm) go straight to the terminal as data and are never written to disk. Older versions kept a plain photo cache in `cache/img`; tuon deletes it on start.
- Photos opened in your photo viewer go to a temporary folder that's deleted when Tuon starts and quits.
- The log holds warnings only. `TUON_DEBUG=1` logs much more, which can include message details. Use it only for troubleshooting, and delete `tuon.log` after.

## What other people send you

Everything shown in Tuon (names, messages, link titles, reactions, even error text) comes from other people, so Tuon treats it as untrusted:

- **Terminal control codes are removed.** Without this, a message could carry hidden codes that change your window title, write to your clipboard or clear the screen. Text that would display backwards to disguise itself (for example `photo‮gpj.exe`), and invisible characters that make look-alike names, are also cleaned up.
- **Links open only if they're `http` or `https`**, and only when you click them. A link can't launch an app or open a local file. On Windows, links never go through `cmd.exe`.
- **A link card shows the real domain**, not just the title the sender picked. Links that are plain `http`, a bare IP address, look-alike letters (punycode), a `name@` prefix or an odd port need a second click, with the reason shown.
- **Files you save are marked as downloaded** (macOS quarantine, Windows Mark of the Web; on Linux they're saved without permission to run), so your system warns before running one. Tuon never opens or runs them itself.
- **Copied messages leave the clipboard after a minute**, unless you've copied something else since.
- **Photos load only from Meta's media servers, over https,** with a size limit (25 MB) and a time limit. Images that claim absurd dimensions are refused before they're decoded, so they can't exhaust memory.

## Dependencies and updates

Tuon builds on code from other people (mautrix-meta, whatsmeow and about 140 other modules). That code runs with your rights, so:

- **Every dependency is pinned by checksum** (`go.sum`, checked against Go's public checksum database). A published version can't be swapped silently.
- **Update notices:** once a day the launcher (never the sandboxed app) asks GitHub for the release names (`git ls-remote`), so GitHub sees your IP address then. The names aren't trusted: a notice only offers `tuon update`, which installs signed releases alone. `TUON_NO_UPDATE_CHECK=1` turns it off; builds from source never check.
- **`tuon update` installs only signed releases.** It takes the newest version tag signed with the maintainer's key (built into tuon) and skips, by name, any tag that isn't. Someone who takes over the GitHub repo or account can push code, but can't sign it (they could block later updates, not install their own). It downloads a fresh copy into a temporary folder each time, with git's local settings and hooks ignored, so a planted clone or git setting can't steer it, and builds the signed files. It works in `~/Library/Caches/tuon`, which the sandboxed chat app can't write, and keeps the build tools' temp files there too. A tag signed under a different name is skipped too.
- **It shows what changed before building.** If an update changes dependencies, it lists them and needs you to type `yes`. A checksum change without a matching dependency change is refused. Modules are re-verified (`go mod verify`) before the build.
- **Dependency updates are reviewed by hand.** [DEPENDENCIES.md](DEPENDENCIES.md) lists the modules that touch the network, your session or encryption, with the version last reviewed, and `./scripts/deps-review.sh` flags changes to them.
- **Builds are reproducible.** The same tag built with the same Go version gives the same file, so anyone can compare against the published `SHA256SUMS`.
- **Checks before every release:** `./scripts/check.sh` verifies checksums, builds, tests and scans for known vulnerabilities (govulncheck). Tests and govulncheck run dependency code, so on macOS they run in a sandbox of their own (`scripts/devsandbox`): no home folder, SSH agent, Unix sockets or repo writes, and tests get no internet. A test checks that this holds. It also fails if the release signing key is loaded in the SSH agent. Tuon requires Go 1.27.4 (1.27.3 fixed a certificate-checking flaw in 1.27.2; 1.27.4 fixes an HTTP header race, GO-2026-6859).
- **The repository** requires signed commits and linear history on `master`, with no force-push and no bypass.

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
- **Your computer.** The lock and encryption stop anyone holding a copy of your files, or a stolen laptop, from reading tuon's data. They don't stop malware already running as you: it could replace tuon or its Touch ID helper, run the helper itself to ask for your unlock, or watch your screen and keyboard. The helper's Touch ID prompt always reads "unlock tuon and your Messenger chats" (a caller can't change it), and tuon only uses the helper installed next to it. Approve that prompt only right after you started tuon or unlocked it yourself, and keep your computer clean and up to date.
- **Dependencies.** Assume one will turn malicious some day. On macOS and Linux a malicious library in the chat app is held to what the sandbox allows, which is still a lot: your messages, tuon's key and your Facebook session while it runs, sending as you, and reading what's in Downloads, Desktop, Documents and Pictures. It can only send things out through Meta (for example as a message to someone), not to a server of its own; the gate logs anything it tries elsewhere and warns about uploads while you're away. It can't use your SSH agent, other secrets in the environment, local sockets, or the folders listed above. If you suspect a bad version: quit tuon, remove its session and linked device on your phone (see "If your laptop is stolen"), then update. The defense is the review before updating them ([DEPENDENCIES.md](DEPENDENCIES.md)). On Linux and Windows there's no sandbox yet, so the chat app runs with your full access.

## Use at your own risk

Tuon is a personal project, provided as is with no warranty (see the [license](LICENSE)). There's no security support or response process. You're responsible for your account and the computer you run it on.
