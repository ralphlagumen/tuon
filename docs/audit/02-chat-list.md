# 02 — Chat list & folders

_Audited 2026-11-05 from code only. No live run this pass._

## 1. Reference
README "What you get" (Folders, Unread tab) and "Use it → Chat list": `↑↓` browse with a preview that doesn't mark
read · `enter` opens · `←→`/`1`–`9` switch folders · `u` Unread · `/` search chats in every folder and people, `enter`
on a person messages them · `t` move chat · `f` add/rename/delete/reorder, `m` picks up · `q` quits · click a chat to
open it · `:` commands (`:mute`, `:sound`, `:images off`, `:quit`).

## 2. Ours today
- Key routing `internal/ui/app.go:604`; list keys `app.go:994-1056`; move `onTagKey` `app.go:1086`; folder manager
  `app.go:1108-1205`; search `app.go:672-725` + `searchTick` `app.go:284`; people `app.go:1758-1812`; mouse
  `app.go:1351`; commands `app.go:917`.
- Store: `Chats` `internal/store/store.go:264` (search ignores folder ✔), `UnreadCounts` `:338`, spaces `:518-620`.

## 3. Reuse check
- Nothing hand-rolled that a library covers. Bubble Tea `list` would replace the row/offset math, but the custom
  rows are deliberate (layout feedback) — no gap.

## 4. Flow (all ✔ in code)
- Browse → `preview()` (`app.go:973`) loads the chat on the right; only `openChat` calls `MarkRead` (`app.go:1220`). Matches README.
- New chats default to `inbox` (`store.go:77`). Unread tab = unread and not muted, any folder (`store.go:268`); it
  shows only while something is unread (`app.go:1081`).
- Search `/` → local name match across all folders, then Messenger people after 350 ms → `enter` on a person →
  `openPerson`.
- Click: list offset math matches render (`views.go:277` vs `app.go:1414`).

## 5. What backs it
- Folders = `tuon_spaces` (name, pos), Inbox is a constant kept last. Chat→folder = `tuon_chats.space`.
- Read state is local only (README known gap); `read_ts` stops old sync data re-marking read chats unread (`store.go:173`).

## 6. Gaps

| ID | Gap | Sev | Tag |
|---|---|---|---|
| G1 | `openPerson` calls `conn.OpenDirect` inside `Update` (`app.go:1797`), with no timeout. A slow network freezes the whole UI until Messenger answers. Should run as a `tea.Cmd`. | P1 | ✔ code · ⚠ live freeze |
| G2 | Leftover commands `:work`, `:personal`, `:inbox` (`app.go:921`) point at fixed folder names. Folders are user-named now, so `:work` lands on the first folder or does nothing. Not in README. Remove, or make `:<folder name>` work for any folder. | P2 | ✔ |
| G3 | `:images blocks` still turns on half-block pixel photos (`app.go:937`). Ralph rejected those. Belongs to batch 04, logged here because it's a command. | P2 | ✔ |
| G4 | Clicking a person in search results does nothing (`app.go:1419` only checks `i < len(m.chats)`). Keyboard `enter` works. README says click opens a chat; people rows look clickable. | P2 | ✔ |
| G5 | Folder tabs aren't clickable (`app.go:1407` ignores rows above the list). README says the mouse works too; tabs are the obvious target. | P2 | ✔ |
| G6 | `RenameSpace` runs two `UPDATE`s with no transaction (`store.go:580-584`). A failure between them leaves chats pointing at a folder that no longer exists, so they'd vanish from every tab but Unread/search. | P2 | ✔ code · low odds |
| G7 | Search treats `%` and `_` in the query as wildcards (`store.go:269`). Searching "a_b" matches more than it should. | P2 | ✔ |
| G8 | `:mute` gives one flash "mute toggled" for both directions (`app.go:929`). It doesn't say which way it went. Muted rows do show "(muted)" (`views.go:318`). | P2 | ✔ |
| G9 | Two orphan `playChime` doc comments above `reloadChats` (`app.go:429-430`). | P2 | ✔ |

**Lens contradictions:** none found. Every README key in this area is wired to a handler that does what it says.

**Not checked this pass:** live run against the real account (folder counts, search people results), how `?` help
lists these keys, Unread tab behaviour when its last chat gets read while you're on it.

## Found live 2026-11-05

| ID | Gap | Sev | Tag |
|---|---|---|---|
| G10 | `/` couldn't find a group not in the local list ("Jayson & Friends"). Messenger's search returned groups, but `SearchPeople` dropped every result that wasn't a 1:1. Fix built: group results show as "Group chat · enter to open" and open as a group (`messenger.go` `SearchPeople`, `app.go` `openPerson`). | P1 | ✔ db (group absent) · fixed, confirmed live by Ralph 2026-11-05 |
| G11 | Only Messenger's first page of chats is loaded (44 chats in `tuon_chats`). Older chats never show in folders until they get a new message. Needs thread paging (`FetchThreadsTask`, as in mautrix-meta `threadbackfill.go`). | P1 | ✔ db count · not built |
| G12 | A group opened from search had no member list, so a sender with no saved contact showed as "…7940" (`views.go` `senderLabel`). Fix built: tuon asks Messenger once per run for unknown sender names (`LookupPeople`, `GetContactsFullTask`). | P2 | ✔ db (no contact, 0 participants) · fix built, live check pending |
