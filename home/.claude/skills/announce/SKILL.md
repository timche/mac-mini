---
name: announce
description: "Share or announce finished work with the team on Discord with mochi announce. Load when the user asks to share or announce work with the team, such as \"share with the team\", \"announce this\" or \"post this for the mods\", never otherwise."
---

# announce

The user asked you to share what this session did with the team. `mochi announce` posts it to the announce channel of the Discord server for this repository, from any session, whether mochi started it or not.

## Who reads it

The team, mostly moderators who are not technical and have no GitHub access. They read it to know what changed in the product and to tell players about it. It stands on its own: nobody reading it has seen this session.

## What to write

What is new or changed, and what players will notice, in plain language. Use this shape:

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
- A link only to where players get the product, such as a store listing on one of the server's allowed domains; `mochi announce` refuses any other link and says which.

## What not to write

No internals: no code, file or function names, file paths, branch names, configuration, infrastructure or jargon. Name none of the tools or services behind the work — not the software running this session, the AI, the terminal or the like. Nothing unreleased or secret that the user has not said may go out, and nobody is mentioned.

## Posting

Post right away when asked: the user's instruction is the go-ahead, so do not ask again or show a draft first. Give the text on stdin, and attach images or recordings with `--file`, up to ten, from this checkout or its scratch folder:

```sh
mochi announce --file after.png <<'EOF'
**Average elo now shows for each team**

- The lobby shows each team's average elo
- Teams can be compared at a glance

**Screenshots**
1. The lobby with each team's average
EOF
```

`--pr <url>` adds a pull request of this repository as its own line, when the user wants it shared. mochi refuses a post with a link it does not allow, a file path, a file that looks like a secret, a file too large for the server, or anything the secret scan finds; fix what it names and post again, or tell the user what it said.

It prints the posted message's link. Then tell the user what was posted and give its link.
