---
name: preview-markdown
description: Open a rendered view of a markdown file in a Herdr pane beside the user, using glow. Use it whenever the user asks to read, view, preview, render, or look over a markdown file (README, CLAUDE.md, a plan, notes, release notes, a doc under ~/docs) in the terminal, and also right after you write or substantially rewrite a markdown file the user is going to read, even if they didn't ask for a preview. Not for markdown you only need to read yourself; use Read for that.
---

# Preview markdown

Render a markdown file for the user with glow in a pane next to this one, so they read it formatted instead of as raw source. The script does the Herdr work; run it rather than issuing pane commands by hand:

```bash
~/.claude/skills/preview-markdown/scripts/preview.sh <file.md> [width]
```

It prints the pane ID on success. Width defaults to 100 columns, which keeps tables readable; pass a smaller one for a narrow pane.

What the script does, so you know what to expect:

- Refuses outside Herdr (exit 2) and prints the command the user can run themselves with the `!` prefix. Relay that command; do not try to run glow in your own Bash tool, since its pager needs a real terminal and only the raw text would come back to you.
- Reuses a pane in the current tab that is already showing glow, sending `q` to the pager first, so calling it again after an edit reloads in place instead of adding another column.
- Otherwise splits the calling pane along its longer axis, keeps focus here, and preserves the working directory.

After it returns, tell the user in one line where the file is showing. Do not read the pane back to check rendering unless the user reports a problem; the wait inside the script already confirms glow started.

## Closing the pane

Close the preview yourself once the reading is over, so the user is not left tidying panes:

```bash
~/.claude/skills/preview-markdown/scripts/preview.sh close
```

The end of a review is a message that approves the file, asks for no more changes, or moves on to a different task. A message asking for another edit to the same file is not the end; apply the edit and rerun the preview so it reloads in place. If in doubt, leave it open: an extra pane costs the user one keypress, a pane closed mid-read costs them a reopen. Outside Herdr the close is a no-op.

## When rendering looks wrong

Escaped bytes such as `<C2><A0>` in the pane mean the shell's `LANG` names a locale that is not generated on the machine, so `less` falls back to ASCII. `locale -a` shows what exists. Fixing it is the user's call: generating the locale needs sudo, or they can export `LANG=C.UTF-8` in their profile. As a one-off, rerun with `LC_ALL=C.UTF-8` in front of the glow command.
