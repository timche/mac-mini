---
name: mochi
description: "How a session posts to Discord through mochi, Tim's Discord bot, and shares work with the team. Load when your opening asks for it with \"Invoke the mochi skill\", or when the user or the project's own instructions ask you to share or announce work with the team, such as \"share with the team\", \"announce this\" or \"post this for the mods\"; never otherwise."
---

# mochi

mochi is Tim's Discord bot for this project's server. When Tim mentions it there, it starts a session for his message, with its standing orders at the top of the brief and a thread of its own to post into; any session in this repository, one mochi did not start included, can share finished work with the team through it. What follows holds for every post, whichever session makes it.

## Writing for the team

Everything posted to Discord is for the team, mostly moderators who are not technical and have no GitHub access, and nobody reading it has seen this session.

Anything you post stands on its own in Discord: plain language, proof only as attached images or video. No links except the pull request --pr adds, the server's allowed domains and the exact addresses it allows, such as `chrome://extensions`, which may be written in backticks for a tester to paste and are not code or configuration (mochi tells you if one is refused); no file paths, branch names, code or jargon, no secrets, and nothing Tim has not seen in this session. No internals: no code, file or function names, file paths, branch names, configuration, infrastructure or jargon. Name none of the tools or services behind the work — not the software running this session, the AI, the terminal, the password manager or the like: it is just "the session". Nothing unreleased or secret that the user has not said may go out, and nobody is mentioned. mochi refuses a link or a path itself.

Every command takes its text on stdin, so a post of several lines needs no quoting. `--file` attaches a file, up to ten, each within what the server takes for one file (10 MB, or 50 or 100 MB on a boosted server); mochi refuses one that is too large, and then say in the post that it is too large to attach. Files have to be inside this session's checkout or its scratch folder, and mochi refuses ones that look like secrets or local settings, as a guard against attaching the wrong file. A secret scan runs over every post first; when it finds something or cannot run, nothing is posted: fix what it names and post again, or tell the user here what it said.

If something posted shouldn't be there, fix it with `mochi edit <link>` (or `mochi delete <link>`) right away and tell the user. `mochi edit` takes the new text the same way as a post and is held to the same rules; `--drop-files` removes the attachments too.

## Sharing work with the team

When the user, or the project's own instructions at a step you have reached, asks you to share what this session did with the team, `mochi announce` posts it to the announce channel of the Discord server for this repository, from any session, whether mochi started it or not. The team reads it to know what changed in the product and to tell players about it.

Write what is new or changed, and what players will notice, in plain language. Use this shape:

```
**<a short headline saying what is new>**

- <what changed, as a player would see it>
- <…>

**Screenshots**
1. <what the first image shows>
2. <…>
```

- The headline is bold and one short line.
- Points are bullets, one line each where they can be, at most five.
- **Screenshots** for images, **Videos** for videos, **Screenshots and videos** for both, numbered in the order the files are attached; leave the heading out when nothing is attached.
- A link only to where players get the product, such as a store listing on one of the server's allowed domains; `mochi announce` refuses a link the server does not allow and says which.

Post right away when asked: the user's instruction is the go-ahead, so do not ask again or show a draft first.

```sh
mochi announce --file after.png <<'EOF'
**Average elo now shows for each team**

- The lobby shows each team's average elo
- Teams can be compared at a glance

**Screenshots**
1. The lobby with each team's average
EOF
```

`--pr <url>` adds a pull request of this repository as its own line, when the user wants it shared. It prints the posted message's link. Then tell the user what was posted and give its link.
