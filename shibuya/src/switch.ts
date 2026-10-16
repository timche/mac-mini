import { DurableObject } from "cloudflare:workers";

import { graceMs, type Env } from "./env";
import { Discord, GoneError, type Thread } from "./discord";
import { code, duration, machine, markers, plain, plural, render, size, title, when } from "./message";

// One object per host, so the alarm that decides a Mac has gone quiet is the same object
// that holds its last check-in, with no coordination between the two.
//
// Everything the object remembers is one record under one key, read and written whole.

const REMIND_DOWN_AFTER = 6 * 60 * 60 * 1000;
const REMIND_FAILING_AFTER = 60 * 60 * 1000;

// A Discord that answered 500 while a Mac is offline is the one failure that may not be
// swallowed: the alert is the whole point of the alarm. So it is retried on an alarm of
// its own, backing off to half an hour, and given up on after a day of them.
//
// Every message is queued and written before it is sent, never sent and then written. A
// handler that posts and then fails to write is retried — an alarm by Cloudflare itself,
// within seconds — and the retry, finding nothing written, posts the same alert again, once
// a retry for as long as storage stays down. A write the object could not make holds back
// every fetch behind it, so a message nothing recorded never leaves.
const RETRY_FIRST = 60_000;
const RETRY_MAX = 30 * 60_000;
const RETRY_ATTEMPTS = 12;

// The one file a failing watch is explained in, and the only thing to do about one.
const LOG = "~/Library/Logs/hachiko.log";

export interface Checkin {
  host: string;
  display?: string;
  free_gb?: number;
  open_incidents?: number;
  hot_processes?: number;
  version?: string;
  reason?: string;
}

export interface Status {
  host: string;
  display: string;
  state: "up" | "down" | "unknown";
  last_ping: string | null;
  last_summary: string;
  down_since: string | null;
  failing_since: string | null;
}

type Where = "outage" | "failing";

interface Queued {
  where: Where;
  text: string;
  // A post to open rather than one to go back into, which is what makes a new outage a new
  // post under a thread id this object is still holding.
  open: boolean;
  // The message that ends the incident, after which the post is tagged Resolved and closed.
  // Optional, since a queue written before it existed has none.
  resolve?: boolean;
  attempt: number;
  at: number;
}

interface Live {
  host: string;
  // What the Mac calls itself, which is what Tim calls it. The slug is the storage key.
  display: string;
  lastPing: number;
  summary: string;
  version: string;
  downSince: number;
  downReminded: number;
  outageThread: string;
  failingSince: number;
  failingReason: string;
  failingReminded: number;
  failingThread: string;
  queue: Queued[];
}

const empty: Live = {
  host: "",
  display: "",
  lastPing: 0,
  summary: "",
  version: "",
  downSince: 0,
  downReminded: 0,
  outageThread: "",
  failingSince: 0,
  failingReason: "",
  failingReminded: 0,
  failingThread: "",
  queue: [],
};

export class Switch extends DurableObject<Env> {
  // How many times this object has been forgotten. Every entry point takes a copy before it
  // loads and hands it to settle(), which drops the write when the number has moved: the
  // record in its hand is a host that no longer exists, and putting it back would re-arm a
  // deadline for a Mac nothing will ever check in for again.
  //
  // The input gate opens while a handler awaits Discord, which is the whole of the window —
  // a DELETE arrives mid-alert, empties the object, and the alert's own settle() would
  // otherwise undo it and post the host offline a grace later.
  //
  // In memory rather than in storage, which is what makes it right: the only write that can
  // be undone is one from a handler already running on this instance, and an eviction that
  // loses this number loses that handler with it. Storage could not stand in for it anyway —
  // a host just forgotten and a host checking in for the first time are the same empty store.
  private forgets = 0;

  // The input gate opens while a message is out to Discord, so a second entry point can reach
  // the queue then and find the same message still due.
  private flushing = false;

  private client: Discord | undefined;
  private clientToken = "";

  // A check-in is liveness and nothing else: it rearms the deadline, and it is what says an
  // outage or a failing streak is over.
  async ping(checkin: Checkin): Promise<void> {
    const forgets = this.forgets;
    const live = await this.load();
    const now = Date.now();

    const silence = live.lastPing === 0 ? 0 : now - live.lastPing;

    live.host = checkin.host;
    live.display = checkin.display ?? live.display;
    live.summary = summarize(checkin);
    live.version = checkin.version ?? live.version;
    live.lastPing = now;

    this.recover(live, silence);
    this.clean(live, now);

    await this.commit(live, forgets);
  }

  // A sweep that ran and went wrong is alive, so this counts for the deadline exactly as a
  // ping does. What it opens instead is a failing streak, whose first fail is the only one
  // that posts: a reason that repeats every five minutes is one incident.
  async fail(checkin: Checkin): Promise<void> {
    const forgets = this.forgets;
    const live = await this.load();
    const now = Date.now();

    const silence = live.lastPing === 0 ? 0 : now - live.lastPing;

    live.host = checkin.host;
    live.display = checkin.display ?? live.display;
    live.summary = summarize(checkin);
    live.version = checkin.version ?? live.version;
    live.lastPing = now;

    this.recover(live, silence);

    const reason = checkin.reason === undefined || checkin.reason === "" ? "hachiko did not say" : checkin.reason;
    live.failingReason = reason;

    if (live.failingSince === 0) {
      live.failingSince = now;

      const named = name(live);
      const shown = plain(named);
      this.post(live, {
        where: "failing",
        open: true,
        text: render({
          marker: markers.warn,
          lead: `${shown} is still checking in, but hachiko's checks are not finishing`,
          details: [
            { label: "Why", value: plain(reason) },
            { label: "Last reading", value: live.summary },
          ],
          action: `The Mac is up and the watch is half blind: read ${code(LOG)}.`,
          machine: machine(live.host, live.version),
        }),
      });
    }

    await this.commit(live, forgets);
  }

  // An object outlives its reason to exist: a hostname typed wrong once has one of its own
  // for ever, and a retired Mac's deadline is armed the next quarter of an hour after it is
  // unplugged. The alarm goes before the state, or a deadline survives the host it was for.
  // Nothing is posted: an outage post is the record of an outage that happened.
  async forget(): Promise<boolean> {
    const known = (await this.ctx.storage.get("live")) !== undefined;

    // Before either delete rather than after both, so a handler that resumes between them
    // still finds the number moved.
    this.forgets += 1;

    await this.ctx.storage.deleteAlarm();
    await this.ctx.storage.deleteAll();

    return known;
  }

  async status(host: string): Promise<Status> {
    const live = await this.load();

    return {
      host: live.host === "" ? host : live.host,
      display: live.display,
      state: live.lastPing === 0 ? "unknown" : live.downSince === 0 ? "up" : "down",
      last_ping: iso(live.lastPing),
      last_summary: live.summary,
      down_since: iso(live.downSince),
      failing_since: iso(live.failingSince),
    };
  }

  override async alarm(): Promise<void> {
    const forgets = this.forgets;
    const live = await this.load();
    const now = Date.now();

    // A ping that arrived after this alarm was set has already rearmed the deadline, so the
    // alarm that fired is about an interval that is no longer the newest one.
    if (live.downSince === 0 && live.lastPing !== 0 && now - live.lastPing >= graceMs(this.env)) {
      live.downSince = now;

      const named = name(live);
      const shown = plain(named);
      this.post(live, {
        where: "outage",
        open: true,
        text: render({
          marker: markers.down,
          lead: `${shown} has gone quiet — no check-in for ${duration(now - live.lastPing)}`,
          details: [
            { label: "Last check-in", value: when(live.lastPing, now) },
            { label: "Last reading", value: live.summary },
          ],
          action: "Check that the Mac is powered up and on the network. shibuya posts here when it checks in again.",
          machine: machine(live.host, live.version),
        }),
      });
    } else if (live.downSince !== 0 && live.downReminded === 0 && now - live.downSince >= REMIND_DOWN_AFTER) {
      live.downReminded = now;

      const named = name(live);
      const shown = plain(named);
      this.post(live, {
        where: "outage",
        open: false,
        text: render({
          marker: markers.down,
          lead: `${shown} is still offline, ${duration(now - live.downSince)} after the first alert`,
          details: [{ label: "Last check-in", value: when(live.lastPing, now) }],
          action: "Check that the Mac is powered up and on the network.",
        }),
      });
    }

    if (live.failingSince !== 0 && live.failingReminded === 0 && now - live.failingSince >= REMIND_FAILING_AFTER) {
      live.failingReminded = now;

      const named = name(live);
      const shown = plain(named);
      this.post(live, {
        where: "failing",
        open: false,
        text: render({
          marker: markers.warn,
          lead: `${shown}: hachiko's checks have been failing for ${duration(now - live.failingSince)}`,
          details: [{ label: "Why", value: plain(live.failingReason) }],
          action: `Read ${code(LOG)} to see what is failing.`,
        }),
      });
    }

    await this.commit(live, forgets);
  }

  // The whole silence rather than the time since the alert, because what Tim wants from a
  // recovery is how long the Mac was gone, and the alert went out a grace into that.
  private recover(live: Live, silence: number): void {
    if (live.downSince === 0) {
      return;
    }

    live.downSince = 0;
    live.downReminded = 0;

    const named = name(live);
    const shown = plain(named);
    this.post(live, {
      where: "outage",
      open: false,
      resolve: true,
      text: render({
        marker: markers.clear,
        lead: `${shown} is back — it checked in after ${duration(silence)} of silence`,
        details: [{ label: "Reading now", value: live.summary }],
        action: "Nothing to do.",
      }),
    });
  }

  private clean(live: Live, now: number): void {
    if (live.failingSince === 0) {
      return;
    }

    const failing = now - live.failingSince;
    live.failingSince = 0;
    live.failingReason = "";
    live.failingReminded = 0;

    const named = name(live);
    const shown = plain(named);
    this.post(live, {
      where: "failing",
      open: false,
      resolve: true,
      text: render({
        marker: markers.clear,
        lead: `${shown}: hachiko's checks are finishing again after ${duration(failing)}`,
        details: [{ label: "Reading now", value: live.summary }],
        action: "Nothing to do.",
      }),
    });
  }

  private post(live: Live, message: Omit<Queued, "attempt" | "at">): void {
    live.queue.push({ ...message, attempt: 0, at: Date.now() });
  }

  private async commit(live: Live, forgets: number): Promise<void> {
    if (await this.settle(live, forgets)) {
      await this.flush(forgets);
    }
  }

  // One message at a time, each settled against the record as it stands once Discord has
  // answered rather than as it stood before: another entry point may have written meanwhile.
  private async flush(forgets: number): Promise<void> {
    if (this.flushing) {
      return;
    }
    this.flushing = true;

    try {
      for (;;) {
        if (forgets !== this.forgets) {
          return;
        }

        const before = await this.load();
        const message = due(before.queue, Date.now());
        if (message === undefined) {
          return;
        }

        const landed = await this.deliver(before, message);

        const live = await this.load();
        this.delivered(live, message, landed, Date.now());
        if (!(await this.settle(live, forgets))) {
          return;
        }
      }
    } finally {
      this.flushing = false;
    }
  }

  // The post the message landed in, or undefined when it did not land.
  private async deliver(live: Live, message: Queued): Promise<string | undefined> {
    const discord = this.discord();
    const prefix = postPrefix(message.where, name(live));
    let open: Thread[] | undefined;

    try {
      let post = message.open ? "" : message.where === "outage" ? live.outageThread : live.failingThread;

      if (post !== "") {
        try {
          await discord.postInto(post, message.text);
        } catch (err) {
          if (!(err instanceof GoneError)) {
            throw err;
          }
          post = "";
        }
      }

      // The forum decides whether this incident already has a post, not this object's
      // memory of one: a retried alarm whose write was lost, a thread id that went with a
      // forgotten host, a post opened by an instance that died before it could say so, all
      // find the post that is there rather than opening a second.
      if (post === "") {
        open = await this.openPosts(discord, live, prefix);
        const newest = open[open.length - 1];
        if (newest !== undefined) {
          try {
            await discord.postInto(newest.id, message.text);
            post = newest.id;
          } catch (err) {
            if (!(err instanceof GoneError)) {
              throw err;
            }
          }
        }
      }

      if (post === "") {
        post = await discord.openPost(postTitle(message.where, name(live), Date.now()), message.text);
      }

      if (message.resolve === true) {
        await this.resolve(discord, live, prefix, post, open);
      }
      return post;
    } catch (err) {
      console.log(`shibuya: the message about ${live.host} did not reach Discord: ${discord.scrub(err)}`);
      return undefined;
    }
  }

  // An empty list when Discord cannot say, which is today's behaviour without the forum: the
  // stored post if there is one, a new post if there is not, and never no alert.
  private async openPosts(discord: Discord, live: Live, prefix: string): Promise<Thread[]> {
    try {
      return await discord.openPosts(prefix);
    } catch (err) {
      console.log(`shibuya: the forum's open posts about ${live.host} could not be read: ${discord.scrub(err)}`);
      return [];
    }
  }

  // Every open post of this incident's kind rather than the one it ended in, so a duplicate
  // that got out anyway is closed with it. Reported and never retried: the message is already
  // in the post, and a label that would not go on is no reason to send it a second time.
  private async resolve(discord: Discord, live: Live, prefix: string, landed: string, listed?: Thread[]): Promise<void> {
    const posts = listed ?? (await this.openPosts(discord, live, prefix));
    const close = new Map<string, string[] | undefined>([[landed, undefined]]);
    for (const post of posts) {
      close.set(post.id, post.appliedTags);
    }

    for (const [post, tags] of close) {
      try {
        await discord.closePost(post, tags);
      } catch (err) {
        console.log(`shibuya: a post about ${live.host} could not be closed: ${discord.scrub(err)}`);
      }
    }
  }

  // Kept for the life of the instance, so the forum and the bot's own id are asked once.
  private discord(): Discord {
    const token = this.env.DISCORD_BOT_TOKEN ?? "";
    if (this.client === undefined || this.clientToken !== token) {
      this.client = new Discord(token, this.env.DISCORD_FORUM);
      this.clientToken = token;
    }
    return this.client;
  }

  private delivered(live: Live, message: Queued, landed: string | undefined, now: number): void {
    const index = live.queue.findIndex((queued) => same(queued, message));

    if (landed !== undefined) {
      if (message.where === "outage") {
        live.outageThread = landed;
      } else {
        live.failingThread = landed;
      }
      if (index !== -1) {
        live.queue.splice(index, 1);
      }
      return;
    }

    const queued = live.queue[index];
    if (queued === undefined) {
      return;
    }

    queued.attempt += 1;
    if (queued.attempt > RETRY_ATTEMPTS) {
      console.log(`shibuya: giving up on a message about ${live.host} after ${RETRY_ATTEMPTS} attempts`);
      live.queue.splice(index, 1);
      return;
    }
    queued.at = now + Math.min(RETRY_FIRST * 2 ** (queued.attempt - 1), RETRY_MAX);
  }

  // The one place this object writes, which is what makes it the one place to check that
  // what it is about to write still belongs to a host that exists.
  private async settle(live: Live, forgets: number): Promise<boolean> {
    if (forgets !== this.forgets) {
      return false;
    }

    await this.ctx.storage.put("live", live);
    await this.arm(live);
    return true;
  }

  // One alarm, so the deadline, the two reminders and whatever is waiting to be retried are
  // one time: the soonest of them, recomputed on every write.
  private async arm(live: Live): Promise<void> {
    const times: number[] = heads(live.queue).map((message) => message.at);

    if (live.downSince === 0 && live.lastPing !== 0) {
      times.push(live.lastPing + graceMs(this.env));
    }
    if (live.downSince !== 0 && live.downReminded === 0) {
      times.push(live.downSince + REMIND_DOWN_AFTER);
    }
    if (live.failingSince !== 0 && live.failingReminded === 0) {
      times.push(live.failingSince + REMIND_FAILING_AFTER);
    }

    if (times.length === 0) {
      await this.ctx.storage.deleteAlarm();
      return;
    }
    await this.ctx.storage.setAlarm(Math.min(...times));
  }

  private async load(): Promise<Live> {
    const stored = await this.ctx.storage.get<Live>("live");
    // A fresh queue, or a push onto a host with none would land in `empty` itself.
    return { ...empty, queue: [], ...stored };
  }

}

// What the Mac is called, falling back to the slug it is keyed by. The slug is a storage key
// that happens to be readable, so it belongs in the subtext line and nowhere Tim is reading
// a sentence.
function name(live: Live): string {
  return live.display === "" ? live.host : live.display;
}

// One line of reading, in the words the rest of a message is written in: no counts of things
// with no unit on them, and nothing that reads as a field name.
export function summarize(checkin: Checkin): string {
  const parts: string[] = [];

  if (checkin.free_gb !== undefined) {
    parts.push(`${size(checkin.free_gb)} free`);
  }
  if (checkin.open_incidents !== undefined) {
    parts.push(
      checkin.open_incidents === 0
        ? "no open incidents"
        : plural(checkin.open_incidents, "open incident", "open incidents"),
    );
  }
  if (checkin.hot_processes !== undefined) {
    parts.push(
      checkin.hot_processes === 0 ? "nothing busy" : `${plural(checkin.hot_processes, "process", "processes")} busy`,
    );
  }

  return parts.join(", ");
}

// The first message waiting for each post, since a recovery sent ahead of the alert it
// answers would land in no post or in the last outage's.
function heads(queue: Queued[]): Queued[] {
  const seen = new Set<Where>();
  const first: Queued[] = [];
  for (const message of queue) {
    if (!seen.has(message.where)) {
      seen.add(message.where);
      first.push(message);
    }
  }
  return first;
}

function due(queue: Queued[], now: number): Queued | undefined {
  return heads(queue).find((message) => message.at <= now);
}

function same(a: Queued, b: Queued): boolean {
  return a.where === b.where && a.open === b.open && a.text === b.text;
}

// What a post of each kind is called, and the start of that name that identifies it: the same
// words whichever message happened to open it, so the forum can be searched for the one post
// an incident has.
function postTitle(where: Where, named: string, at: number): string {
  return where === "outage" ? title(markers.down, `${named} offline`, at) : title(markers.warn, failing(named), at);
}

function postPrefix(where: Where, named: string): string {
  return where === "outage" ? `${markers.down} ${named} offline · ` : `${markers.warn} ${failing(named)} · `;
}

function failing(named: string): string {
  return `${named}: hachiko's checks are failing`;
}

function iso(at: number): string | null {
  return at === 0 ? null : new Date(at).toISOString();
}
