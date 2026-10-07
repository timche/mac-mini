import { DurableObject } from "cloudflare:workers";

import { graceMs, type Env } from "./env";
import { postToForum, redact } from "./discord";
import { code, duration, machine, markers, plain, plural, render, size, title, when } from "./message";

// One object per host, so the alarm that decides a Mac has gone quiet is the same object
// that holds its last check-in, with no coordination between the two.
//
// Everything the object remembers is one record under one key: every entry point reads it
// once and writes it once, and a Durable Object runs one of them at a time.

const REMIND_DOWN_AFTER = 6 * 60 * 60 * 1000;
const REMIND_FAILING_AFTER = 60 * 60 * 1000;

// A Discord that answered 500 while a Mac is offline is the one failure that may not be
// swallowed: the alert is the whole point of the alarm. So it is retried on an alarm of
// its own, backing off to half an hour, and given up on after a day of them.
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
  // Every message carries one, because a post Tim deleted is a post the next message has to
  // open; `open` is what says which of the two this message is.
  title: string;
  open: boolean;
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

    await this.recover(live, silence, now);
    await this.clean(live, now);

    await this.settle(live, forgets);
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

    await this.recover(live, silence, now);

    const reason = checkin.reason === undefined || checkin.reason === "" ? "hachiko did not say" : checkin.reason;
    live.failingReason = reason;

    if (live.failingSince === 0) {
      live.failingSince = now;

      const named = name(live);
      const shown = plain(named);
      await this.post(live, {
        where: "failing",
        open: true,
        title: title(markers.warn, `${named}: hachiko's checks are failing`, now),
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

    await this.settle(live, forgets);
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

    await this.drain(live, now);

    // A ping that arrived after this alarm was set has already rearmed the deadline, so the
    // alarm that fired is about an interval that is no longer the newest one.
    if (live.downSince === 0 && live.lastPing !== 0 && now - live.lastPing >= graceMs(this.env)) {
      live.downSince = now;

      const named = name(live);
      const shown = plain(named);
      await this.post(live, {
        where: "outage",
        open: true,
        title: title(markers.down, `${named} offline`, now),
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
      await this.post(live, {
        where: "outage",
        open: false,
        title: title(markers.down, `${named} still offline`, now),
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
      await this.post(live, {
        where: "failing",
        open: false,
        title: title(markers.warn, `${named}: hachiko's checks still failing`, now),
        text: render({
          marker: markers.warn,
          lead: `${shown}: hachiko's checks have been failing for ${duration(now - live.failingSince)}`,
          details: [{ label: "Why", value: plain(live.failingReason) }],
          action: `Read ${code(LOG)} to see what is failing.`,
        }),
      });
    }

    await this.settle(live, forgets);
  }

  // The whole silence rather than the time since the alert, because what Tim wants from a
  // recovery is how long the Mac was gone, and the alert went out a grace into that.
  private async recover(live: Live, silence: number, now: number): Promise<void> {
    if (live.downSince === 0) {
      return;
    }

    live.downSince = 0;
    live.downReminded = 0;

    const named = name(live);
    const shown = plain(named);
    await this.post(live, {
      where: "outage",
      open: false,
      title: title(markers.clear, `${named} back online`, now),
      text: render({
        marker: markers.clear,
        lead: `${shown} is back — it checked in after ${duration(silence)} of silence`,
        details: [{ label: "Reading now", value: live.summary }],
        action: "Nothing to do.",
      }),
    });
  }

  private async clean(live: Live, now: number): Promise<void> {
    if (live.failingSince === 0) {
      return;
    }

    const failing = now - live.failingSince;
    live.failingSince = 0;
    live.failingReason = "";
    live.failingReminded = 0;

    const named = name(live);
    const shown = plain(named);
    await this.post(live, {
      where: "failing",
      open: false,
      title: title(markers.clear, `${named}: hachiko's checks are clean`, now),
      text: render({
        marker: markers.clear,
        lead: `${shown}: hachiko's checks are finishing again after ${duration(failing)}`,
        details: [{ label: "Reading now", value: live.summary }],
        action: "Nothing to do.",
      }),
    });
  }

  private async post(live: Live, message: Omit<Queued, "attempt" | "at">): Promise<void> {
    if (await this.deliver(live, message)) {
      return;
    }
    live.queue.push({ ...message, attempt: 1, at: Date.now() + RETRY_FIRST });
  }

  private async deliver(live: Live, message: Omit<Queued, "attempt" | "at">): Promise<boolean> {
    const thread = message.where === "outage" ? live.outageThread : live.failingThread;

    try {
      // `open` is a post to open rather than one to go back into, which is what makes a new
      // outage a new post under a thread id this object is still holding. The title rides
      // along either way: a post Tim deleted answers 404, and the message that found it gone
      // opens one of its own under a name of its own rather than its first line.
      const landed = await postToForum(this.env.DISCORD_WEBHOOK_URL, {
        content: message.text,
        threadId: message.open ? "" : thread,
        threadName: message.title,
      });

      if (message.where === "outage") {
        live.outageThread = landed;
      } else {
        live.failingThread = landed;
      }
      return true;
    } catch (err) {
      console.log(`shibuya: the message about ${live.host} did not reach Discord: ${this.say(err)}`);
      return false;
    }
  }

  private async drain(live: Live, now: number): Promise<void> {
    const keep: Queued[] = [];

    for (const message of live.queue) {
      if (message.at > now) {
        keep.push(message);
        continue;
      }
      if (await this.deliver(live, message)) {
        continue;
      }

      message.attempt += 1;
      if (message.attempt > RETRY_ATTEMPTS) {
        console.log(`shibuya: giving up on a message about ${live.host} after ${RETRY_ATTEMPTS} attempts`);
        continue;
      }

      message.at = now + Math.min(RETRY_FIRST * 2 ** (message.attempt - 1), RETRY_MAX);
      keep.push(message);
    }

    live.queue = keep;
  }

  // The one place this object writes, which is what makes it the one place to check that
  // what it is about to write still belongs to a host that exists.
  private async settle(live: Live, forgets: number): Promise<void> {
    if (forgets !== this.forgets) {
      return;
    }

    await this.ctx.storage.put("live", live);
    await this.arm(live);
  }

  // One alarm, so the deadline, the two reminders and whatever is waiting to be retried are
  // one time: the soonest of them, recomputed on every write.
  private async arm(live: Live): Promise<void> {
    const times: number[] = live.queue.map((message) => message.at);

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
    return { ...empty, ...stored };
  }

  private say(err: unknown): string {
    return redact(err instanceof Error ? err.message : String(err), this.env.DISCORD_WEBHOOK_URL);
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

function iso(at: number): string | null {
  return at === 0 ? null : new Date(at).toISOString();
}
