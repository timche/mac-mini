# show-image

A Claude Code mod that gives every session on this Mac a tool, `mcp__show-image__show_image`, to show Tim an image, since he works from the Herdr app on another machine and a path here is nothing he can open.

Claude Code draws no image in the terminal by itself; only a mod's `<Image>` element does, as kitty graphics. Three things about the path to the Herdr app decide how this one works:

- **Bytes, never a file path.** Herdr refuses kitty's file medium, and a path means nothing on the app's machine. Claude Code holds at most 2 MiB of bytes for one image, so the tool re-encodes with `sips`, shrinking until the PNG fits, and fails if it never does.
- **`CLAUDE_CODE_FORCE_TERMINAL_IMAGES=1`, set in `home/.claude/settings.json`.** hc answers Claude Code's graphics probe in the wrong order (herdr-app#533), so without it Claude Code decides the terminal has no images and every `<Image>` draws its `alt`, here the source path and size. Claude Code reads it at startup, and it is undocumented, so a session that shows only that text after an update is where to look.
- **Drawn under the turn's final reply.** A tool's own row is folded in a tool group and hidden in focus view, which is how Tim works. So a call only queues the picture, and `turn.complete` attaches the queue to the reply row, which means nothing appears until the turn ends.

Each call copies the picture into a `show-image.*` folder of its own under `$TMPDIR`, because the path a session names is often overwritten by its next screenshot. A row whose copy has gone draws just the reply.

## How it fails

hc passes images to the windows attached at that moment and keeps none for a later one, so a picture sent while Tim is on another tab is lost, and the screen shows blank cells where it would be. Anything that must outlast that also goes in an artifact.

A raw image written to a pane's tty from a shell, past Claude Code, draws over the interface and desynchronises its screen; ctrl+L repairs that, and the mod never does it.

## Installing and testing

`home/.claude/settings.json` declares this folder as the `mac-mini` marketplace, by a path through `~`, and turns the plugin on; Claude Code reads it from here, so an edit reaches the next session.

```sh
claude plugin validate tools/show-image
claude plugin test tools/show-image
```
