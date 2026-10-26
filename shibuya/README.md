# shibuya

`shibuya` is hachiko's dead man's switch, and the one piece of this repository that does not run on the Mac. Named for the station Hachikō turned up at every day, which is the whole of what it does: hachiko checks in after every sweep, and a quarter of an hour without a check-in is a message in the same Discord forum the alerts go to.

It exists because a watch cannot report its own silence. A LaunchAgent that never loaded because the Mac is sitting at its login window, a sweep hung on a mount whose server went away, a Mac that lost power, a binary that stopped compiling and left the last one that did — every one of those is hachiko saying nothing, which is also what a quiet Mac looks like. The only thing that can tell the two apart is something off the machine.

A Cloudflare Worker with one SQLite-backed Durable Object per host, which is the only kind the free plan runs, and the object's alarm is the deadline. `idFromName(host)` is how a host gets its own, so the Mac that checks in is the Mac that is reported missing. hachiko sends its name on the tailnet, the first label of `tailscale status --json`'s `Self.DNSName`, rather than its hostname, which the router can change under a running Mac and which shibuya would read as this Mac going offline; the last name the tailnet gave is kept in `~/Library/Caches/hachiko/host` for a sweep while tailscale is not answering, a Mac never on a tailnet falls back to its hostname, and `HACHIKO_HOST` overrides all three. The name is reduced to a slug and validated as `^[a-z0-9-]{1,32}$` before it reaches a storage key. The cost is about three hundred requests a day against a hundred thousand, and nothing runs between them.

## The routes

`GET /` answers `shibuya` with no token and no data, because something has to answer a browser. `POST /ping` carries the reading — free space, open incidents, busy processes, the commit the running binary was built from, and what the Mac is called — and `POST /fail` carries a reason instead; both count as a check-in, since a sweep that ran and went wrong is a sweep that ran. `GET /status` is for Tim: where a host stands, with ISO timestamps. `DELETE /host` is for Tim too, forgetting one.

The four that are not the root take `Authorization: Bearer <ping token>`, compared with `crypto.subtle.timingSafeEqual` and answered with `401` and nothing else; the method is part of the route, so anything else on one of them is a `404`, and a body over four kilobytes is refused unread. A call the Durable Object does not answer is a `503` and `try again` rather than Cloudflare's own 1101 page — which is what a deploy that has reached this Worker and not yet the object it calls looks like, for the minutes in between — and hachiko reads that as a check-in that did not land and retries on its next sweep.

One entrance: `shibuya.timche.dev`, a Custom Domain in `wrangler.jsonc` with `workers_dev` and `preview_urls` both off, because a workers.dev subdomain and a version preview URL are each a second hostname serving the same Worker with the same one token in front of it. Cloudflare creates the DNS record and the certificate for a Custom Domain itself, which is why there is no record to manage here and why the hostname must not already have a CNAME on it.

## The switch

A check-in arms the alarm a grace ahead of itself — `GRACE_MINUTES`, fifteen — so the alarm that fires is the one no check-in replaced. When it does fire, a forum post is opened, `🔴 Mac mini offline · <day and time in Europe/London>`, carrying the last check-in's time and its reading, and the host is marked down. One alert per outage: nothing re-arms while it is down, beyond a single reminder six hours later in the same post. The check-in that comes back posts the recovery into that post — `🟢 Mac mini is back — it checked in after 42 minutes of silence`, and what it came back to — and clears it.

A failing streak is the other half, and it is a streak rather than an incident per sweep. The first `/fail` opens a post of its own, `⚠️ Mac mini: hachiko's checks are failing · <time>`, with the reason; the ones after it post nothing, with one reminder at the hour, and a clean sweep says so in that post and closes it. What hachiko reports as a failure is the honest set — a walk that ran out of its seconds and saw only part of the disk, a check with no process sample, a state file it could not read — all three faults of the watch rather than of the Mac.

What the Mac is called is the check-in's to say: hachiko sends `display`, cut to forty characters and stripped of anything a terminal would act on, and shibuya uses it in every post title and every lead line. Without one it falls back to the host slug, which is a storage key spelled by whatever the router called the Mac rather than anything a person would write; the slug is still in the one line of each message nobody acts on, beside the build, and `GET /status` answers with both. The shape of a message is `shibuya/src/message.ts`, and it is the shape `hachiko/internal/wording` writes, because the two post into one channel.

## Discord

Through shibuya's own bot, `shibuya`, rather than hachiko's: the name on a message is what tells what the watch says from what the thing watching the watch says, and a token kept on Cloudflare reaches nothing but this forum rather than the bot that takes Tim's replies to an on-call agent. The forum is `DISCORD_FORUM` in `wrangler.jsonc`, a channel id and no secret, and its tags are found by name on the forum Discord describes, a tag it lacks skipped. A post is opened tagged `Open`, and the message that ends the incident is followed by `Open` off, `Resolved` on and the post archived — after the message and never before it, since Discord unarchives a post anything is posted into — so what is left open in the forum is what is still wrong.

The forum's own open posts, rather than the object's memory, are the answer to whether an incident already has one: before a message opens a post, shibuya looks through the server's active threads for one in this forum that the bot opened, still tagged `Open`, whose name starts with this Mac's name and this kind, and posts into the newest of those instead — so a retried alarm whose write was lost, a thread id that went with a forgotten host, and a duplicate that got out anyway all end in one closed post. Posts auto-archive after a week rather than hachiko's day, because Discord stops listing a post as active once it has archived and an active post is how shibuya finds one again. Every message carries `allowed_mentions` with nothing in it, since a reason quotes whatever filled the disk, and the bot token is in no log and in no error.

A Discord that will not answer is the one failure that may not be swallowed, the alert being the whole point of the alarm, so a message that failed is kept and retried on an alarm of its own, backing off from a minute to half an hour, and the object stays armed until it lands.

## Forgetting a host

An object outlives its reason to exist: a hostname typed wrong once has a Durable Object of its own for ever, and a retired Mac's deadline is still armed the quarter of an hour after it is unplugged.

```sh
# The header goes down a pipe rather than into -H, because `ps` shows one process's
# arguments to every other on this Mac, and printf is a shell builtin.
printf 'header = "Authorization: Bearer %s"\n' "$(cat ~/.config/hachiko/shibuya-token)" |
  curl -s -K - -X DELETE 'https://shibuya.timche.dev/host?host=probe-test'
```

It deletes the object's alarm and then everything the object holds, so no outage post, no reminder and no queued retry can follow, and answers `forgot <name>` — or `404` and `unknown host` when there was nothing stored under that name, which makes a second run harmless. Nothing goes to Discord, and a forum post the host is still down in is left exactly as it is: an outage that happened is worth a record whatever became of the Mac. A host that checks in afterwards is simply a new one.

A forget that lands while an alert is still in flight stands, which takes saying because the object's input gate opens while it waits on Discord: the alert would otherwise resume into a storage this forget emptied and put the record, and the deadline with it, back. So the object counts in memory how many times it has been forgotten, every entry point takes that number before it loads, and the one place that writes drops the write when the number has moved. In memory on purpose — storage could not answer for it either way, a host just forgotten and a host checking in for the first time being the same empty store.

## Its secrets

Three, and they are reached in opposite ways. `DISCORD_BOT_TOKEN` is the `shibuya` bot's, the `bot token` field of the `shibuya-discord` item in the `dev` vault. `CLOUDFLARE_API_TOKEN` is `op://dev/shibuya/api token`, made from Cloudflare's **Edit Cloudflare Workers** template, which carries both permissions a Custom Domain deploy needs — *Workers Scripts: Edit* on the account and *Workers Routes: Edit* on the zone — and no DNS permission, since Cloudflare makes that record itself. Both are `op://` references in `shibuya/.env.op` beside the code, resolved by `op run --env-file` at the moment they are needed and never written anywhere.

The bot is a Discord application of its own named `shibuya`, with **Public Bot** off — which Discord refuses to save until the application's Install Link is set to None — and no privileged intent, since it reads which posts are open and never what is in them. It is invited to the private server and given, on the alerts forum, View Channel, Send Messages, Send Messages in Threads, Create Public Threads, Manage Threads and Read Message History, and nothing server-wide beyond that, so a token that leaks from Cloudflare can post and tag in that one forum. Manage Threads is the one that tags and closes a post.

`PING_TOKEN` is deliberately neither. hachiko reads it twice an hour for ever, which as a 1Password reference would be two hundred and eighty-eight `op run` calls a day against a service account with a daily limit; and writing a resolved `op://` value into a file is the one thing this machine never does. So it is generated on the Mac instead — `shibuya/secrets.sh`, 32 random bytes of hex at `~/.config/hachiko/shibuya-token`, mode 0600, in no repository and in no vault. What it can do if it leaks is fake a check-in, which is worth the asymmetry; hachiko refuses a token file anything but the account can read, because a loose one is a token every session on this Mac has and the fix is one `chmod` rather than a rotation.

If the token is lost or leaked, delete `~/.config/hachiko/shibuya-token` and run `shibuya/secrets.sh` again: it generates a new one and puts it on the Worker as `PING_TOKEN`, and the old one stops working the moment that lands. Between the two, hachiko has no token to send and says so once rather than every five minutes, and shibuya posts the Mac offline a quarter of an hour later — the honest signal, since nothing is checking in — and the check-in that works again posts the recovery into that same post.

The URL is not a secret and is in the checkout, `home/.config/hachiko/shibuya`, one `https` line linked to `~/.config/hachiko/shibuya`. A Mac with no file there is one with the switch off rather than one with a broken switch: hachiko says so once and goes on watching.

## Deploying it

Two steps, in this order, because `wrangler secret put` wants a Worker to put a secret on:

```sh
~/.mac-mini/shibuya/deploy.sh      # creates the Worker and its Custom Domain
~/.mac-mini/shibuya/secrets.sh     # makes the ping token if there is none, then sets the secrets
```

`claude.sh` takes both on a fresh Mac, so neither has to be run by hand there — a Mac being provisioned has to come out with a switch, since there is nothing else to notice that it never did. The second only where there is no token file, since a token generated on the Mac has nowhere for a later run to read it back from and there is one Mac checking in. Between the two the Worker answers `401` to everything, which costs nothing: no check-in has ever arrived, so no alarm is armed and there is no outage to report. `secrets.sh` pipes both values into `wrangler secret put` on stdin, never an argument, and prompts for nothing, so a provision with nobody watching takes it.

Every deploy after that is run by hand, and no install runs one: an `op run` counts against the service account's day, and the Worker is the live switch rather than something to upload on a timer. `deploy.sh` installs from the lockfile, runs `npm test` and deploys under one `op run`; it refuses to deploy with no token to resolve the Cloudflare one with, and a deploy that lands with no ping token file beside it says so.

`npm test` is this Worker's behaviour, in workerd through `@cloudflare/vitest-pool-workers` against the same `wrangler.jsonc` the deploy reads, with `tsc --noEmit` beside vitest because a Durable Object's whole interface is a type. The compatibility date is the newest the workerd that pool bundles will start on, so what the tests run against is what the deploy asks for. `npx wrangler deploy --dry-run` proves the bundle builds and the bindings resolve without deploying anything, and is what to use rather than a real deploy to try something out.

```sh
curl -s https://shibuya.timche.dev            # shibuya, and nothing else

# up or down, the last check-in and its reading
printf 'header = "Authorization: Bearer %s"\n' "$(cat ~/.config/hachiko/shibuya-token)" |
  curl -s -K - 'https://shibuya.timche.dev/status?host=tims-mac-mini'

cd ~/.mac-mini/shibuya && npx wrangler tail   # what the Worker is saying, live
```

## Environment knobs

`SHIBUYA_TOKEN_FILE` (the ping token the two shibuya scripts read and write), and `GRACE_MINUTES` and `DISCORD_FORUM` in `wrangler.jsonc`.
