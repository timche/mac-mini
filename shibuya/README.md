# shibuya

`shibuya` is hachiko's dead man's switch, and the one piece of this repository that does not run on the Mac. Named for the station Hachikō turned up at every day, which is the whole of what it does: hachiko checks in after every sweep, and a quarter of an hour without a check-in is a message in the same Discord channel the alerts go to.

It exists because a watch cannot report its own silence. A LaunchAgent that never loaded because the Mac is sitting at its login window, a sweep hung on a mount whose server went away, a Mac that lost power, a binary that stopped compiling and left the last one that did — every one of those is hachiko saying nothing, which is also what a quiet Mac looks like. The only thing that can tell the two apart is something off the machine.

A Cloudflare Worker with one SQLite-backed Durable Object per host, which is the only kind the free plan runs, and the object's alarm is the deadline. `idFromName(host)` is how a host gets its own, so the Mac that checks in is the Mac that is reported missing; hachiko sends the Mac's name on the tailnet, the first label of `tailscale status --json`'s `Self.DNSName`, rather than its hostname, which the router can change under a running Mac and which shibuya would read as this Mac going offline. The last name the tailnet gave is kept in `~/Library/Caches/hachiko/host` for a sweep while tailscale is not answering, a Mac never on a tailnet falls back to its hostname, and `HACHIKO_HOST` overrides all three. The name is reduced to that shape — the first label, lowercased, anything else a hyphen — and a name is validated as `^[a-z0-9-]{1,32}$` before it reaches a storage key. The cost is about three hundred requests a day against a hundred thousand, and nothing runs between them.

Five routes. `GET /` answers `shibuya` with no token and no data, because something has to answer a browser. `POST /ping` carries the reading — free space, open incidents, busy processes, the commit the running binary was built from, and what the Mac is called — and `POST /fail` carries a reason instead; both count as a check-in, since a sweep that ran and went wrong is a sweep that ran. `GET /status` is for Tim: where a host stands, with ISO timestamps, and `DELETE /host` is for Tim too, forgetting one. The four that are not the root take `Authorization: Bearer <ping token>`, compared with `crypto.subtle.timingSafeEqual` against a secret and answered with `401` and nothing else; the method is part of the route, so anything else on one of them is a `404`, and a body over four kilobytes is refused unread. A call the Durable Object does not answer is a `503` and `try again` rather than Cloudflare's own 1101 page — which is what a deploy that has reached this Worker and not yet the object it calls looks like, for the minutes in between — and hachiko reads that as a check-in that did not land and retries on its next sweep.

The alarm is the switch. A check-in arms it a grace ahead of itself — `GRACE_MINUTES`, fifteen — so the alarm that fires is the one no check-in replaced. When it does fire, a forum post is opened titled `🔴 Mac mini offline · <day and time in Europe/London>`, carrying the last check-in's time and its reading, and the host is marked down. One alert per outage: nothing re-arms while it is down, beyond a single reminder six hours later in the same post — and the forum, not the object's memory, is what says the outage already has one, so an alarm retried after a write that did not land finds the post it opened and goes into that. The check-in that comes back posts the recovery into that post — `🟢 Mac mini is back — it checked in after 42 minutes of silence`, and what it came back to — and clears it. The forum's two tags work as they do on hachiko's own posts: an outage post or a failing streak's is opened tagged `Open`, and the message that ends it — the recovery, or the run that came back clean — is followed by `Open` off, `Resolved` on and the post archived, so what is left open in the forum is what is still wrong.

What the Mac is called is the check-in's to say: hachiko sends `display`, cut to forty characters and stripped of anything a terminal would act on, and shibuya uses it in every post title and every lead line. Without one it falls back to the host slug, which is a storage key spelled by whatever the router called the Mac rather than anything a person would write. The slug is still in the one line of each message nobody acts on, beside the build, and `GET /status` answers with both. The shape of a message here is `shibuya/src/message.ts` and it is the shape `hachiko/internal/wording` writes, because the two post into one channel; the six of them render in full in `npm test`.

A failing streak is the other half, and it is a streak rather than an incident per sweep. The first `/fail` opens a post of its own, `⚠️ Mac mini: hachiko's checks are failing · <time>`, with the reason; the ones after it post nothing, since a sweep that fails every five minutes for an hour is one thing wrong, with one reminder at the hour. A clean sweep says so in that post and closes it. What hachiko reports as a failure is the honest set — a walk that ran out of its seconds and saw only part of the disk, a check with no process sample, a state file it could not read — and all three are faults of the watch rather than of the Mac.

Discord is reached through shibuya's own bot, `shibuya`, rather than hachiko's: the name on a message is what tells what the watch says from what the thing watching the watch says, and a token kept on Cloudflare reaches nothing but this forum rather than the bot that takes Tim's replies to an on-call agent. It posts the way hachiko's sender does. The first message of an incident is one `POST /channels/<forum>/threads` carrying the post's name, the `Open` tag and the message; every message after it is a `POST` into the post; a post Tim deleted answers `404` with code `10003` and the message that found it gone opens another under the incident's own name. The forum is `DISCORD_FORUM` in `wrangler.jsonc`, a channel id and no secret, and its tags are found by name on the forum Discord describes, a tag it lacks skipped. Posts are opened to auto-archive after a week rather than hachiko's day, because Discord stops listing a post as active once it has archived, and an active post is how shibuya finds one again.

The forum's open posts are the answer to whether an incident already has a post. Before a message opens one, shibuya lists the server's active threads and looks for a post in this forum that the bot itself opened, still tagged `Open`, whose name starts with this Mac's name and this kind — `🔴 Mac mini offline · ` or `⚠️ Mac mini: hachiko's checks are failing · `, the same words whichever message opened it — and posts into the newest of those instead. The message that ends an incident closes every such post rather than only the one it went into, `Open` off, `Resolved` on and archived, after the message and never before it, since Discord unarchives a post anything is posted into. So a retried alarm whose write was lost, a thread id that went with a forgotten host, and a duplicate that got out anyway all end in one closed post. A Discord that cannot be listed is today's behaviour rather than no alert: the stored post if there is one, a new post if there is not. A check-in that changes nothing asks Discord nothing, and a label that would not go on is a log line and nothing else — the message is already in the post, and sending it again would be a second recovery. Every message carries `allowed_mentions` with nothing in it, since a reason quotes whatever filled the disk. The bot token is in no log and in no error: `console.log` only ever sees text it has been taken out of.

A Discord that will not answer is the one failure that may not be swallowed, since the alert is the whole point of the alarm. So a message that failed is kept and retried on an alarm of its own, backing off from a minute to half an hour, and the object stays armed until it lands.

`DELETE /host?host=<name>` is how an object stops existing, because one outlives its reason to exist: a hostname typed wrong once has a Durable Object of its own for ever, and a retired Mac's deadline is still armed the quarter of an hour after it is unplugged. It deletes the object's alarm and then everything the object holds, so no outage post, no reminder and no queued retry can follow, and answers `forgot <name>` — or `404` and `unknown host` when there was nothing stored under that name, which makes a second run harmless. Nothing goes to Discord, and a forum post the host is still down in is left exactly as it is: an outage that happened is worth a record whatever became of the Mac. A host that checks in afterwards is simply a new one.

A forget that lands while an alert is still in flight stands, which takes saying because the object's input gate opens while it waits on Discord: the alert resumes into a storage this forget emptied, and the record in its hand would otherwise go back and the deadline with it. So the object counts how many times it has been forgotten, every entry point takes that number before it loads, and the one place that writes drops the write when the number has moved. The count is in memory, which is what makes it right rather than a shortcut — the only write that can be undone is one from a handler already running on this instance, and an eviction that loses the count loses that handler too. Storage could not answer for it either way, since a host just forgotten and a host checking in for the first time are the same empty store.

```sh
# The header goes down a pipe for the same reason it does below: `ps` shows one process's
# arguments to every other on this Mac.
printf 'header = "Authorization: Bearer %s"\n' "$(cat ~/.config/hachiko/shibuya-token)" |
  curl -s -K - -X DELETE 'https://shibuya.timche.dev/host?host=probe-test'
```

One entrance: `shibuya.timche.dev`, a Custom Domain in `wrangler.jsonc` with `workers_dev` and `preview_urls` both off, because a workers.dev subdomain and a version preview URL are each a second hostname serving the same Worker with the same one token in front of it. Cloudflare creates the DNS record and the certificate for a Custom Domain itself, which is why there is no record to manage here and why the hostname must not already have a CNAME on it.

## shibuya's secrets

Three, and they are reached in opposite ways.

`DISCORD_BOT_TOKEN` is the `shibuya` bot's, the `bot token` field of the `shibuya-discord` item in the `dev` vault. `CLOUDFLARE_API_TOKEN` is `op://dev/shibuya/api token`, made from Cloudflare's **Edit Cloudflare Workers** template, which carries both permissions a Custom Domain deploy needs — *Workers Scripts: Edit* on the account and *Workers Routes: Edit* on the zone — and no DNS permission, since Cloudflare makes that record itself. All three are `op://` references in `shibuya/.env.op` beside the code, resolved by `op run --env-file` at the moment they are needed and never written anywhere.

The bot is a Discord application of its own named `shibuya`, with **Public Bot** off — which Discord refuses to save until the application's Install Link is set to None — and no privileged intent, since it reads which posts are open and never what is in them. It is invited to the private server and given, on the alerts forum, View Channel, Send Messages, Send Messages in Threads, Create Public Threads, Manage Threads and Read Message History, and nothing server-wide beyond that: a token that leaks from Cloudflare can then post and tag in that one forum. Manage Threads is the one that tags and closes a post.

`PING_TOKEN` is deliberately neither. hachiko reads it twice an hour for ever, which as a 1Password reference would be two hundred and eighty-eight `op run` calls a day against a service account with a daily limit; and writing a resolved `op://` value into a file is the one thing this machine never does. So it is generated on the Mac instead — `shibuya/secrets.sh`, 32 random bytes of hex at `~/.config/hachiko/shibuya-token`, mode 0600, in no repository and in no vault. What it can do if it leaks is fake a check-in, which is worth the asymmetry; hachiko refuses a token file anything but the account can read, because a loose one is a token every session on this Mac has and the fix is one `chmod` rather than a rotation.

If the token is lost or leaked, delete `~/.config/hachiko/shibuya-token` and run `shibuya/secrets.sh` again: it generates a new one and puts it on the Worker as `PING_TOKEN`, and the old one stops working the moment that lands. Between deleting the file and rerunning the script, hachiko has no token to send and says so once rather than every five minutes, and shibuya posts the Mac offline a quarter of an hour later — which is the honest signal, since nothing is checking in. The check-in that works again posts the recovery into that same post.

The URL is not a secret and is in the checkout, `home/.config/hachiko/shibuya`, one `https` line linked to `~/.config/hachiko/shibuya`. A Mac with no file there is one with the switch off rather than one with a broken switch: hachiko says so once and goes on watching.

## Deploying it

Two steps, in this order, because `wrangler secret put` wants a Worker to put a secret on:

```sh
~/.mac-mini/shibuya/deploy.sh      # creates the Worker and its Custom Domain
~/.mac-mini/shibuya/secrets.sh     # makes the ping token if there is none, then sets the secrets
```

`claude.sh` takes both of them on a fresh Mac, which is why neither has to be run by hand there; the second only when there is no token file, since it makes one and replaces the Worker's `PING_TOKEN` with it, and there is one Mac checking in. Between the two the Worker answers `401` to everything, which costs nothing: no check-in has ever arrived, so no alarm is armed and there is no outage to report.

`shibuya/secrets.sh` is the step nothing re-runs — a token generated on the Mac has nowhere for a later run to read it back from, so `claude.sh` takes it only where there is no token file yet. It makes the token if there is none, then pipes both values into `wrangler secret put` on stdin — never an argument, where `ps` shows every process on this Mac the command line of every other — and prints the names it set. It prompts for nothing, so a provision with nobody watching takes it.

`shibuya/deploy.sh` is everything after that: it installs from the lockfile, runs `npm test`, and deploys under `op run`. `claude.sh` is the one thing that calls it, once, after the signing key has stored the service-account token the `op run` needs — a Mac being provisioned has to come out with a switch, since there is nothing else to notice that it never did. Every deploy after that is run by hand, and no install runs one: an `op run` counts against the service account's day, and the Worker is the live switch rather than something to upload on a timer. It refuses to deploy with no token to resolve the Cloudflare one with, and a deploy that lands with no ping token file beside it says so.

Wrangler rather than Cloudflare's own `cf` CLI, which is in beta and short three things this needs: it cannot set one secret without rewriting the rest, a deploy through it wipes the secrets already set, and there is no vitest pool for a Worker bound to its own Durable Object.

`npm test` in `shibuya/` is its behaviour, and it runs in workerd through `@cloudflare/vitest-pool-workers` against the same `wrangler.jsonc` the deploy reads: the auth, the body limit, every field validated, the alarm a check-in arms, the one post an outage opens and the second alarm that opens nothing, the recovery into that post, a failing streak and the clean run that closes it, the open post found in the forum and posted into rather than a second, every open post closed with the incident, a forum that cannot be listed, Discord answering 404 and 500, and that the bot token reaches no log. `tsc --noEmit` is in it as well as vitest, because a Durable Object's whole interface is a type. The compatibility date is the newest the workerd that pool bundles will start on, so what the tests run against is what the deploy asks for.

To see where it is:

```sh
curl -s https://shibuya.timche.dev            # shibuya, and nothing else

# up or down, the last check-in and its reading. The header goes down a pipe as a curl
# config rather than in -H, because `ps` shows one process's arguments to every other on
# this Mac; printf is a shell builtin, so the token is in no command line either.
printf 'header = "Authorization: Bearer %s"\n' "$(cat ~/.config/hachiko/shibuya-token)" |
  curl -s -K - 'https://shibuya.timche.dev/status?host=tims-mac-mini'

cd ~/.mac-mini/shibuya && npx wrangler tail   # what the Worker is saying, live
```

## Environment knobs

`SHIBUYA_TOKEN_FILE` (the ping token the two shibuya scripts read and write).
