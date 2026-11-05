# tuon

*Tuon* is Tagalog for "focus".

A calm, chat-only Messenger client for the terminal. You get your chats, sorted into **Work**, **Personal** and **Inbox**. No feed, stories, ads or notifications pulling you back in.

It's a personal tool. See [Before you use it](#before-you-use-it).

## Install

You need macOS, [Go](https://go.dev) and [Homebrew](https://brew.sh).

```sh
./install.sh
```

The script:
- builds `tuon` and installs it to `~/.local/bin` (set `PREFIX` to change that)
- installs [Ghostty](https://ghostty.org) if it's missing. Ghostty is the terminal that shows real photos.

## Run

```sh
tuon
```

Tuon runs right in the terminal you type it in. Use **Ghostty** for real photos. Other terminals, like Terminal.app, work too, but photos there are blocky.

To make Ghostty's edges match Tuon, you can add these to `~/.config/ghostty/config`:

```
background = 1f2329
window-padding-color = background
copy-on-select = clipboard
```

The last line copies text as soon as you select it with shift-drag.

### First run: log in

Tuon logs in as you, using the session cookies from your browser.

1. Log in at [messenger.com](https://www.messenger.com).
2. Open its cookies:
   - Safari: Develop › Show Web Inspector › Storage › Cookies
   - Chrome: DevTools › Application › Cookies
3. When Tuon asks, paste one line:

   ```
   datr=…; c_user=…; xs=…
   ```

The paste stays hidden. The line is saved to `~/.config/tuon/cookies.txt`, readable only by you. If Messenger logs you out later, run `tuon login` and paste a fresh line.

## Using it

Tuon has two panes: your chats on the left and the open chat on the right.

### Chat list

| Key | Does |
|---|---|
| `↑` `↓` | Browse chats. The right pane previews each one without marking it read. |
| `←` `→` or `1`–`9` | Switch folders |
| `enter` | Open the chat, ready to type |
| `t` | Move the chat to a folder |
| `f` | Edit folders: add, rename, delete, reorder |
| `/` | Search chat names in every space |
| `?` | Show all keys |
| `q` | Quit |

### Inside a chat

| Key | Does |
|---|---|
| type, then `enter` | Send |
| `@` | Tag someone. Pick with `↑` `↓`, confirm with `tab`. |
| `@~/`… | Attach a file. Path completes as you type, `tab` picks. |
| `ctrl+v` | Paste an image (or a file copied in Finder) from the clipboard |
| drag a file in | Attaches it. `backspace` on an empty box removes the last file. |
| `↑` on an empty box | Step into the messages |
| `r`, then `1`–`6` | React ❤ 😆 😮 😢 😠 👍. Same key again removes it. |
| `y` | Copy the selected message |
| `o` | View the photo full screen. `O` opens it in Preview, `s` saves it to Downloads. |
| `esc` | Back to the chat list. Your draft is kept. |

The mouse also works: click a chat to open it, scroll either pane with the wheel, and drag over messages to copy them. Shift-drag selects any text the terminal's own way.

Your message shows right away with "sending…", until Messenger confirms it.

### Folders

You start with **Work** and **Personal**. New chats land in **Inbox**. Press `t` once to move a chat to a folder, and it stays there. Press `f` to add, rename, delete or reorder folders. Deleting a folder moves its chats back to Inbox.

### Sound

A soft tick plays when a new message arrives. It doesn't play for muted chats or your own messages. `:sound off` turns it off.

### Commands

Type `:` in the chat list:
- `:mute` mutes or unmutes the chat
- `:images off` hides photo previews (`:images on` turns them back on)
- `:sound off` turns off the new-message tick (`:sound on` brings it back)
- `:quit` quits

## Photos

- **In Ghostty** (also kitty and WezTerm), photos show as real images: thumbnails in the chat, and full size with `o`.
- **In other terminals**, a photo is a one-line `[Photo]` tag. `o` shows a pixelated version, and `O` opens the real file.

To force one mode, set `TUON_IMAGES=kitty` or `TUON_IMAGES=blocks`.

## Where things live

| Path | What |
|---|---|
| `~/.config/tuon/cookies.txt` | Your login (owner-only) |
| `~/.config/tuon/tuon.db` | Chats, spaces, message cache, encryption keys |
| `~/.config/tuon/tuon.log` | Warnings and errors. Set `TUON_DEBUG=1` for more. |
| `~/.config/tuon/cache/img/` | Photo cache |

To start fresh, delete `~/.config/tuon`. You'll need to log in again.

## Before you use it

- **Meta's terms.** Messenger has no official API for personal accounts. Tuon works the way [Beeper](https://www.beeper.com) does: it uses [mautrix-meta](https://github.com/mautrix/meta) to talk to Messenger as your browser would. That breaks Meta's terms. Meta could ask you to verify your account or lock it. Use it on your own account, at your own risk.
- **Encrypted chats.** Tuon registers itself as a linked device, so you may see a "new login" notice. Messages from 1:1 encrypted chats only arrive from that point on. Older history isn't loaded yet.
- **License.** Tuon is AGPL-3.0, the same as mautrix-meta, which it's built on. If you share Tuon, or a changed version, with anyone, you must also offer them its source under the AGPL.

## Known gaps

- Your 1:1 chats only show up after a new message arrives in them. Messenger's web sync lists group chats only. The full 1:1 list and its history need your Messenger PIN to restore the encrypted backup, which isn't built yet.
- Read state isn't sent back to Messenger, so your phone still shows read chats as unread. No "seen" receipts go out either.
- Videos, voice messages and files show as a tag. Open them on your phone.

## Development

```sh
go build -o bin/tuon ./cmd/tuon      # build
go test ./...                        # tests (no network)

# Read-only check against your real account (never sends)
TUON_LIVE=1 go test ./internal/ui -run TestLive -v

# Render the UI and photos to PNG for review, into bin/preview
TUON_PREVIEW=bin/preview go test ./internal/ui -run 'TestScreenshot|TestPreview'
```

Layout:

```
cmd/tuon/                        app entry: login, Ghostty launch, wiring
cmd/spike/                       first proof-of-concept CLI
internal/connector/              what a chat platform must provide
internal/connector/messenger/    Messenger via messagix (+ whatsmeow for E2EE)
internal/store/                  SQLite: chats, spaces, messages, reactions
internal/ui/                     Bubble Tea UI, images, styles
```

Slack and Google Chat are planned as more connectors, behind the same interface.

## License

Copyright (C) 2026 Ralph Lagumen

Tuon is free software under the [GNU Affero General Public License v3.0](LICENSE). It comes with no warranty.
