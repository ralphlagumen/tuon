# 01 — Getting in

_Audited 2026-11-05 from code only. No live login, logout or PIN restore was run this pass (they touch Ralph's real
session and E2EE keys), so every runtime behaviour below is either read from code (`✔`) or a lead (`⚠`)._

## 1. Reference
- README "Install", "Log in", Known gaps bullet 1; SECURITY.md "Your login".
- Promises: `install.sh` builds, installs Ghostty, runs `tuon setup`. Three logins (browser recommended, password + 2FA,
  cookie paste). Password and PIN never stored; session in system keychain, else owner-only file. Browser login uses a
  throwaway profile, deleted after. On logout by Meta, tuon asks you to log in again. `logout` drops the session,
  `--wipe` also deletes chats, folders, keys. Setup asks for the 6-digit PIN; skip and run `tuon history` later, with
  tuon closed.

## 2. Ours today
- Dispatch: `cmd/tuon/main.go:30-61`. Expiry loop: `main.go:63-76`. Session re-saved on quit: `main.go:170`.
- Login menu: `cmd/tuon/setup.go:53-92`; browser `internal/auth/browser.go:63`; password
  `internal/connector/messenger/login.go:33`; cookie paste `setup.go:133`.
- Storage: `internal/auth/auth.go:44-111` (keychain via go-keyring v0.2.8, which writes through `security -i` stdin, so
  the session never shows in `ps` ✔).
- History: `cmd/tuon/history.go:24-112`, `internal/connector/messenger/history.go:79-140`.
- Logout: `setup.go:168-193`. Lock: `internal/store/lock_unix.go:14`.

## 3. Reuse check
- `history.go:115` `keep()` re-implements the UI's event-to-store handling (`internal/ui/app.go:385` area) for the
  minutes `tuon history` runs. Two copies can drift (e.g. the 1:1 merge rules). P2 ⚠ — confirm what app.go does
  that `keep` skips.
- Keychain, devtools websocket, terminal password read: all library-backed, nothing hand-rolled worth replacing.

## 4. Flow
1. `./install.sh` → build → Ghostty → `tuon setup` (tty only) → `login()` → `history()` → PIN or skip. ✔
2. `tuon` first run with no session → `loadLogin` → `setup()` → open. ✔ (`setup.go:46`)
3. Session dies at start or mid-run → "Messenger logged you out" → `login()` → reopen. ✔ README undersells this
   (it says "on its next start"; it actually re-prompts right away).
4. `tuon login` while tuon is open → see G1. Breaks.
5. `tuon login` with a different account → see G2. Breaks.

## 5. What backs it
- Session = cookies; validity check is only `c_user` + `xs` present (`auth.go:32`).
- E2EE device = first row in whatsmeow's store inside `tuon.db` (`messenger.go:427`), not tied to an account.
- PIN goes to `RecoverPIN`, local var cleared after (`history.go:96`). Backup keys stored in `tuon_history_device`.

## 6. Gaps

| ID | Gap | Sev | Tag |
|---|---|---|---|
| G1 | `tuon login` takes no lock (`setup.go:53`). With tuon open, it saves the new session, then the open tuon overwrites it on quit with its own cookies (`main.go:170`). The new login is silently lost. History then fails with "already running", only as a side note. | P1 | ✔ code · ⚠ live |
| G2 | Setup tells you "`tuon login` switches accounts" (`history.go:145`), but `tuon.db` keeps the old account's chats, folders and E2EE device (`GetFirstDevice`, `messenger.go:427`). Account B would run on account A's encrypted device and see A's chats. **Decided 2026-11-05 (Ralph): reset.** When the new login's `c_user` differs from the stored account, wipe the old account's local data (chats, folders, E2EE device, history keys) before opening, and say so. | P1 | ✔ code · ⚠ live effect |
| G3 | Browser login accepts the session as soon as `c_user` and `xs` exist (`browser.go:117`). If Facebook sets them before the 2FA/checkpoint step finishes, tuon closes the window mid-2FA and saves a session that fails on start. | P1 | ⚠ needs one live browser login with 2FA |
| G4 | Password login: a "approve on your phone" step `continue`s straight back into `DoLoginSteps` (`login.go:55`). If that call doesn't block while waiting, 25 loops pass in seconds and it fails with "too many steps". | P2 | ⚠ check messagix `DoLoginSteps` |
| G5 | `logout --wipe` deletes the local E2EE keys but never removes the linked device from Meta. Contacts keep encrypting to a dead device until Ralph removes it by hand. README mentions manual removal only under "Linked device", not in the logout text. | P2 | ✔ code (no unregister call) |
| G6 | `install.sh:25` says "Photos will use pixel blocks" when Homebrew is missing. Half-block photos were dropped; README says other terminals get a `View photo` button. | P2 | ✔ |
| G7 | `browser.go:75-76` comment says the window shows `https://www.messenger.com`; it opens facebook.com (`browser.go:27`). | P2 | ✔ |
| G8 | Arc is in the browser list (`browser.go:34`). Unclear that Arc honours `--user-data-dir` and remote debugging; if not, the "throwaway profile" promise doesn't hold for Arc users. | P2 | ⚠ |
| G9 | README "Log in" says tuon asks to log in again "on its next start"; it re-prompts immediately (`main.go:71`). Doc understates; fix wording. | P2 | ✔ |
| G10 | `keep()` duplicates UI event handling (see §3). | P2 | ⚠ |

**Contradictions between lenses:** the coverage lens marks "`tuon login` switches accounts" as shipped (the text is
there, the command runs); the wiring lens says it can't work (G2). Same for "log in again" (G1). Both count as
findings, not as covered.

**Not checked this pass:** Windows/Linux keychain paths and `lock_windows.go`; live runs of all three logins; the
Meta "new login" notice; behaviour when the keychain prompt is denied on macOS.
