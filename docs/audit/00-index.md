# Audit index

**Reference:** what tuon promises in [README.md](../../README.md) and [SECURITY.md](../../SECURITY.md) (no separate
spec exists; `docs/plans/ROADMAP.md` is still the empty template). Profile: development — the real thing is
`bin/tuon` against Ralph's real account.

**Lenses per batch:** coverage vs README/SECURITY · wiring (reachable end to end) · security · flow/UX.
Tags: `✔` read in code this pass · `⚠` lead, still to confirm (usually needs a live run).

## Batches

| # | Area | Status |
|---|---|---|
| 01 | [Getting in](01-getting-in.md): install, setup, login ×3, session storage, expiry, logout, `tuon history` | open, audited 2026-11-05 |
| 02 | [Chat list & folders](02-chat-list.md): folders, Unread, `/` search + message a person, `t` move, `f` manage | open, audited 2026-11-05 |
| 03 | Inside a chat: send, newline, `@` mentions, `@~/` attach, `ctrl+v`, reply, react, `o`, `y`, sending…/not sent | not audited |
| 04 | Media & photos: kitty placeholders, `View photo`, gallery, cache trim, viewer temp folder | not audited |
| 05 | Quiet & commands: chime + `:sound`, `:mute`, `:images off`, no read receipts, `?` help | not audited |
| 06 | [Sync & data](06-sync-data.md): E2EE 1:1 merge, history fill, 1,000-message prune, dedupe, older-page loading | open: early P0 logged, rest not audited |
| 07 | Untrusted content (SECURITY.md): control-code stripping, bidi, link schemes, photo host/size limits | not audited |
| 08 | Claims & platforms: memory/CPU figures, README screenshots, Linux/Windows paths | not audited |

## Real surface (units to cover)

- **Commands** (`cmd/tuon/main.go:30`): `tuon`, `setup`, `login`, `history`, `logout [--wipe]`, `help`, `--here`
- **Packages:** `cmd/tuon`, `internal/auth`, `internal/connector{,/messenger}`, `internal/store`, `internal/ui`
- **UI keys:** listed in README "Use it" — enumerate against `internal/ui/app.go` in batches 02–05
- **Files on disk:** keychain item `tuon`, `login.json`, `tuon.db`, `tuon.log`, `tuon.lock`, `cache/img/`, `$TMPDIR/tuon`
