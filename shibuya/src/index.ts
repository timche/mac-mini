// shibuya is hachiko's dead man's switch: the station it turns up at every five minutes.
// hachiko watches the Mac, and nothing watches hachiko — a LaunchAgent that never loaded, a
// Mac that lost power, a sweep that hangs on a mount are all the same silence, and silence
// is what a watch cannot report about itself. So the check-in happens here, off the machine
// entirely, and a quarter of an hour without one is a message in Discord.
//
// Named for the station Hachikō waited at.

import { redact } from "./discord";
import type { Env } from "./env";
import type { Checkin, Switch } from "./switch";

export { Switch } from "./switch";

// A payload from hachiko is a few dozen bytes. Anything else arriving at this hostname is
// not hachiko, and reading it is the one thing a free plan is spent on.
const BODY_LIMIT = 4096;

// The host names the Durable Object, so it reaches a storage key and is checked like one.
const HOST = /^[a-z0-9-]{1,32}$/;

const REASON_LIMIT = 300;
const VERSION_LIMIT = 64;

// What the Mac calls itself, which is what Tim calls it: a title's worth and no more, since
// it is the first thing in a lead line and in a forum post's name.
const DISPLAY_LIMIT = 40;

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    // No data and no token: something has to answer a browser, and the answer is the name.
    if (url.pathname === "/") {
      return text("shibuya\n");
    }

    if (!authorized(request, env.PING_TOKEN)) {
      return text("unauthorized\n", 401);
    }

    const route = `${request.method} ${url.pathname}`;

    if (route === "GET /status") {
      const host = url.searchParams.get("host") ?? "";
      if (!HOST.test(host)) {
        return text("host\n", 400);
      }

      const status = await reached(env, () => switchFor(env, host).status(host));
      return status.ok ? json(status.value) : unavailable();
    }

    if (route === "DELETE /host") {
      const host = url.searchParams.get("host") ?? "";
      if (!HOST.test(host)) {
        return text("host\n", 400);
      }

      const forgotten = await reached(env, () => switchFor(env, host).forget());
      if (!forgotten.ok) {
        return unavailable();
      }
      return forgotten.value ? text(`forgot ${host}\n`) : text("unknown host\n", 404);
    }

    if (route !== "POST /ping" && route !== "POST /fail") {
      return text("not found\n", 404);
    }

    const body = await read(request);
    if (!body.ok) {
      return text(`${body.why}\n`, body.why === "too big" ? 413 : 400);
    }

    const checkin = validate(body.payload, route === "POST /fail");
    if (typeof checkin === "string") {
      return text(`${checkin}\n`, 400);
    }

    const host = switchFor(env, checkin.host);
    const taken = await reached(env, () => (route === "POST /fail" ? host.fail(checkin) : host.ping(checkin)));
    if (!taken.ok) {
      return unavailable();
    }
    return text("ok\n");
  },
} satisfies ExportedHandler<Env>;

function switchFor(env: Env, host: string): DurableObjectStub<Switch> {
  return env.SWITCH.get(env.SWITCH.idFromName(host));
}

type Reached<T> = { ok: true; value: T } | { ok: false };

// A call into the object fails for reasons that are neither the caller's fault nor a bug
// here. A deploy is the one that bit: for the minutes a new version is reaching every
// location, this Worker can be the new one and the object it calls still the old class, and
// a method this version calls does not exist over there. Unhandled, that is Cloudflare's own
// 1101 page — an uncaught exception, with nothing in it to act on and no reason given to try
// again. A 503 says the one useful thing, and it is what hachiko already treats as a
// check-in that did not land and retries on its next sweep.
async function reached<T>(env: Env, work: () => Promise<T>): Promise<Reached<T>> {
  try {
    return { ok: true, value: await work() };
  } catch (err) {
    // An error out of the object is the object's own text, and the object talks to Discord.
    const why = redact(err instanceof Error ? err.message : String(err), env.DISCORD_WEBHOOK_URL);
    console.log(`shibuya: the switch did not answer: ${why}`);
    return { ok: false };
  }
}

function unavailable(): Response {
  return text("try again\n", 503);
}

// Constant-time, because the alternative is a token recovered one character at a time by
// whoever finds the hostname. A wrong length is the one thing that cannot be hidden, since
// timingSafeEqual refuses to compare two buffers of different sizes.
function authorized(request: Request, token: string): boolean {
  const prefix = "Bearer ";
  const header = request.headers.get("authorization") ?? "";

  if (token === "" || !header.startsWith(prefix)) {
    return false;
  }

  const encoder = new TextEncoder();
  const offered = encoder.encode(header.slice(prefix.length));
  const expected = encoder.encode(token);

  if (offered.byteLength !== expected.byteLength) {
    return false;
  }
  return crypto.subtle.timingSafeEqual(offered, expected);
}

type Body = { ok: true; payload: unknown } | { ok: false; why: "too big" | "malformed" };

async function read(request: Request): Promise<Body> {
  const declared = Number(request.headers.get("content-length"));
  if (Number.isFinite(declared) && declared > BODY_LIMIT) {
    return { ok: false, why: "too big" };
  }

  const raw = await request.arrayBuffer();
  if (raw.byteLength > BODY_LIMIT) {
    return { ok: false, why: "too big" };
  }

  try {
    return { ok: true, payload: JSON.parse(new TextDecoder().decode(raw)) as unknown };
  } catch {
    return { ok: false, why: "malformed" };
  }
}

// What comes back is either a Checkin or the name of the field that was wrong, which is all
// the detail an answer carries: everything here is hachiko's own payload, so a message
// explaining it is a message for whoever is guessing at the shape.
function validate(payload: unknown, failing: boolean): Checkin | string {
  if (typeof payload !== "object" || payload === null || Array.isArray(payload)) {
    return "payload";
  }

  const fields = payload as Record<string, unknown>;
  if (typeof fields.host !== "string" || !HOST.test(fields.host)) {
    return "host";
  }

  const checkin: Checkin = { host: fields.host };

  // A display name that cleans away to nothing is one that was not sent: the slug stands in
  // for it, rather than a message about a Mac with no name.
  if (fields.display !== undefined) {
    if (typeof fields.display !== "string") {
      return "display";
    }
    const display = clean(fields.display, DISPLAY_LIMIT);
    if (display !== "") {
      checkin.display = display;
    }
  }

  const free = count(fields.free_gb);
  if (free === "bad") {
    return "free_gb";
  }
  if (free !== undefined) {
    checkin.free_gb = free;
  }

  const incidents = count(fields.open_incidents);
  if (incidents === "bad") {
    return "open_incidents";
  }
  if (incidents !== undefined) {
    checkin.open_incidents = incidents;
  }

  const hot = count(fields.hot_processes);
  if (hot === "bad") {
    return "hot_processes";
  }
  if (hot !== undefined) {
    checkin.hot_processes = hot;
  }

  if (fields.version !== undefined) {
    if (typeof fields.version !== "string") {
      return "version";
    }
    checkin.version = clean(fields.version, VERSION_LIMIT);
  }

  if (failing) {
    if (fields.reason !== undefined && typeof fields.reason !== "string") {
      return "reason";
    }
    checkin.reason = clean(typeof fields.reason === "string" ? fields.reason : "", REASON_LIMIT);
  }

  return checkin;
}

function count(value: unknown): number | "bad" | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return "bad";
  }
  return value;
}

// A reason is written by a sweep, and a sweep names paths whatever filled the disk called
// them. A newline there is a line of its own in Discord and an escape is one a terminal
// reading the log back would act on.
function clean(value: string, limit: number): string {
  const stripped = value.replace(/[\p{Cc}\p{Cf}]/gu, " ").trim();
  return stripped.length <= limit ? stripped : `${stripped.slice(0, limit - 1)}…`;
}

function text(body: string, status = 200): Response {
  return new Response(body, { status, headers: { "content-type": "text/plain; charset=utf-8" } });
}

function json(body: unknown): Response {
  return Response.json(body);
}
