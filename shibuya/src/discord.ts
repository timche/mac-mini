// The same rules hachiko's own sender obeys, because it is the same channel: a webhook
// on a forum refuses a message that names no post and one on a text channel refuses a
// message that does, and nothing here is told which it has. So the first message of an
// outage opens a post, reads the post's id off the message that comes back, and every
// message after it goes in by id. A post Tim deleted answers 404 and the message that
// found it gone opens a new one; a 400 is the webhook refusing the id rather than the post
// being missing, which is what a text channel says, so that message goes to the channel.

const CONTENT_LIMIT = 2000;
const CONTENT_KEEP = 1940;
const NAME_LIMIT = 100;

const SNOWFLAKE = /^[0-9]{1,24}$/;

export interface Post {
  content: string;
  threadId?: string;
  threadName?: string;
}

// The post the message landed in, or "" when there is no post to go back into.
export async function postToForum(webhook: string, post: Post): Promise<string> {
  const content = cap(post.content);

  if (post.threadId && SNOWFLAKE.test(post.threadId)) {
    const into = await send(webhook, `thread_id=${encodeURIComponent(post.threadId)}`, { content });
    if (into.ok) {
      return post.threadId;
    }
    if (into.status === 400) {
      await plain(webhook, content);
      return "";
    }
    if (into.status !== 404) {
      throw new Error(`the webhook answered ${into.status}`);
    }
  }

  // wait=true so Discord answers with the message it made rather than an empty 204: the
  // channel that message landed in is the post, and its id is the only way back into it.
  const opened = await send(webhook, "wait=true", {
    content,
    thread_name: name(post),
  });
  if (opened.ok) {
    return threadOf(opened.body);
  }
  if (opened.status === 400) {
    await plain(webhook, content);
    return "";
  }
  throw new Error(`the webhook answered ${opened.status}`);
}

// net/http's counterpart on this side: an error from fetch names the URL it failed on, and
// nothing that leaves this module may carry the webhook. Longest form first, so a prefix of
// one does not break the match for another.
export function redact(text: string, webhook: string): string {
  const trimmed = webhook.trim();
  if (trimmed === "") {
    return text;
  }

  const forms = [trimmed, encodeURIComponent(trimmed), encodeURI(trimmed)];
  forms.sort((a, b) => b.length - a.length);

  let out = text;
  for (const form of forms) {
    out = out.split(form).join("the webhook");
  }
  return out;
}

interface Answer {
  ok: boolean;
  status: number;
  body: string;
}

async function send(webhook: string, query: string, fields: Record<string, string>): Promise<Answer> {
  let response: Response;
  try {
    response = await fetch(withQuery(webhook, query), {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(fields),
    });
  } catch (err) {
    throw new Error(redact(String(err), webhook));
  }

  const body = await response.text().catch(() => "");
  return { ok: response.ok, status: response.status, body: body.slice(0, 4096) };
}

// The message and nothing else, which is what a webhook on a text channel takes and what
// anything that could not find its post falls back to.
async function plain(webhook: string, content: string): Promise<void> {
  const answer = await send(webhook, "", { content });
  if (!answer.ok) {
    throw new Error(`the webhook answered ${answer.status}`);
  }
}

// A webhook URL of Tim's carries no query of its own, but one that did would otherwise have
// `?wait=true` appended to a URL that already has a `?` in it.
function withQuery(webhook: string, query: string): string {
  if (query === "") {
    return webhook;
  }
  return webhook + (webhook.includes("?") ? "&" : "?") + query;
}

function name(post: Post): string {
  const first = (post.threadName ?? post.content).trim().split("\n")[0] ?? "";
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

// A forum webhook answers with the message, whose `channel_id` is the post it opened
// rather than the channel the webhook is on.
function threadOf(body: string): string {
  try {
    const message: unknown = JSON.parse(body);
    const id = (message as { channel_id?: unknown }).channel_id;
    return typeof id === "string" && SNOWFLAKE.test(id) ? id : "";
  } catch {
    return "";
  }
}
