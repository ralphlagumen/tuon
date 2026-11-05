# Security

Tuon logs in to Messenger as you, keeps your encryption keys, and shows text and photos other people send. This page covers how it protects those and what it can't protect.

## Your login

- **Your password is never stored.**
  - With the browser login, you type it into Facebook's own login page, in a normal browser window where you can check the address.
  - With the password login, it goes straight to Meta and is then dropped.
- **Only the session is kept.** It's the cookies Meta gives back after you log in, saved in your system keychain (macOS Keychain, Windows Credential Manager, Linux Secret Service). If no keychain is available, Tuon falls back to a file only you can read.
- **The browser login uses a throwaway profile.** It never reads your normal browser's cookies, and the temporary profile is deleted afterwards. Tuon talks to that window through Chrome's debugging port, which listens only on your computer and closes with the window.
- **Your Messenger PIN is never stored.** `tuon history` uses it once to join Messenger's encrypted backup. The backup keys it gets back are kept in the database with your other encryption keys.
- **You can remove it.** `tuon logout` deletes the session. `tuon logout --wipe` also deletes all local data.

## Data on your computer

- Tuon creates its folder (`~/.config/tuon`) and files readable only by you, and fixes the permissions of older files on startup.
- The database holds your chats, folders, the newest 1,000 messages per chat, and the keys for encrypted chats. It isn't encrypted itself, so turn on full-disk encryption (FileVault on macOS, BitLocker on Windows). Anyone who can read your files can read those messages and keys.
- Photos opened in your photo viewer go to a temporary folder that's deleted when Tuon starts and quits.
- The log holds warnings only. `TUON_DEBUG=1` logs much more, which can include message details. Use it only for troubleshooting, and delete `tuon.log` after.

## What other people send you

Everything shown in Tuon (names, messages, link titles, reactions, even error text) comes from other people, so Tuon treats it as untrusted:

- **Terminal control codes are removed.** Without this, a message could carry hidden codes that change your window title, write to your clipboard or clear the screen. Text that would display backwards to disguise itself (for example `photo‮gpj.exe`) is also cleaned up.
- **Links open only if they're `http` or `https`**, and only when you click them. A link can't launch an app or open a local file. On Windows, links never go through `cmd.exe`.
- **Photos load only from Meta's media servers, over https,** with a size limit (25 MB) and a time limit. Images that claim absurd dimensions are refused before they're decoded, so they can't exhaust memory.

## Dependencies

Tuon is pinned to a Go version with current security fixes, and its dependencies are checked with:

```sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

## Limits you should know

- **Meta's terms.** Tuon uses Messenger the way Beeper does, through an unofficial route (mautrix-meta). Meta could ask you to verify your account or lock it.
- **Linked device.** For encrypted chats, Tuon registers as one of your devices. You can see and remove it in Messenger's security settings.
- **Your computer.** Tuon can't protect your session from malware or someone with access to your user account.

## Use at your own risk

Tuon is a personal project, provided as is with no warranty (see the [license](LICENSE)). There's no security support or response process. You're responsible for your account and the computer you run it on.
