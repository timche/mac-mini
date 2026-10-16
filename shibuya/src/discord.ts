// shibuya's own bot, which is hachiko's sender on this side of the wire: a forum post is
// opened with its first message in one call, every message after it goes into the post, and
// the post is tagged and closed the way hachiko's are. A bot of its own rather than hachiko's,
// so the name on a message says which of the two is talking and a token leaked from
// Cloudflare reaches neither the forum hachiko reads Tim's replies in nor anything else.

const API = "https://discord.com/api/v10";
const FORUM = 15;

const CONTENT_LIMIT = 2000;
const CONTENT_KEEP = 1940;
const NAME_LIMIT = 100;

// Unknown Channel: the post Tim deleted.
const UNKNOWN_CHANNEL = 10003;

// A week rather than hachiko's day, because an open post is how shibuya finds its incident
// again, and Discord drops a post from the active list once it has auto-archived — an outage
// is quiet for longer than a day between its alert and its six-hour reminder and the end.
const AUTO_ARCHIVE_MINUTES = 10080;

const SNOWFLAKE = /^[0-9]{1,24}$/;

// By name and never by id, exactly as hachiko's sender finds them: the ids are the forum's
// own, and a forum missing one simply does not get that label.
const TAG_OPEN = "Open";
const TAG_RESOLVED = "Resolved";

export interface Thread {
  id: string;
  name: string;
  appliedTags: string[];
}

interface Channel {
  id?: string;
  type?: number;
  guild_id?: string;
  parent_id?: string;
  owner_id?: string;
  name?: string;
  applied_tags?: string[];
  available_tags?: { id: string; name: string }[];
}

interface Forum {
  guild: string;
  open: string;
  resolved: string;
}

export class GoneError extends Error {}

export class Discord {
  private forumCache: Forum | undefined;
  private self = "";

  constructor(
    private readonly token: string,
    private readonly forum: string,
  ) {}

  // The post and the message that opens it, which is the only shape a forum takes. The id
  // that comes back is the post's and the starter message's both.
  async openPost(name: string, content: string): Promise<string> {
    let tags: string[] = [];
    try {
      const forum = await this.describe();
      tags = forum.open === "" ? [] : [forum.open];
    } catch (err) {
      console.log(`shibuya: a post goes up untagged, since the forum could not be read: ${this.scrub(err)}`);
    }

    const answer = await this.call("POST", `/channels/${this.forum}/threads`, {
      name: postName(name),
      auto_archive_duration: AUTO_ARCHIVE_MINUTES,
      ...(tags.length > 0 ? { applied_tags: tags } : {}),
      message: { content: cap(content), allowed_mentions: noMentions() },
    });

    const id = (parse(answer) as Channel).id;
    if (typeof id !== "string" || !SNOWFLAKE.test(id)) {
      throw new Error("Discord made a post it did not name");
    }
    return id;
  }

  // Throws GoneError when the post is gone, which the caller answers by opening another.
  async postInto(post: string, content: string): Promise<void> {
    if (!SNOWFLAKE.test(post)) {
      throw new GoneError("no post");
    }
    await this.call("POST", `/channels/${post}/messages`, {
      content: cap(content),
      allowed_mentions: noMentions(),
    });
  }

  // The posts shibuya opened that are still tagged Open and whose name starts with the
  // prefix, which is what makes the forum rather than this object's memory the answer to
  // whether an incident is already posted. Active threads only: a post that archived is
  // either closed or one Discord no longer lists, and both are a post to open afresh.
  async openPosts(prefix: string): Promise<Thread[]> {
    const forum = await this.describe();
    if (forum.open === "") {
      return [];
    }
    const self = await this.me();

    const answer = parse(await this.call("GET", `/guilds/${forum.guild}/threads/active`)) as {
      threads?: Channel[];
    };

    return (answer.threads ?? [])
      .filter(
        (thread) =>
          thread.parent_id === this.forum &&
          thread.owner_id === self &&
          (thread.applied_tags ?? []).includes(forum.open) &&
          (thread.name ?? "").startsWith(prefix) &&
          typeof thread.id === "string" &&
          SNOWFLAKE.test(thread.id),
      )
      .map((thread) => ({ id: thread.id as string, name: thread.name ?? "", appliedTags: thread.applied_tags ?? [] }))
      .sort((a, b) => (BigInt(a.id) < BigInt(b.id) ? -1 : 1));
  }

  // The tags a post ends with and the archive in one call, after the message and never
  // before it, because Discord unarchives a post anything is posted into.
  async closePost(post: string, applied?: string[]): Promise<void> {
    if (!SNOWFLAKE.test(post)) {
      return;
    }
    const forum = await this.describe();

    let current = applied;
    if (current === undefined) {
      current = (parse(await this.call("GET", `/channels/${post}`)) as Channel).applied_tags ?? [];
    }

    const tags = current.filter((id) => id !== forum.open);
    if (forum.resolved !== "" && !tags.includes(forum.resolved)) {
      tags.push(forum.resolved);
    }

    await this.call("PATCH", `/channels/${post}`, { applied_tags: tags, archived: true });
  }

  // The error text with the token taken out, for anything that logs one.
  scrub(err: unknown): string {
    return redact(err instanceof Error ? err.message : String(err), this.token);
  }

  // Kept for the life of the instance: the guild a forum is in does not move, and tags
  // renamed under a running object are found again on its next start.
  private async describe(): Promise<Forum> {
    if (this.forumCache !== undefined) {
      return this.forumCache;
    }
    const channel = parse(await this.call("GET", `/channels/${this.forum}`)) as Channel;
    if (channel.type !== FORUM || typeof channel.guild_id !== "string") {
      throw new Error("DISCORD_FORUM is not a forum channel");
    }
    this.forumCache = { guild: channel.guild_id, open: tag(channel, TAG_OPEN), resolved: tag(channel, TAG_RESOLVED) };
    return this.forumCache;
  }

  private async me(): Promise<string> {
    if (this.self === "") {
      const id = (parse(await this.call("GET", "/users/@me")) as { id?: unknown }).id;
      if (typeof id !== "string" || !SNOWFLAKE.test(id)) {
        throw new Error("Discord did not say who this bot is");
      }
      this.self = id;
    }
    return this.self;
  }

  private async call(method: string, path: string, body?: unknown): Promise<string> {
    if (this.token === "") {
      throw new Error("there is no DISCORD_BOT_TOKEN");
    }

    let response: Response;
    try {
      response = await fetch(API + path, {
        method,
        headers: {
          authorization: `Bot ${this.token}`,
          ...(body === undefined ? {} : { "content-type": "application/json" }),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    } catch (err) {
      throw new Error(redact(String(err), this.token));
    }

    const text = await response.text().catch(() => "");
    if (response.status === 404 && (parse(text) as { code?: unknown }).code === UNKNOWN_CHANNEL) {
      throw new GoneError(`Discord answered 404 to ${method} ${path}`);
    }
    if (!response.ok) {
      throw new Error(`Discord answered ${response.status} to ${method} ${path}`);
    }
    return text;
  }
}

// Longest form first, so a prefix of one does not break the match for another. The token is
// only ever in a header, but an error that echoed one back would be logged.
export function redact(text: string, token: string): string {
  const trimmed = token.trim();
  if (trimmed === "") {
    return text;
  }

  const forms = [trimmed, encodeURIComponent(trimmed)];
  forms.sort((a, b) => b.length - a.length);

  let out = text;
  for (const form of forms) {
    out = out.split(form).join("the bot token");
  }
  return out;
}

// Discord resolves @everyone, @here and a role mention in a message body unless the body
// says otherwise, and what shibuya posts carries a reason written by a sweep — so a path or
// a log line holding "@everyone" would page the whole server from inside an outage alert.
function noMentions(): { parse: string[] } {
  return { parse: [] };
}

function tag(forum: Channel, name: string): string {
  const found = (forum.available_tags ?? []).find((candidate) => candidate.name.toLowerCase() === name.toLowerCase());
  return found?.id ?? "";
}

function parse(text: string): unknown {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return {};
  }
}

function postName(name: string): string {
  const first = name.trim().split("\n")[0] ?? "";
  const trimmed = first.slice(0, NAME_LIMIT).trim();
  return trimmed === "" ? "shibuya" : trimmed;
}

function cap(content: string): string {
  const message = content.replace(/\n+$/, "");
  if (message.length <= CONTENT_LIMIT) {
    return message;
  }
  return `${message.slice(0, CONTENT_KEEP)}\n[truncated]`;
}
