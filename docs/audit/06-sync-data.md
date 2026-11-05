# 06 — Sync & data

_Batch not fully audited yet. Early finding logged 2026-11-05 from a live report by Ralph._

## Gaps

| ID | Gap | Sev | Tag |
|---|---|---|---|
| G1 | An encrypted message that fails to decrypt live disappears without a trace. 2026-11-05 18:57:29: Raine (`100001279802241`, sender device 90) sent message `7524062326348106228`. whatsmeow logged "no valid sessions" 5×, then "Not sending any more retry receipts". Nothing was stored; the newest row in `tuon_messages` for that chat is 18:52 "game raine?". The chat moved to the top ("1m") with the old snippet, so it looked like nothing arrived. The connector has no `events.UndecryptableMessage` handler (`internal/connector/messenger/messenger.go`, `onWA`), and the PIN-backup fill (`loadNewest`, `internal/ui/app.go:1234`) runs only once per chat per run, so it never goes back for the missed message. Fix: on undecryptable, show a "waiting for this message" placeholder and re-run the backup fill for that chat after a delay (the phone has to upload it first). | P0 | ✔ log + db · **fixed, confirmed live by Ralph 2026-11-05** |
| G2 | Why the Signal session with Raine's device 90 broke is unknown. A session row exists (`whatsmeow_sessions`), but decryption failed even on the sender's prekey retries, which points to our local keys being out of step with the server. Could be left over from the overlapping-run breakage earlier on 2026-11-05. | P1 | ⚠ cause not confirmed |
| G3 | A message can skip the encrypted connection entirely: at 19:02:25 Messenger bumped Raine's chat, but no encrypted message or decrypt error arrived. The backup was the only way to get it, and nothing triggered a fetch. Fix built: an encrypted chat whose activity is newer than its newest stored message gets backup checks at 8 s, 30 s, 1, 3, 10 and 30 min (`internal/ui/app.go` `startRefill`, `store.Behind`). | P0 | ✔ db · fixed, confirmed live by Ralph 2026-11-05 |
| G4 | A message that loaded late from the backup never updated the chat-list snippet, because Messenger had already moved the chat's time forward (Raine's chat kept showing "You: game raine?"). Fixed: the snippet follows the newest stored message (`store.AddMessage`). | P1 | ✔ db · fixed, confirmed live by Ralph 2026-11-05 |
