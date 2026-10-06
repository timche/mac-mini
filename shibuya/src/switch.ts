import { DurableObject } from "cloudflare:workers";

import { graceMs, type Env } from "./env";
import { postToForum, redact } from "./discord";

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

export interface Checkin {
  host: string;
  free_gb?: number;
  open_incidents?: number;
  hot_processes?: number;
  version?: string;
  reason?: string;
}

export interface Status {
  host: string;
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
  title?: string;
  attempt: number;
  at: number;
}

interface Live {
  host: string;
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
  // A check-in is liveness and nothing else: it rearms the deadline, and it is what says an
  // outage or a failing streak is over.
  async ping(checkin: Checkin): Promise<void> {
    const live = await this.load();
    const now = Date.now();

    const silence = live.lastPing === 0 ? 0 : now - live.lastPing;

    live.host = checkin.host;
    live.summary = summarize(checkin);
    live.version = checkin.version ?? live.version;
    live.lastPing = now;

    await this.recover(live, silence);
    await this.clean(live, now);

    await this.settle(live);
  }

  // A sweep that ran and went wrong is alive, so this counts for the deadline exactly as a
  // ping does. What it opens instead is a failing streak, whose first fail is the only one
  // that posts: a reason that repeats every five minutes is one incident.
  async fail(checkin: Checkin): Promise<void> {
    const live = await this.load();
    const now = Date.now();

    const silence = live.lastPing === 0 ? 0 : now - live.lastPing;

    live.host = checkin.host;
    live.summary = summarize(checkin);
    live.version = checkin.version ?? live.version;
    live.lastPing = now;

    await this.recover(live, silence);

    const reason = checkin.reason ?? "no reason given";
    live.failingReason = reason;

    if (live.failingSince === 0) {
      live.failingSince = now;
      await this.post(live, {
        where: "failing",
        title: `${live.host} · hachiko runs are failing · ${london(now)}`,
        text: [`${live.host}: a hachiko sweep ran and did not finish its job.`, reason, live.summary].join("\n"),
      });
    }

    await this.settle(live);
  }

  // An object outlives its reason to exist: a hostname typed wrong once has one of its own
  // for ever, and a retired Mac's deadline is armed the next quarter of an hour after it is
  // unplugged. The alarm goes before the state, or a deadline survives the host it was for.
  // Nothing is posted: an outage post is the record of an outage that happened.
  async forget(): Promise<boolean> {
    const known = (await this.ctx.storage.get("live")) !== undefined;

    await this.ctx.storage.deleteAlarm();
    await this.ctx.storage.deleteAll();

    return known;
  }

  async status(host: string): Promise<Status> {
    const live = await this.load();

    return {
      host: live.host === "" ? host : live.host,
      state: live.lastPing === 0 ? "unknown" : live.downSince === 0 ? "up" : "down",
      last_ping: iso(live.lastPing),
      last_summary: live.summary,
      down_since: iso(live.downSince),
      failing_since: iso(live.failingSince),
    };
  }

  override async alarm(): Promise<void> {
    const live = await this.load();
    const now = Date.now();

    await this.drain(live, now);

    // A ping that arrived after this alarm was set has already rearmed the deadline, so the
    // alarm that fired is about an interval that is no longer the newest one.
    if (live.downSince === 0 && live.lastPing !== 0 && now - live.lastPing >= graceMs(this.env)) {
      live.downSince = now;
      await this.post(live, {
        where: "outage",
        title: `${live.host} offline · ${london(now)}`,
        text: [
          `${live.host} has not checked in for ${span(now - live.lastPing)}.`,
          `Last ping: ${london(live.lastPing)}.`,
          `Last reading: ${live.summary === "" ? "none" : live.summary}.`,
          live.version === "" ? "" : `hachiko ${live.version}.`,
        ]
          .filter((line) => line !== "")
          .join("\n"),
      });
    } else if (live.downSince !== 0 && live.downReminded === 0 && now - live.downSince >= REMIND_DOWN_AFTER) {
      live.downReminded = now;
      await this.post(live, {
        where: "outage",
        text: `${live.host} has still not checked in, ${span(now - live.downSince)} after the first alert.`,
      });
    }

    if (live.failingSince !== 0 && live.failingReminded === 0 && now - live.failingSince >= REMIND_FAILING_AFTER) {
      live.failingReminded = now;
      await this.post(live, {
        where: "failing",
        text: [
          `${live.host}: hachiko runs have been failing for ${span(now - live.failingSince)}.`,
          live.failingReason,
        ].join("\n"),
      });
    }

    await this.settle(live);
  }

  // The whole silence rather than the time since the alert, because what Tim wants from a
  // recovery is how long the Mac was gone, and the alert went out a grace into that.
  private async recover(live: Live, silence: number): Promise<void> {
    if (live.downSince === 0) {
      return;
    }

    live.downSince = 0;
    live.downReminded = 0;

    await this.post(live, {
      where: "outage",
      text: [`${live.host} is checking in again after ${span(silence)}.`, live.summary].join("\n"),
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

    await this.post(live, {
      where: "failing",
      text: [`${live.host}: hachiko runs are clean again after ${span(failing)}.`, live.summary].join("\n"),
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
      // A title is a post to open rather than one to go back into, which is what makes a new
      // outage a new post under a thread id this object is still holding.
      const landed = await postToForum(this.env.DISCORD_WEBHOOK_URL, {
        content: message.text,
        threadId: message.title === undefined ? thread : "",
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

  private async settle(live: Live): Promise<void> {
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

export function summarize(checkin: Checkin): string {
  const parts: string[] = [];

  if (checkin.free_gb !== undefined) {
    parts.push(`${checkin.free_gb} GB free`);
  }
  if (checkin.open_incidents !== undefined) {
    parts.push(checkin.open_incidents === 0 ? "no open incidents" : `${checkin.open_incidents} open incident(s)`);
  }
  if (checkin.hot_processes !== undefined) {
    parts.push(`${checkin.hot_processes} hot`);
  }

  return parts.join(", ");
}

// Tim's own zone, because the only person who reads these is in it and a time he has to
// convert is a time he misreads at four in the morning.
export function london(at: number): string {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: "Europe/London",
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(at));
}

export function span(ms: number): string {
  const minutes = Math.round(ms / 60_000);
  if (minutes < 1) {
    return "less than a minute";
  }
  if (minutes < 60) {
    return `${minutes} min`;
  }
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

function iso(at: number): string | null {
  return at === 0 ? null : new Date(at).toISOString();
}
