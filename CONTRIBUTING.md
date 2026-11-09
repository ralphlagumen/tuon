# Contributing to tuon

Thanks for helping. This page covers how to set up, how the code is laid out, how to test without bothering anyone on Messenger, and what makes a change ready.

## What tuon is for

Tuon is a calm, chat-only client: your chats, sorted into folders, and nothing else. Every change should keep it that way.

- **Chats only.** No feed, stories, reels, suggestions or badges that pull you back in.
- **Readable first.** Clear contrast and plain words. Nothing should need squinting.
- **Keyboard first, mouse welcome.** Every action has a key. Clicking and scrolling just work.
- **Quiet.** Sound and notifications stay minimal and easy to turn off.

If a feature makes Tuon louder or busier, say why it's worth it in the pull request.

## Set up

You need macOS, Go 1.27+ and Homebrew.

```sh
git clone git@github.com:ralphlagumen/tuon.git
cd tuon
./install.sh          # builds tuon and installs Ghostty if missing
tuon                  # run it inside Ghostty for real photos
```

The first run asks for your messenger.com cookies (see the README). Use your own account. If you can, use a **test account**, because Tuon works around Meta's terms (see [Risks](#risks)).

## Layout

```
cmd/tuon/                        the small launcher: unlock, login browser, update (no Messenger code)
cmd/tuon-app/                    the chat app: login prompt, setup, history, the UI
cmd/spike/                       first proof-of-concept CLI (reference only)
internal/connector/              what any chat platform must provide
internal/connector/messenger/    Messenger via messagix (+ whatsmeow for encrypted chats)
internal/store/                  SQLite: chats, folders, messages, reactions, people
internal/ui/                     Bubble Tea UI: views, keys, images, styles
```

How data moves: the **connector** turns Messenger traffic into plain events (chats, messages, reactions, people). The **UI** writes them to the **store** and redraws from it. The UI never talks to messagix directly.

## Adding a platform (Slack, Google Chat, …)

Implement `connector.Connector` in a new package under `internal/connector/`:
- Map the platform's chats, messages and reactions onto the `connector` types.
- Emit events on `Events()`. Don't touch the store or UI from the connector.
- Prefer official APIs with user tokens where the platform has them.

## Make a change

1. Branch from `main`: `git switch -c short-description`.
2. Keep the change focused. Unrelated fixes go in their own branch.
3. Format and check:
   ```sh
   gofmt -w . && go vet ./... && go test ./...
   ```
4. **Try it for real** (below). Passing tests alone don't make a change ready.
5. Open a pull request with:
   - what changed and why, in plain words
   - how you checked it (what you ran and what you saw)
   - a screenshot for anything visible on screen

### Commit messages

Short summary line in the present tense (`Add folder reordering`), a blank line, then what and why if it isn't obvious. One logical change per commit.

## Testing

| Command | What it does | Touches Messenger? |
|---|---|---|
| `go test ./...` | Unit and UI tests with a fake connector | No |
| `TUON_LIVE=1 go test ./internal/ui -run TestLive -v` | Syncs your real account and prints the screens | Reads only |
| `TUON_LIVE=1 go test ./internal/connector/messenger -run TestLiveThreadTypes -v` | Counts what kinds of chats Messenger sends | Reads only |
| `TUON_PREVIEW=bin/preview go test ./internal/ui -run 'TestScreenshot\|TestPreview'` | Renders the UI and photos to PNG in `bin/preview` | No |

UI tests drive the real `Model` with key presses, then check the text on screen. When you change how something looks, update or add a test that checks what a person would see, not internal fields.

### Sending during tests

Sends are real messages from a real account.
- Only send to a chat you set up for testing, like a group with just you, or your own test account.
- Never send test messages to other people's chats.
- Automated tests must not send. Live tests are read-only by design, so keep them that way.

### Checking the UI by hand

- Try both Ghostty (real photos) and Terminal.app (photo tags and blocky photos).
- Try a narrow window (under 72 columns shows one pane) and a very wide one.
- Watch for rows that shift or repeat while you move around. That means a width bug. `ctrl+L` redraws.

## Security and privacy

Never commit, paste or log:
- cookie values, or anything in `~/.config/tuon/`
- chat contents, names, user or chat IDs from a real account (use made-up data in tests)
- screenshots of real chats in public issues. Blur them first.

`.gitignore` already covers `cookies*.txt`, `*.db`, logs, `bin/` and assistant files, so don't force-add them. If you commit a secret by accident, tell the maintainer and rotate it by logging out of Messenger on that session.

## Risks

- **Meta's terms.** Messenger has no official API for personal accounts. Tuon talks to Messenger the way Beeper does, which breaks Meta's terms. Accounts can be asked to verify or get locked. Test on an account you can afford to lose.
- **Licensing.** Tuon is AGPL-3.0, to match mautrix-meta. Contributions are accepted under the same license. Anyone given a Tuon build must be able to get its source.

## Questions

Open an issue, or start a draft pull request early and ask there. Showing a rough version is better than guessing.

## Before you push

Run `./scripts/check.sh`: module checksums, build, vet, tests and known vulnerabilities. There's no hosted CI, so this is the check.
