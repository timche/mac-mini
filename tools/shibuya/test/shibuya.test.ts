import { env, runDurableObjectAlarm, runInDurableObject, SELF } from "cloudflare:test";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// The pool runs the Worker in this isolate, so this is the same class object the runtime
// instantiates the Durable Object from — which is what lets a test make one of its methods
// throw the way a rollout does.
import { Switch } from "../src/switch";

// The two the test config binds. The bot token is the one string that may never appear in
// anything this Worker writes, so it is here to be asserted against as well as used.
const TOKEN = "a-token-only-the-tests-know";
const BOT = "a-bot-token-only-the-tests-know";

// A Discord of the tests' own, holding the forum's posts the way the real one does, so what
// shibuya finds open, adopts, opens and closes is asserted against state rather than against
// a script of replies.
const FORUM = env.DISCORD_FORUM;
const GUILD = "50";
const SELF_ID = "900";
const SOMEONE_ELSE = "901";
const OPEN = "1";
const RESOLVED = "2";

interface Call {
  method: string;
  path: string;
  body: unknown;
}

interface Post {
  id: string;
  name: string;
  owner: string;
  tags: string[];
  archived: boolean;
}

interface Request_ {
  post: string;
  opened: boolean;
  fields: {
    content?: string;
    thread_name?: string;
    allowed_mentions?: unknown;
    applied_tags?: string[];
  };
}

interface Reply {
  status: number;
  // The id a post this reply opens gets.
  id?: string;
  throws?: string;
}

let api: Call[] = [];
let posts = new Map<string, Post>();
// Checked before the fake answers anything, for a test that needs Discord to misbehave.
let answer: (call: Call) => Response | undefined | Promise<Response | undefined> = () => undefined;

function existing(id: string, name = "a post", tags = [OPEN], owner = SELF_ID, archived = false): void {
  posts.set(id, { id, name, owner, tags, archived });
}

function gone(): Response {
  return Response.json({ code: 10003, message: "Unknown Channel" }, { status: 404 });
}

function serve(call: Call, sent: Request_[], replies: Reply[], next: () => string): Response {
  const path = call.path;
  const body = (call.body ?? {}) as Record<string, unknown>;

  if (call.method === "GET" && path === "/users/@me") {
    return Response.json({ id: SELF_ID, bot: true });
  }
  if (call.method === "GET" && path === `/channels/${FORUM}`) {
    return Response.json({
      id: FORUM,
      type: 15,
      guild_id: GUILD,
      available_tags: [
        { id: OPEN, name: "Open" },
        { id: RESOLVED, name: "Resolved" },
      ],
    });
  }
  if (call.method === "GET" && path === `/guilds/${GUILD}/threads/active`) {
    return Response.json({
      threads: [...posts.values()]
        .filter((post) => !post.archived)
        .map((post) => ({
          id: post.id,
          name: post.name,
          parent_id: FORUM,
          owner_id: post.owner,
          applied_tags: post.tags,
        })),
    });
  }

  if (call.method === "POST" && path === `/channels/${FORUM}/threads`) {
    const message = (body.message ?? {}) as Record<string, unknown>;
    const reply = replies.shift() ?? { status: 200 };
    const request: Request_ = {
      post: "",
      opened: true,
      fields: {
        content: message.content as string,
        allowed_mentions: message.allowed_mentions,
        thread_name: body.name as string,
        applied_tags: body.applied_tags as string[] | undefined,
      },
    };
    sent.push(request);
    if (reply.throws !== undefined) {
      throw new TypeError(reply.throws);
    }
    if (reply.status !== 200) {
      return new Response("{}", { status: reply.status });
    }
    request.post = reply.id ?? next();
    existing(request.post, body.name as string, (body.applied_tags as string[] | undefined) ?? []);
    return Response.json({ id: request.post, parent_id: FORUM });
  }

  const into = /^\/channels\/(\d+)\/messages$/.exec(path);
  if (call.method === "POST" && into !== null) {
    const reply = replies.shift() ?? { status: 200 };
    sent.push({
      post: into[1]!,
      opened: false,
      fields: { content: body.content as string, allowed_mentions: body.allowed_mentions },
    });
    if (reply.throws !== undefined) {
      throw new TypeError(reply.throws);
    }
    const post = posts.get(into[1]!);
    if (post === undefined || reply.status === 404) {
      return gone();
    }
    if (reply.status !== 200) {
      return new Response("{}", { status: reply.status });
    }
    post.archived = false;
    return Response.json({ id: "5", channel_id: post.id });
  }

  const channel = /^\/channels\/(\d+)$/.exec(path);
  if (channel !== null) {
    const post = posts.get(channel[1]!);
    if (post === undefined) {
      return gone();
    }
    if (call.method === "PATCH") {
      post.tags = (body.applied_tags as string[] | undefined) ?? post.tags;
      post.archived = body.archived === true;
    }
    return Response.json({ id: post.id, parent_id: FORUM, applied_tags: post.tags });
  }

  return new Response("{}", { status: 404 });
}

// The pool runs the tests in the same isolate as the Worker, so a stubbed global fetch is
// what the Worker's own fetch resolves to. What comes back is the list of messages sent, in
// order, each with the post it went into; every call is in `api` as well. Each message uses
// up the next reply, and one with none left succeeds.
function discord(...replies: Reply[]): Request_[] {
  const sent: Request_[] = [];
  const queue = [...replies];
  let id = 4242;
  posts = new Map();

  vi.stubGlobal("fetch", async (input: unknown, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input));
    const call: Call = {
      method: init?.method ?? "GET",
      path: url.pathname.replace(/^\/api\/v10/, ""),
      body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
    };
    expect(url.origin).toBe("https://discord.com");
    expect(new Headers(init?.headers).get("authorization")).toBe(`Bot ${BOT}`);
    api.push(call);

    const override = await answer(call);
    if (override !== undefined) {
      return override;
    }
    return serve(call, sent, queue, () => String(id++));
  });

  return sent;
}

function patches(): Call[] {
  return api.filter((call) => call.method === "PATCH");
}

// Every test spies on console.log, to assert the token reaches no log, so the one test that
// prints for a reader to read takes the real one before any of that happens.
const print = console.log.bind(console);

const HOST = "mac-mini";

// The golden messages are rendered for the Mac as it really reports itself, so what a
// reviewer reads out of `npm test` is what Discord would show.
const GOLDEN_HOST = "tims-mac-mini";
const DISPLAY = "Mac mini";
const BUILD = "1a2b3c4d5e6f";
const READING = "1 GB free, no open incidents, nothing busy";

const SUMMARY = "787 GB free, no open incidents, nothing busy";

function switchFor(host = HOST) {
  return env.SWITCH.get(env.SWITCH.idFromName(host));
}

async function seed(live: Record<string, unknown>, host = HOST, alarmAt = Date.now() + 600_000): Promise<void> {
  await runInDurableObject(switchFor(host), async (_instance, state) => {
    await state.storage.put("live", live);
    await state.storage.setAlarm(alarmAt);
  });
}

async function stored(host = HOST): Promise<{ live: Record<string, unknown>; alarm: number | null }> {
  return runInDurableObject(switchFor(host), async (_instance, state) => ({
    live: (await state.storage.get<Record<string, unknown>>("live")) ?? {},
    alarm: await state.storage.getAlarm(),
  }));
}

function ping(body: unknown, token = TOKEN, path = "/ping"): Promise<Response> {
  return SELF.fetch(`https://shibuya.test${path}`, {
    method: "POST",
    headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

function forget(host: string, token = TOKEN, method = "DELETE"): Promise<Response> {
  return SELF.fetch(`https://shibuya.test/host?host=${encodeURIComponent(host)}`, {
    method,
    headers: { authorization: `Bearer ${token}` },
  });
}

const checkin = { host: HOST, free_gb: 787, open_incidents: 0, hot_processes: 0, version: "abc1234" };

// The reason as Discord would show it, out of the line that carries it.
function why(content: string): string {
  const line = content.split("\n").find((candidate) => candidate.startsWith("**Why:** ")) ?? "";
  return line.slice("**Why:** ".length);
}

let logged: string[] = [];

beforeEach(async () => {
  // A Durable Object outlives the test that made it, so one left down or failing would be
  // that test deciding this one's assertions.
  for (const host of [HOST, "other-mac", GOLDEN_HOST]) {
    await runInDurableObject(switchFor(host), async (instance, state) => {
      await state.storage.deleteAll();
      await state.storage.deleteAlarm();
      // And what it remembers of Discord, which is the forum one test described to it.
      (instance as unknown as { client: unknown }).client = undefined;
    });
  }

  logged = [];
  api = [];
  posts = new Map();
  answer = () => undefined;
  vi.spyOn(console, "log").mockImplementation((...args: unknown[]) => {
    logged.push(args.map(String).join(" "));
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it("answers the name at the root with no token and no data", async () => {
  const response = await SELF.fetch("https://shibuya.test/");

  expect(response.status).toBe(200);
  expect(await response.text()).toBe("shibuya\n");
});

it("refuses a missing, a wrong and a nearly-right token", async () => {
  discord();

  expect((await ping(checkin, "")).status).toBe(401);
  expect((await ping(checkin, "not-the-token")).status).toBe(401);
  expect((await ping(checkin, `${TOKEN}x`)).status).toBe(401);
  expect((await ping(checkin)).status).toBe(200);

  const bare = await SELF.fetch("https://shibuya.test/ping", { method: "POST", body: "{}" });
  expect(bare.status).toBe(401);
});

it("says nothing but the status when a token is wrong", async () => {
  const response = await ping(checkin, "not-the-token");

  expect(await response.text()).toBe("unauthorized\n");
});

it("refuses a body over four kilobytes", async () => {
  const fat = JSON.stringify({ host: HOST, version: "x".repeat(5000) });

  expect((await ping(fat)).status).toBe(413);
});

it("refuses a payload it cannot read or trust", async () => {
  expect((await ping("not json at all")).status).toBe(400);
  expect((await ping([1, 2, 3])).status).toBe(400);
  expect((await ping({ host: "Mac Mini" })).status).toBe(400);
  expect((await ping({ host: "" })).status).toBe(400);
  expect((await ping({ host: "x".repeat(33) })).status).toBe(400);
  expect((await ping({ host: HOST, free_gb: "lots" })).status).toBe(400);
  expect((await ping({ host: HOST, open_incidents: -1 })).status).toBe(400);
  expect((await ping({ host: HOST, hot_processes: true })).status).toBe(400);
});

it("arms the deadline a check-in's grace ahead of it, and remembers the reading", async () => {
  discord();
  const before = Date.now();

  expect((await ping(checkin)).status).toBe(200);

  const { live, alarm } = await stored();
  expect(alarm).not.toBeNull();
  expect(alarm! - before).toBeGreaterThanOrEqual(14 * 60_000);
  expect(alarm! - before).toBeLessThanOrEqual(16 * 60_000);
  expect(live.summary).toBe(SUMMARY);
  expect(live.version).toBe("abc1234");
});

it("opens one forum post, tagged Open, when the deadline passes with no check-in", async () => {
  const sent = discord();
  const lastPing = Date.now() - 20 * 60_000;
  await seed({ host: HOST, lastPing, summary: SUMMARY, version: "abc1234" });

  expect(await runDurableObjectAlarm(switchFor())).toBe(true);

  expect(sent).toHaveLength(1);
  expect(sent[0]!.opened).toBe(true);
  expect(sent[0]!.fields.thread_name).toMatch(/^🔴 mac-mini offline · /);
  expect(sent[0]!.fields.thread_name!.length).toBeLessThanOrEqual(100);
  expect(sent[0]!.fields.applied_tags).toEqual([OPEN]);
  expect(sent[0]!.fields.content).toContain(`**Last reading:** ${SUMMARY}`);
  expect(sent[0]!.fields.content).toMatch(/^🔴 mac-mini has gone quiet — no check-in for 20 minutes$/m);
  expect(sent[0]!.fields.content).toContain("-# mac-mini · hachiko build abc1234");

  const { live } = await stored();
  expect(live.downSince).toBeGreaterThan(0);
  expect(live.outageThread).toBe("4242");
});

it("leaves the build line out of an outage alert when no check-in has named one", async () => {
  const sent = discord();
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  expect(await runDurableObjectAlarm(switchFor())).toBe(true);

  expect(sent[0]!.fields.content).toContain(SUMMARY);
  expect(sent[0]!.fields.content).toContain("-# mac-mini");
  expect(sent[0]!.fields.content).not.toContain("hachiko");
});

it("asks Discord nothing on a check-in that changes nothing", async () => {
  discord();

  await ping(checkin);
  await ping(checkin);
  await ping({ ...checkin, reason: "no process sample" }, TOKEN, "/fail");
  api = [];
  await ping({ ...checkin, reason: "no process sample" }, TOKEN, "/fail");

  expect(api).toEqual([]);
});

it("does not post a second time on the next alarm of the same outage", async () => {
  const sent = discord();
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());
  expect(sent).toHaveLength(1);

  await seed({ ...(await stored()).live });
  expect(await runDurableObjectAlarm(switchFor())).toBe(true);

  expect(sent).toHaveLength(1);
  expect((await stored()).live.queue).toEqual([]);
});

// What a Durable Objects storage incident did: the alarm posted, its write failed, Cloudflare
// retried the alarm within seconds, and every retry found the host still up and posted again.
it("opens one post when the alarm's write fails and the alarm is retried", async () => {
  const sent = discord();
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runInDurableObject(switchFor(), async (_instance, state) => {
    vi.spyOn(state.storage, "put").mockRejectedValueOnce(new Error("Durable Object storage is unavailable"));
  });
  await expect(runDurableObjectAlarm(switchFor())).rejects.toThrow("storage is unavailable");
  vi.restoreAllMocks();
  expect(sent).toHaveLength(0);

  // Cloudflare retries an alarm that threw; the test pool runs it once and clears it.
  await seed({ ...(await stored()).live });
  expect(await runDurableObjectAlarm(switchFor())).toBe(true);

  expect(sent).toHaveLength(1);
  expect(sent[0]!.fields.thread_name).toMatch(/^🔴 mac-mini offline · /);
  const { live } = await stored();
  expect(live.outageThread).toBe("4242");
  expect(live.queue).toEqual([]);
});

// The other half of the same incident: the post went out and the record of it did not, so
// the object holds no thread and no outage. The forum still has the post, open, and that is
// what decides there is one already.
it("posts into the open post it finds in the forum rather than opening a second", async () => {
  const sent = discord();
  existing("3000", "🔴 mac-mini offline · Thu 18:06");
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());

  expect(sent).toHaveLength(1);
  expect(sent[0]!.opened).toBe(false);
  expect(sent[0]!.post).toBe("3000");
  expect((await stored()).live.outageThread).toBe("3000");
});

it("adopts only its own open post for this Mac and this kind", async () => {
  const sent = discord();
  existing("3001", "🔴 mac-mini offline · Thu 18:06", [OPEN], SOMEONE_ELSE);
  existing("3002", "🔴 mac-mini offline · Thu 18:06", [RESOLVED]);
  existing("3003", "🔴 mac-mini offline · Thu 18:06", [OPEN], SELF_ID, true);
  existing("3004", "🔴 other-mac offline · Thu 18:06");
  existing("3005", "⚠️ mac-mini: hachiko's checks are failing · Thu 18:06");
  existing("3006", "🔴 mac-mini offline again · Thu 18:06");
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());

  expect(sent).toHaveLength(1);
  expect(sent[0]!.opened).toBe(true);
  expect(sent[0]!.post).toBe("4242");
});

it("still opens the alert when the forum's open posts cannot be read", async () => {
  const sent = discord();
  existing("3000", "🔴 mac-mini offline · Thu 18:06");
  answer = (call) =>
    call.path.endsWith("/threads/active") ? new Response("{}", { status: 500 }) : undefined;
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());

  expect(sent).toHaveLength(1);
  expect(sent[0]!.opened).toBe(true);
  expect(logged.join("\n")).toContain("could not be read");
  expect((await stored()).live.queue).toEqual([]);
});

// The alert is held back by Discord, the Mac comes back meanwhile, and the recovery has to
// wait for the post the alert opens rather than go out ahead of it into none.
it("sends a recovery after the alert it answers, into that alert's post", async () => {
  const sent = discord({ status: 500 });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());
  expect(sent).toHaveLength(1);

  await ping(checkin);
  expect(sent).toHaveLength(1);
  expect((await stored()).live.queue).toHaveLength(2);

  const held = (await stored()).live;
  const queue = (held.queue as { at: number }[]).map((message) => ({ ...message, at: Date.now() - 1 }));
  await seed({ ...held, queue });
  await runDurableObjectAlarm(switchFor());

  expect(sent).toHaveLength(3);
  expect(sent[1]!.opened).toBe(true);
  expect(sent[1]!.fields.content).toContain("has gone quiet");
  expect(sent[2]!.post).toBe(sent[1]!.post);
  expect(sent[2]!.fields.content).toContain("is back");
  expect((await stored()).live.queue).toEqual([]);
});

// The forum reads as a list of what is still running, the same as hachiko's own posts: the
// message that ends an incident closes its post Resolved, after the message and never before.
it("posts the recovery into the outage's own post and closes it Resolved", async () => {
  const sent = discord();
  let messagesBeforeClose = -1;
  answer = (call) => {
    if (call.method === "PATCH") {
      messagesBeforeClose = sent.length;
    }
    return undefined;
  };
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());
  expect(patches()).toEqual([]);
  posts.get("4242")!.tags.push("7");

  expect((await ping(checkin)).status).toBe(200);

  expect(sent).toHaveLength(2);
  expect(sent[1]!.opened).toBe(false);
  expect(sent[1]!.post).toBe("4242");
  expect(sent[1]!.fields.content).toContain("🟢 mac-mini is back — it checked in after 20 minutes of silence");
  expect(sent[1]!.fields.content).toContain(`**Reading now:** ${SUMMARY}`);

  expect(patches()).toHaveLength(1);
  expect(patches()[0]!.path).toBe("/channels/4242");
  // Open off, Resolved on, and whatever else Tim tagged it with left alone.
  expect(patches()[0]!.body).toEqual({ applied_tags: ["7", RESOLVED], archived: true });
  expect(messagesBeforeClose).toBe(2);
  expect((await stored()).live.downSince).toBe(0);
});

// Whatever got out twice is closed with the incident, so nothing is left Open behind it.
it("closes every open post of the outage when the Mac is back", async () => {
  const sent = discord();
  existing("3000", "🔴 mac-mini offline · Thu 18:06");
  existing("3001", "🔴 mac-mini offline · Thu 18:07");
  existing("3002", "⚠️ mac-mini: hachiko's checks are failing · Thu 18:07");
  await seed({
    host: HOST,
    lastPing: Date.now() - 20 * 60_000,
    summary: SUMMARY,
    downSince: Date.now() - 5 * 60_000,
    outageThread: "3001",
  });

  await ping(checkin);

  expect(sent).toHaveLength(1);
  expect(sent[0]!.post).toBe("3001");
  expect(patches().map((call) => call.path).sort()).toEqual(["/channels/3000", "/channels/3001"]);
  expect(posts.get("3002")!.archived).toBe(false);
});

// After the record of a post was lost: the recovery goes into the post the forum has open.
it("finds the outage's post in the forum when it has none of its own", async () => {
  const sent = discord();
  existing("3000", "🔴 mac-mini offline · Thu 18:06");
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY, downSince: Date.now() - 60_000 });

  await ping(checkin);

  expect(sent).toHaveLength(1);
  expect(sent[0]!.post).toBe("3000");
  expect(posts.get("3000")!.archived).toBe(true);
  expect(posts.get("3000")!.tags).toEqual([RESOLVED]);
});

it("opens a failing post tagged Open and closes it Resolved on a clean run", async () => {
  const sent = discord();

  await ping({ ...checkin, reason: "there was no process sample" }, TOKEN, "/fail");
  expect(sent[0]!.fields.applied_tags).toEqual([OPEN]);
  expect(sent[0]!.fields.thread_name).toMatch(/^⚠️ mac-mini: hachiko's checks are failing · /);

  await ping(checkin);
  expect(patches().map((call) => call.path)).toEqual(["/channels/4242"]);
  expect(posts.get("4242")!.tags).toEqual([RESOLVED]);
});

it("posts a reminder into the post already open, untagged", async () => {
  const sent = discord();
  existing("4242", "🔴 mac-mini offline · Thu 18:06");
  await seed({
    host: HOST,
    lastPing: Date.now() - 7 * 60 * 60_000,
    summary: SUMMARY,
    downSince: Date.now() - 6 * 60 * 60_000 - 60_000,
    outageThread: "4242",
  });

  await runDurableObjectAlarm(switchFor());
  expect(sent).toHaveLength(1);
  expect(sent[0]!.post).toBe("4242");
  expect(patches()).toEqual([]);
});

// Without a token nothing reaches Discord at all, so the message waits in the queue and is
// retried rather than being dropped.
it("keeps a message to retry when there is no bot token", async () => {
  const sent = discord();
  await runInDurableObject(switchFor(), (instance) => {
    (instance as unknown as { env: Record<string, unknown> }).env.DISCORD_BOT_TOKEN = "";
  });

  try {
    await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });
    await runDurableObjectAlarm(switchFor());
  } finally {
    await runInDurableObject(switchFor(), (instance) => {
      (instance as unknown as { env: Record<string, unknown> }).env.DISCORD_BOT_TOKEN = BOT;
    });
  }

  expect(sent).toHaveLength(0);
  expect(api).toEqual([]);
  expect(logged.join("\n")).toContain("no DISCORD_BOT_TOKEN");
  expect((await stored()).live.queue).toHaveLength(1);
});

// The message is in the post already, so a label that would not go on is said and dropped:
// sending the recovery again would be the duplicate this is meant to prevent.
it("logs a post it could not close and does not send the message again", async () => {
  const sent = discord();
  answer = (call) => {
    if (call.method === "PATCH") {
      throw new TypeError(`fetch failed with Bot ${BOT}`);
    }
    return undefined;
  };
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());
  await ping(checkin);

  expect(sent).toHaveLength(2);
  const { live } = await stored();
  expect(live.queue).toEqual([]);
  expect(live.outageThread).toBe("4242");
  expect(await runDurableObjectAlarm(switchFor())).toBe(true);
  expect(sent).toHaveLength(2);

  const log = logged.join("\n");
  expect(log).toContain("could not be closed");
  expect(log).not.toContain(BOT);
  expect(log).toContain("the bot token");
});

it("opens the post untagged when the forum cannot be read", async () => {
  const sent = discord();
  answer = (call) =>
    call.method === "GET" && call.path === `/channels/${FORUM}` ? new Response("{}", { status: 403 }) : undefined;
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());

  expect(sent).toHaveLength(1);
  expect(sent[0]!.fields.applied_tags).toBeUndefined();
  expect(logged.join("\n")).toContain("untagged");
  expect((await stored()).live.outageThread).toBe("4242");
});

it("opens one post for a failing streak, posts nothing for the next fail, and clears it on a clean run", async () => {
  const sent = discord();

  const first = await ping({ ...checkin, reason: "there was no process sample" }, TOKEN, "/fail");
  expect(first.status).toBe(200);
  expect(sent).toHaveLength(1);
  expect(sent[0]!.opened).toBe(true);
  expect(sent[0]!.fields.thread_name).toMatch(/^⚠️ mac-mini: hachiko's checks are failing · /);
  expect(sent[0]!.fields.content).toContain("**Why:** there was no process sample");

  await ping({ ...checkin, reason: "there was no process sample" }, TOKEN, "/fail");
  expect(sent).toHaveLength(1);

  await ping(checkin);
  expect(sent).toHaveLength(2);
  expect(sent[1]!.post).toBe("4242");
  expect(sent[1]!.fields.content).toContain("hachiko's checks are finishing again after");

  const { live } = await stored();
  expect(live.failingSince).toBe(0);
});

it("counts a fail as a check-in, so a failing Mac is not also an offline one", async () => {
  discord();
  const before = Date.now();

  await ping({ ...checkin, reason: "no process sample" }, TOKEN, "/fail");

  const { live, alarm } = await stored();
  expect(live.lastPing).toBeGreaterThanOrEqual(before);
  expect(alarm! - before).toBeLessThanOrEqual(16 * 60_000);
});

it("strips the control characters out of a reason and caps it", async () => {
  const sent = discord();

  await ping({ ...checkin, reason: `ps failed\n\u001b and said so ${"x".repeat(400)}` }, TOKEN, "/fail");

  const reason = why(sent[0]!.fields.content!);
  expect(reason).not.toMatch(/[\u0000-\u001f]/);
  expect(reason.startsWith("ps failed   and said so xxx")).toBe(true);
  expect(reason.length).toBeLessThanOrEqual(300);
});

// A post Tim deleted is gone from the forum too, so the message opens one of its own, under
// the incident's own name, and closes it if the message is the one that ends it.
it("opens a new post when the one it recorded is gone", async () => {
  const sent = discord();
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY, failingThread: "7", failingSince: 1 });

  await ping(checkin);

  expect(sent).toHaveLength(2);
  expect(sent[0]!.post).toBe("7");
  expect(sent[1]!.opened).toBe(true);
  expect(sent[1]!.fields.thread_name).toMatch(/^⚠️ mac-mini: hachiko's checks are failing · /);
  expect((await stored()).live.failingThread).toBe("4242");
  expect(posts.get("4242")!.archived).toBe(true);
});

it("retries an outage alert Discord refused, soon and then less often", async () => {
  const sent = discord({ status: 500 }, { status: 500 });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });
  const before = Date.now();

  await runDurableObjectAlarm(switchFor());

  const first = await stored();
  expect(sent).toHaveLength(1);
  expect(first.live.queue).toHaveLength(1);
  expect(first.alarm! - before).toBeGreaterThan(0);
  expect(first.alarm! - before).toBeLessThanOrEqual(61_000);

  // The minute having passed, which is the only part of this a test cannot wait for.
  const due = (first.live.queue as { at: number }[]).map((message) => ({ ...message, at: Date.now() - 1 }));
  await seed({ ...first.live, queue: due });
  await runDurableObjectAlarm(switchFor());

  const second = await stored();
  expect(sent).toHaveLength(2);
  expect((second.live.queue as { attempt: number }[])[0]!.attempt).toBe(2);
  expect(second.alarm! - before).toBeGreaterThan(60_000);
});

// A reason is written by a sweep, which names paths and quotes log lines chosen by whatever
// filled the disk — so a worker writing "@everyone" into the log it is flooding would page
// the whole server from inside an outage alert. Both shapes a message can take are exercised,
// because the one that forgot the flag is the one that would be reached. The text is not
// scrubbed and should not be: what Tim reads is what the sweep found.
it("lets nothing it posts mention anybody", async () => {
  const sent = discord();

  await ping({ ...checkin, reason: "@everyone there was no process sample" }, TOKEN, "/fail");
  await ping(checkin);

  expect(sent).toHaveLength(2);
  expect(sent[0]!.opened).toBe(true);
  expect(sent[1]!.opened).toBe(false);

  for (const request of sent) {
    expect(request.fields.allowed_mentions).toEqual({ parse: [] });
  }
  expect(sent[0]!.fields.content).toContain("@everyone there was no process sample");
});

it("never writes the bot token anywhere, whatever Discord says about it", async () => {
  discord({ status: 500, throws: `Network connection lost: Bot ${BOT}` });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());

  expect(logged.length).toBeGreaterThan(0);
  expect(logged.join("\n")).not.toContain(BOT);
  expect(logged.join("\n")).toContain("the bot token");
});

it("reports where a host stands, to a caller with the token", async () => {
  discord();
  await ping(checkin);

  const refused = await SELF.fetch(`https://shibuya.test/status?host=${HOST}`);
  expect(refused.status).toBe(401);

  const response = await SELF.fetch(`https://shibuya.test/status?host=${HOST}`, {
    headers: { authorization: `Bearer ${TOKEN}` },
  });
  expect(response.status).toBe(200);

  const status = (await response.json()) as Record<string, unknown>;
  expect(status.host).toBe(HOST);
  expect(status.display).toBe("");
  expect(status.state).toBe("up");
  expect(status.last_summary).toBe(SUMMARY);
  expect(status.down_since).toBeNull();
  expect(status.failing_since).toBeNull();
  expect(String(status.last_ping)).toMatch(/^\d{4}-\d{2}-\d{2}T/);
});

it("keeps one object per host", async () => {
  discord();

  await ping(checkin);
  await ping({ host: "other-mac", free_gb: 12 });

  const mine = await switchFor().status(HOST);
  const theirs = await switchFor("other-mac").status("other-mac");

  expect(mine.last_summary).toBe(SUMMARY);
  expect(theirs.last_summary).toBe("12 GB free");
});

it("refuses to forget a host without the token, and leaves it armed", async () => {
  discord();
  await ping(checkin);

  const bare = await SELF.fetch(`https://shibuya.test/host?host=${HOST}`, { method: "DELETE" });
  expect(bare.status).toBe(401);
  expect((await forget(HOST, "")).status).toBe(401);
  expect((await forget(HOST, "not-the-token")).status).toBe(401);

  expect((await stored()).alarm).not.toBeNull();
  expect((await stored()).live.lastPing).toBeGreaterThan(0);
});

it("refuses to forget a name it would not take as a host anywhere else", async () => {
  expect((await forget("")).status).toBe(400);
  expect((await forget("Mac Mini")).status).toBe(400);
  expect((await forget("x".repeat(33))).status).toBe(400);
});

it("forgets a host, taking its alarm and its state with it", async () => {
  discord();
  await ping(checkin);
  expect((await stored()).alarm).not.toBeNull();

  const response = await forget(HOST);
  expect(response.status).toBe(200);
  expect(await response.text()).toBe(`forgot ${HOST}\n`);

  expect((await stored()).live).toEqual({});
  expect((await stored()).alarm).toBeNull();
  expect(await runDurableObjectAlarm(switchFor())).toBe(false);

  const status = await SELF.fetch(`https://shibuya.test/status?host=${HOST}`, {
    headers: { authorization: `Bearer ${TOKEN}` },
  });
  expect(((await status.json()) as Record<string, unknown>).state).toBe("unknown");
});

it("lets no reminder follow a host forgotten while it was down", async () => {
  const sent = discord();
  const sevenHoursAgo = Date.now() - 7 * 60 * 60 * 1000;
  await seed({
    host: HOST,
    lastPing: sevenHoursAgo,
    summary: SUMMARY,
    downSince: sevenHoursAgo,
    outageThread: "4242",
  });

  expect((await forget(HOST)).status).toBe(200);

  // The reminder is six hours into an outage, so the one this object was holding is due.
  expect(await runDurableObjectAlarm(switchFor())).toBe(false);
  expect(sent).toHaveLength(0);
});

// The input gate opens while a handler awaits Discord, which is exactly when a DELETE gets
// in: the alert is posted, the object is emptied under it, and the alert's own settle() would
// put the record back and re-arm the deadline — so a host Tim had just forgotten would have a
// live deadline out of an object that no longer holds a thing.
it("lets a forget stand that landed while an alert was in flight", async () => {
  let release = (): void => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });

  discord();
  let posting = false;
  answer = async (call) => {
    if (call.method === "POST" && call.path === `/channels/${FORUM}/threads`) {
      posting = true;
      await held;
    }
    return undefined;
  };

  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  // Not awaited: the alarm is inside the fetch above, which is where the gate opens.
  const alarm = runDurableObjectAlarm(switchFor());
  await vi.waitUntil(() => posting);

  const response = await forget(HOST);
  expect(response.status).toBe(200);
  expect(await response.text()).toBe(`forgot ${HOST}\n`);

  release();
  expect(await alarm).toBe(true);

  expect((await stored()).live).toEqual({});
  expect((await stored()).alarm).toBeNull();
  expect(await runDurableObjectAlarm(switchFor())).toBe(false);

  const status = await SELF.fetch(`https://shibuya.test/status?host=${HOST}`, {
    headers: { authorization: `Bearer ${TOKEN}` },
  });
  expect(((await status.json()) as Record<string, unknown>).state).toBe("unknown");
});

// The count of forgets is in memory and is never cleared, so a sticky flag in its place
// would leave this object refusing to write anything ever again.
it("takes a host back after it was forgotten, on the same object", async () => {
  discord();

  await ping(checkin);
  expect((await forget(HOST)).status).toBe(200);
  expect((await ping(checkin)).status).toBe(200);

  const { live, alarm } = await stored();
  expect(live.summary).toBe(SUMMARY);
  expect(alarm).not.toBeNull();
});

// Observed on the deploy that added the route: for the minutes a new version is reaching
// every location, this Worker can be the new one and the object it calls still the old
// class, where the method does not exist. Unhandled that is Cloudflare's 1101 page, which
// says nothing and gives no reason to try again.
//
// workerd prints the object's own exception to stderr while this runs. That is the platform
// reporting what happened inside the object, which is what it does in production too; what
// is asserted here is the answer the caller gets instead of 1101.
it("answers 503 rather than an uncaught exception when the object will not answer", async () => {
  discord();

  // Thrown rather than a rejected promise handed to the mock, which workerd reports as an
  // unhandled rejection the moment it is made, before anything has awaited it.
  const why = `Durable Object class has no method forget: Bot ${BOT}`;
  const throwing = (): never => {
    throw new Error(why);
  };
  vi.spyOn(Switch.prototype, "forget").mockImplementation(throwing);
  vi.spyOn(Switch.prototype, "ping").mockImplementation(throwing);

  const forgetting = await forget(HOST);
  expect(forgetting.status).toBe(503);
  expect(await forgetting.text()).toBe("try again\n");

  // hachiko reads a non-2xx as a check-in that did not land and retries on its next sweep.
  expect((await ping(checkin)).status).toBe(503);

  expect(logged.join("\n")).toContain("the switch did not answer");
  expect(logged.join("\n")).not.toContain(BOT);
  expect(logged.join("\n")).toContain("the bot token");
});

it("answers a host it has never heard of without making one", async () => {
  const sent = discord();

  const response = await forget("other-mac");
  expect(response.status).toBe(404);
  expect(await response.text()).toBe("unknown host\n");
  expect((await forget("other-mac")).status).toBe(404);

  expect(sent).toHaveLength(0);
  expect((await switchFor("other-mac").status("other-mac")).state).toBe("unknown");
});

it("answers nothing else", async () => {
  const response = await SELF.fetch("https://shibuya.test/elsewhere", {
    headers: { authorization: `Bearer ${TOKEN}` },
  });
  expect(response.status).toBe(404);

  // /host is the one route with a method of its own, so the method is the route.
  expect((await forget(HOST, TOKEN, "GET")).status).toBe(404);
  expect((await forget(HOST, TOKEN, "POST")).status).toBe(404);
});

// The slug is a storage key that happens to be readable; the display name is what the Mac
// calls itself. Everything Tim reads is the second one and falls back to the first.
it("takes a display name, cleans it, and refuses one that is not a string", async () => {
  discord();

  expect((await ping({ ...checkin, display: 42 })).status).toBe(400);
  expect(await (await ping({ ...checkin, display: [] })).text()).toBe("display\n");

  await ping({ ...checkin, display: "  Tim's\u001b Mac\nmini  " });
  expect((await stored()).live.display).toBe("Tim's  Mac mini");

  await ping({ ...checkin, display: "M".repeat(60) });
  const clipped = String((await stored()).live.display);
  expect(clipped).toHaveLength(40);
  expect(clipped.endsWith("…")).toBe(true);
});

// A path is bytes macOS makes no promises about, and half of an emoji is a lone surrogate:
// invalid UTF-8, and a body Discord refuses outright. So the cut falls between characters,
// and one that arrived in halves to begin with goes.
it("never cuts a name through the middle of a character", async () => {
  discord();

  await ping({ ...checkin, display: "Mac mini 🖥".padEnd(60, "x") });
  const clipped = String((await stored()).live.display);
  expect(clipped).toBe(JSON.parse(JSON.stringify(clipped)));
  expect(/\p{Cs}/u.test(clipped)).toBe(false);

  // A display name that is nothing but emoji, cut where the pairs are.
  await ping({ ...checkin, display: "🖥".repeat(30) });
  expect(/\p{Cs}/u.test(String((await stored()).live.display))).toBe(false);

  // And one sent in halves on purpose, which JSON.parse hands over as it was written.
  await ping({ ...checkin, display: "Mac \ud83d mini" });
  expect((await stored()).live.display).toBe("Mac   mini");
});

// Everything hachiko sends lands in a lead line or a labelled one, where Discord renders
// markdown. A reason naming a path somebody called `[Fix it](https://wherever)` must not
// arrive as a link, and a Mac that calls itself `**Mac mini**` must not arrive as emphasis —
// but the post's title is plain text, so the name goes in there as it is.
it("lets nothing in a check-in style a message or fake a link", async () => {
  const sent = discord();

  await ping(
    {
      ...checkin,
      host: GOLDEN_HOST,
      display: "**Mac mini**",
      reason: "the sweep quoted [Fix it](https://wherever) out of a log",
    },
    TOKEN,
    "/fail",
  );

  const content = sent[0]!.fields.content!;
  expect(content).toContain("⚠️ \\*\\*Mac mini\\*\\* is still checking in");
  expect(content).toContain("**Why:** the sweep quoted \\[Fix it\\]\\(https://wherever\\) out of a log");
  expect(content).not.toContain("[Fix it](https://wherever)");
  expect(sent[0]!.fields.thread_name).toMatch(/^⚠️ \*\*Mac mini\*\*: hachiko's checks are failing · /);
});

it("treats a display name that cleans away to nothing as one that was never sent", async () => {
  discord();

  await ping({ ...checkin, display: DISPLAY });
  await ping({ ...checkin, display: " \u0007 " });

  expect((await stored()).live.display).toBe(DISPLAY);
});

it("calls the Mac what it calls itself, in the post's name and in the first line", async () => {
  const sent = discord();
  await seed(
    {
      host: GOLDEN_HOST,
      display: DISPLAY,
      lastPing: Date.now() - 20 * 60_000,
      summary: READING,
      version: BUILD,
    },
    GOLDEN_HOST,
  );

  await runDurableObjectAlarm(switchFor(GOLDEN_HOST));

  expect(sent[0]!.fields.thread_name).toMatch(/^🔴 Mac mini offline · /);
  expect(sent[0]!.fields.content).toContain("🔴 Mac mini has gone quiet");
  // The slug stays honest, in the one line Tim does not act on.
  expect(sent[0]!.fields.content).toContain("-# tims-mac-mini · hachiko build 1a2b3c4d5e6f");
  expect(sent[0]!.fields.content).not.toContain("tims-mac-mini has");
});

it("keeps the display name across a check-in that does not repeat it, and reports it", async () => {
  discord();

  await ping({ ...checkin, host: GOLDEN_HOST, display: DISPLAY });
  await ping({ host: GOLDEN_HOST, free_gb: 1 });

  const response = await SELF.fetch(`https://shibuya.test/status?host=${GOLDEN_HOST}`, {
    headers: { authorization: `Bearer ${TOKEN}` },
  });
  const status = (await response.json()) as Record<string, unknown>;

  expect(status.display).toBe(DISPLAY);
  expect(status.host).toBe(GOLDEN_HOST);
  expect(status.last_summary).toBe("1 GB free");
});

it("reads a reading in words, however many of each there are", async () => {
  discord();

  await ping({ host: GOLDEN_HOST, free_gb: 0.9, open_incidents: 1, hot_processes: 1 });
  expect((await stored(GOLDEN_HOST)).live.summary).toBe("922 MB free, 1 open incident, 1 process busy");

  await ping({ host: GOLDEN_HOST, free_gb: 4.25, open_incidents: 2, hot_processes: 3 });
  expect((await stored(GOLDEN_HOST)).live.summary).toBe("4.3 GB free, 2 open incidents, 3 processes busy");
});

// A reading with nothing in it is a check-in that reported nothing, so the line it would have
// been on is missing rather than there and empty.
it("leaves the reading line out when a check-in carried no reading", async () => {
  const sent = discord();
  await seed({ host: GOLDEN_HOST, display: DISPLAY, lastPing: Date.now() - 20 * 60_000 }, GOLDEN_HOST);

  await runDurableObjectAlarm(switchFor(GOLDEN_HOST));

  expect(sent[0]!.fields.content).toContain("🔴 Mac mini has gone quiet");
  expect(sent[0]!.fields.content).not.toContain("Last reading");
});

// Every message shibuya can send, rendered in full and printed, so the six of them can be
// read end to end out of one `npm test` rather than inferred from assertions. The real path
// to Discord and the real clock: a fake one here is an alarm armed in the past, which workerd
// then fires for itself in the middle of the test. So the clock in what is printed is
// whenever the suite ran, and every interval in it is exact.
it("renders the six messages, in full", async () => {
  const NOW = Date.now();

  const shown: string[] = [];
  const reading = {
    host: GOLDEN_HOST,
    display: DISPLAY,
    version: BUILD,
    free_gb: 1,
    open_incidents: 0,
    hot_processes: 0,
  };

  function show(what: string, sent: Request_[]): void {
    expect(sent).toHaveLength(1);
    const title = sent[0]!.fields.thread_name;
    const head = title === undefined ? `── ${what} (into the post already open)` : `── ${what}\npost title: ${title}`;
    shown.push(`${head}\n\n${sent[0]!.fields.content}\n`);
  }

  async function fresh(live: Record<string, unknown>): Promise<void> {
    await runInDurableObject(switchFor(GOLDEN_HOST), async (_instance, state) => {
      await state.storage.deleteAll();
      await state.storage.deleteAlarm();
    });
    if (Object.keys(live).length > 0) {
      await seed({ host: GOLDEN_HOST, display: DISPLAY, version: BUILD, ...live }, GOLDEN_HOST);
    }
  }

  // 1. The deadline passed with no check-in, which opens the post.
  let sent = discord();
  await fresh({ lastPing: NOW - 15 * 60_000, summary: READING });
  await runDurableObjectAlarm(switchFor(GOLDEN_HOST));
  expect(sent[0]!.fields.thread_name).toMatch(/^🔴 Mac mini offline · \w{3} \d\d:\d\d$/);
  expect(sent[0]!.fields.content).toContain("🔴 Mac mini has gone quiet — no check-in for 15 minutes");
  expect(sent[0]!.fields.content).toMatch(/\*\*Last check-in:\*\* (at|yesterday at|on) /);
  show("offline", sent);

  // 2. Six hours into the outage, into the same post.
  sent = discord();
  existing("4242", "🔴 Mac mini offline · Thu 18:06");
  await fresh({
    lastPing: NOW - 6 * 60 * 60_000 - 15 * 60_000,
    summary: READING,
    downSince: NOW - 6 * 60 * 60_000,
    outageThread: "4242",
  });
  await runDurableObjectAlarm(switchFor(GOLDEN_HOST));
  expect(sent[0]!.fields.content).toContain("🔴 Mac mini is still offline, 6 hours after the first alert");
  show("still offline, six hours on", sent);

  // 3. The check-in that comes back, into the same post.
  sent = discord();
  existing("4242", "🔴 Mac mini offline · Thu 18:06");
  await fresh({ lastPing: NOW - 16 * 60_000, summary: READING, downSince: NOW - 60_000, outageThread: "4242" });
  await ping(reading);
  expect(sent[0]!.fields.content).toContain("🟢 Mac mini is back — it checked in after 16 minutes of silence");
  show("recovered", sent);

  // 4. The first failing check-in, which opens a post of its own.
  sent = discord();
  await fresh({});
  await ping({ ...reading, reason: "there was no process sample, so nothing was watching the CPU" }, TOKEN, "/fail");
  expect(sent[0]!.fields.thread_name).toMatch(/^⚠️ Mac mini: hachiko's checks are failing · \w{3} \d\d:\d\d$/);
  show("hachiko's checks are failing", sent);

  // 5. An hour of them, into that post.
  sent = discord();
  existing("7", "⚠️ Mac mini: hachiko's checks are failing · Thu 18:06");
  await fresh({
    lastPing: NOW - 60_000,
    summary: READING,
    failingSince: NOW - 60 * 60_000,
    failingReason: "there was no process sample, so nothing was watching the CPU",
    failingThread: "7",
  });
  await runDurableObjectAlarm(switchFor(GOLDEN_HOST));
  expect(sent[0]!.fields.content).toContain("⚠️ Mac mini: hachiko's checks have been failing for 1 hour");
  show("still failing, an hour on", sent);

  // 6. The check that finishes, into that post.
  sent = discord();
  existing("7", "⚠️ Mac mini: hachiko's checks are failing · Thu 18:06");
  await fresh({
    lastPing: NOW - 5 * 60_000,
    summary: READING,
    failingSince: NOW - 65 * 60_000,
    failingReason: "there was no process sample, so nothing was watching the CPU",
    failingThread: "7",
  });
  await ping(reading);
  expect(sent[0]!.fields.content).toContain(
    "🟢 Mac mini: hachiko's checks are finishing again after 1 hour 5 minutes",
  );
  show("clean again", sent);

  print(`\nEvery message shibuya sends, as Discord shows it:\n\n${shown.join("\n")}`);
});
