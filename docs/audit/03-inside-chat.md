# 03 — Inside a chat

_Audited 2026-11-05 from code only. No live sends this pass (the test thread still isn't agreed)._

## 1. Reference
README "Use it → Inside a chat": `enter` send · `alt+enter`/`ctrl+j` new line · `@` tag, `tab` picks · `@~/` attach with
path completion · `ctrl+v` paste image or Finder file · `ctrl+o` newest photo · `↑` on empty box steps into messages ·
`esc` back, keeping the draft. Selected message: `enter` reply · `r` then `1`–`6` react (same key removes) · `o` open
its photo or link · `y` copy. Files show as `[Image #1]`/`[File #2]` tokens; deleting the token drops the file.
"sending…" right away, "not sent" after 45 s. Mouse: drag over messages copies.

## 2. Ours today
- Compose keys `internal/ui/app.go:787-880`; message box `internal/ui/box.go:21-31` (`CharLimit 4000`, newline keys).
- Select keys `app.go` `onSelectKey`; react `onReactKey`; send `internal/ui/compose.go:209` `sendAll`.
- Pending label `internal/ui/views.go:611-613`. Connector `Send` `messenger.go:671`, `React` `messenger.go:772`.

## 3. Reuse check
- Bubbles `textarea` is used for the box (good). Nothing hand-rolled that a library covers.

## 4. Flow (✔ in code unless tagged)
- Type → `enter` → draft cleared → `sendAll`: files first, then text → connector shows a pending copy, replaced on confirm.
- `↑` on empty box → select newest → `enter` sets reply → back to box. `esc` cancels a reply before leaving.
- Every README key in this area has a handler. `shift+enter` also adds a line (not in README).

## 5. What backs it
- Drafts live in `m.drafts` / `m.draftAtts` (memory only). Pending messages are rows with `pending = 1`.

## 6. Gaps

| ID | Gap | Sev | Tag |
|---|---|---|---|
| G1 | A failed file send loses the text. `enter` clears the box and draft first (`app.go:~847`), then `sendAll` returns on the first file error (`compose.go:219`) before the text is ever sent or shown as pending. The typed message is gone. | P1 | ✔ |
| G2 | "not sent" never shows on an idle screen. It's drawn only when `time.Since(msg.Time) > 45s` at redraw time (`views.go:611`), and nothing schedules a redraw for pending messages (the only timers are toast, search and backup refill). The row stays "sending…" until some other event redraws. | P1 | ✔ code · ⚠ live |
| G3 | A "not sent" message has no way out: no retry, no delete. It stays in the chat (`pending = 1`) for good. | P1 | ✔ |
| G4 | Pasting more than 4,000 characters is cut off silently (`box.go:25` `CharLimit = 4000`). Messenger takes far longer messages. | P2 | ✔ code · ⚠ Messenger's real limit |
| G5 | Drafts are memory only. `esc` keeps them (as README says), but quitting tuon loses every draft. | P2 | ✔ |
| G6 | `o` on a selected message with no photo or link opens the chat's newest photo instead (`onSelectKey` `o`). README says `o` opens *its* photo or link. | P2 | ✔ |
| G7 | `r` works on a pending message (no `Pending` check, unlike `enter`), so the reaction goes to a temporary ID Messenger doesn't know. | P2 | ✔ code · ⚠ live error text |
| G8 | Replying with only files drops the reply (`reply` is set only when `val != ""`). | P2 | ✔ |
| G9 | `Send` and `React` run with the app's context and no timeout (`compose.go:225`, `onReactKey`). If the encrypted connection stalls, the command can hang with no message. | P2 | ⚠ depends on `readyWA` limits |

**Lens contradictions:** coverage says "not sent after 45 seconds" is shipped (the label exists); the wiring lens says it
won't appear on an idle screen (G2). Counted as a finding.

**Not checked this pass:** live send/react/reply to a test thread; `@` mention delivery on encrypted vs plain chats;
clipboard paths on Linux/Windows; Terminal.app `option+enter` without "Use Option as Meta key".
