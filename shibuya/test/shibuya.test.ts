import { env, runDurableObjectAlarm, runInDurableObject, SELF } from "cloudflare:test";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// The pool runs the Worker in this isolate, so this is the same class object the runtime
// instantiates the Durable Object from — which is what lets a test make one of its methods
// throw the way a rollout does.
import { Switch } from "../src/switch";

// The two the test config binds. The webhook is the one string that may never appear in
// anything this Worker writes, so it is here to be asserted against as well as used.
const TOKEN = "a-token-only-the-tests-know";
const WEBHOOK = "https://discord.invalid/api/webhooks/1/secret";

const HOST = "mac-mini";
const SUMMARY = "787 GB free, no open incidents, 0 hot";

interface Reply {
  status: number;
  body?: unknown;
  throws?: string;
}

interface Request_ {
  url: string;
  fields: {
    content?: string;
    thread_name?: string;
    username?: string;
    allowed_mentions?: unknown;
  };
}

// The pool runs the tests in the same isolate as the Worker, so a stubbed global fetch is
// what the Worker's own fetch resolves to. What comes back is the list of requests, which is
// the whole of what the Discord rules are asserted on.
function discord(...replies: Reply[]): Request_[] {
  const sent: Request_[] = [];
  const queue = [...replies];

  vi.stubGlobal("fetch", async (input: unknown, init?: RequestInit): Promise<Response> => {
    sent.push({ url: String(input), fields: JSON.parse(String(init?.body ?? "{}")) as Request_["fields"] });

    const reply = queue.shift() ?? { status: 200, body: { channel_id: "999" } };
    if (reply.throws !== undefined) {
      throw new TypeError(reply.throws);
    }
    // Discord answers a plain webhook post with 204 and no body at all, which a Response
    // refuses to carry.
    return new Response(reply.status === 204 ? null : JSON.stringify(reply.body ?? {}), {
      status: reply.status,
      headers: { "content-type": "application/json" },
    });
  });

  return sent;
}

function switchFor(host = HOST) {
  return env.SWITCH.get(env.SWITCH.idFromName(host));
}

async function seed(live: Record<string, unknown>, alarmAt = Date.now() + 600_000): Promise<void> {
  await runInDurableObject(switchFor(), async (_instance, state) => {
    await state.storage.put("live", live);
    await state.storage.setAlarm(alarmAt);
  });
}

async function stored(): Promise<{ live: Record<string, unknown>; alarm: number | null }> {
  return runInDurableObject(switchFor(), async (_instance, state) => ({
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

let logged: string[] = [];

beforeEach(async () => {
  // A Durable Object outlives the test that made it, so one left down or failing would be
  // that test deciding this one's assertions.
  for (const host of [HOST, "other-mac"]) {
    await runInDurableObject(switchFor(host), async (_instance, state) => {
      await state.storage.deleteAll();
      await state.storage.deleteAlarm();
    });
  }

  logged = [];
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

it("opens one forum post when the deadline passes with no check-in", async () => {
  const sent = discord({ status: 200, body: { channel_id: "4242" } });
  const lastPing = Date.now() - 20 * 60_000;
  await seed({ host: HOST, lastPing, summary: SUMMARY, version: "abc1234" });

  expect(await runDurableObjectAlarm(switchFor())).toBe(true);

  expect(sent).toHaveLength(1);
  expect(sent[0]!.url).toBe(`${WEBHOOK}?wait=true`);
  expect(sent[0]!.fields.thread_name).toMatch(/^mac-mini offline · /);
  expect(sent[0]!.fields.thread_name!.length).toBeLessThanOrEqual(100);
  expect(sent[0]!.fields.content).toContain(SUMMARY);
  expect(sent[0]!.fields.content).toContain("has not checked in for 20 min");
  expect(sent[0]!.fields.content).toContain("Running hachiko build abc1234.");

  const { live } = await stored();
  expect(live.downSince).toBeGreaterThan(0);
  expect(live.outageThread).toBe("4242");
});

it("leaves the build line out of an outage alert when no check-in has named one", async () => {
  const sent = discord({ status: 200, body: { channel_id: "4242" } });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  expect(await runDurableObjectAlarm(switchFor())).toBe(true);

  expect(sent[0]!.fields.content).toContain(SUMMARY);
  expect(sent[0]!.fields.content).not.toContain("hachiko");
});

// The webhook is hachiko's own, so every one of these arrives under hachiko's name unless
// the body says otherwise — and an outage post is the one message that has to be clearly
// not the watch talking. The three shapes a post can take are all exercised, because the
// one that forgot the name is the one that would be reached.
it("posts under its own name rather than the webhook's", async () => {
  const sent = discord({ status: 200, body: { channel_id: "7" } }, { status: 400 }, { status: 204 });

  await ping({ ...checkin, reason: "the walk ran out of its seconds" }, TOKEN, "/fail");
  await ping(checkin);

  expect(sent).toHaveLength(3);
  expect(sent[0]!.url).toBe(`${WEBHOOK}?wait=true`);
  expect(sent[1]!.url).toBe(`${WEBHOOK}?thread_id=7`);
  expect(sent[2]!.url).toBe(WEBHOOK);

  for (const request of sent) {
    expect(request.fields.username).toBe("shibuya");
  }
});

it("does not post a second time on the next alarm of the same outage", async () => {
  const sent = discord({ status: 200, body: { channel_id: "4242" } });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());
  expect(sent).toHaveLength(1);

  await seed({ ...(await stored()).live });
  expect(await runDurableObjectAlarm(switchFor())).toBe(true);

  expect(sent).toHaveLength(1);
  expect((await stored()).live.queue).toEqual([]);
});

it("posts the recovery into the outage's own post", async () => {
  const sent = discord({ status: 200, body: { channel_id: "4242" } }, { status: 200, body: { id: "5" } });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());
  expect((await ping(checkin)).status).toBe(200);

  expect(sent).toHaveLength(2);
  expect(sent[1]!.url).toBe(`${WEBHOOK}?thread_id=4242`);
  expect(sent[1]!.fields.thread_name).toBeUndefined();
  expect(sent[1]!.fields.content).toContain("is checking in again after 20 min");
  expect(sent[1]!.fields.content).toContain(SUMMARY);

  const { live } = await stored();
  expect(live.downSince).toBe(0);
});

it("opens one post for a failing streak, posts nothing for the next fail, and clears it on a clean run", async () => {
  const sent = discord({ status: 200, body: { channel_id: "7" } });

  const first = await ping({ ...checkin, reason: "the walk ran out of its 1m0s" }, TOKEN, "/fail");
  expect(first.status).toBe(200);
  expect(sent).toHaveLength(1);
  expect(sent[0]!.url).toBe(`${WEBHOOK}?wait=true`);
  expect(sent[0]!.fields.thread_name).toMatch(/^mac-mini · hachiko runs are failing · /);
  expect(sent[0]!.fields.content).toContain("the walk ran out of its 1m0s");

  await ping({ ...checkin, reason: "the walk ran out of its 1m0s" }, TOKEN, "/fail");
  expect(sent).toHaveLength(1);

  await ping(checkin);
  expect(sent).toHaveLength(2);
  expect(sent[1]!.url).toBe(`${WEBHOOK}?thread_id=7`);
  expect(sent[1]!.fields.content).toContain("runs are clean again");

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

  const reason = sent[0]!.fields.content!.split("\n")[1]!;
  expect(reason).not.toMatch(/[\u0000-\u001f]/);
  expect(reason.startsWith("ps failed   and said so xxx")).toBe(true);
  expect(reason.length).toBeLessThanOrEqual(300);
});

it("opens a new post when the one it recorded is gone", async () => {
  const sent = discord({ status: 404 }, { status: 200, body: { channel_id: "8080" } });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY, failingThread: "7", failingSince: 1 });

  await ping(checkin);

  expect(sent).toHaveLength(2);
  expect(sent[0]!.url).toBe(`${WEBHOOK}?thread_id=7`);
  expect(sent[1]!.url).toBe(`${WEBHOOK}?wait=true`);
  expect(sent[1]!.fields.thread_name).toBeDefined();
  expect((await stored()).live.failingThread).toBe("8080");
});

it("sends a plain message when the webhook will not take a post id", async () => {
  const sent = discord({ status: 400 }, { status: 204 });
  await seed({ host: HOST, lastPing: Date.now(), summary: SUMMARY, failingThread: "7", failingSince: 1 });

  await ping(checkin);

  expect(sent).toHaveLength(2);
  expect(sent[1]!.url).toBe(WEBHOOK);
  expect(sent[1]!.fields.thread_name).toBeUndefined();
  expect((await stored()).live.failingThread).toBe("");
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
// the whole server from inside an outage alert. The three shapes a post can take are all
// exercised, because the one that forgot the flag is the one that would be reached. The text
// is not scrubbed and should not be: what Tim reads is what the sweep found.
it("lets nothing it posts mention anybody", async () => {
  const sent = discord({ status: 200, body: { channel_id: "7" } }, { status: 400 }, { status: 204 });

  await ping({ ...checkin, reason: "@everyone the walk ran out of its seconds" }, TOKEN, "/fail");
  await ping(checkin);

  expect(sent).toHaveLength(3);
  expect(sent[0]!.url).toBe(`${WEBHOOK}?wait=true`);
  expect(sent[1]!.url).toBe(`${WEBHOOK}?thread_id=7`);
  expect(sent[2]!.url).toBe(WEBHOOK);

  for (const request of sent) {
    expect(request.fields.allowed_mentions).toEqual({ parse: [] });
  }
  expect(sent[0]!.fields.content).toContain("@everyone the walk ran out of its seconds");
});

it("never writes the webhook anywhere, whatever Discord says about it", async () => {
  discord({ status: 500, throws: `Network connection lost: POST ${WEBHOOK}?wait=true` });
  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  await runDurableObjectAlarm(switchFor());

  expect(logged.length).toBeGreaterThan(0);
  expect(logged.join("\n")).not.toContain(WEBHOOK);
  expect(logged.join("\n")).not.toContain("/api/webhooks/");
  expect(logged.join("\n")).toContain("the webhook");
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

  const sent: Request_[] = [];
  vi.stubGlobal("fetch", async (input: unknown, init?: RequestInit): Promise<Response> => {
    sent.push({ url: String(input), fields: JSON.parse(String(init?.body ?? "{}")) as Request_["fields"] });
    await held;
    return new Response(JSON.stringify({ channel_id: "4242" }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  });

  await seed({ host: HOST, lastPing: Date.now() - 20 * 60_000, summary: SUMMARY });

  // Not awaited: the alarm is inside the fetch above, which is where the gate opens.
  const alarm = runDurableObjectAlarm(switchFor());
  await vi.waitUntil(() => sent.length === 1);

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
  const why = `Durable Object class has no method forget: ${WEBHOOK}`;
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
  expect(logged.join("\n")).not.toContain(WEBHOOK);
  expect(logged.join("\n")).toContain("the webhook");
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
