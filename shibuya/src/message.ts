// What a shibuya message looks like, decided once. Everything here is read on a phone at
// four in the morning, so a message is a headline and then the detail under it: one marker,
// one sentence, labelled lines, and the one thing Tim can do about it.
//
// hachiko's own Go sender obeys the same rules into the same channel, so this file and that
// one are one style and a change to either is a change to both.

// Exactly one of these starts a lead line, and nothing else in a message carries an emoji —
// which is what keeps a marker meaning what it says when the text around it was written by
// whatever filled the disk.
export const markers = {
  down: "🔴",
  clear: "🟢",
  warn: "⚠️",
  info: "ℹ️",
  disk: "💾",
  busy: "🔥",
} as const;

export type Marker = (typeof markers)[keyof typeof markers];

export interface Detail {
  label: string;
  value: string;
}

export interface Message {
  marker: Marker;
  // One short sentence and no full stop: it is a headline, and a lock screen shows about this
  // much of it.
  lead: string;
  // In a stable order. One with nothing to say is dropped rather than printed empty.
  details?: Detail[];
  // What Tim can do, or that there is nothing.
  action?: string;
  // Machine detail he never acts on, as Discord subtext.
  machine?: string;
}

const TZ = "Europe/London";

// Discord's own cap on a forum post's name.
const TITLE_LIMIT = 100;

export function render(message: Message): string {
  const lines = [`${message.marker} ${message.lead}`];

  for (const detail of message.details ?? []) {
    if (detail.value !== "") {
      lines.push(`**${detail.label}:** ${detail.value}`);
    }
  }

  if (message.action !== undefined && message.action !== "") {
    lines.push("", message.action);
  }
  if (message.machine !== undefined && message.machine !== "") {
    if (message.action === undefined || message.action === "") {
      lines.push("");
    }
    lines.push(`-# ${message.machine}`);
  }

  return lines.join("\n");
}

// A forum post's name, which is the whole of what a notification and a channel list show.
// The time is in it because a post is read weeks later, next to the others.
export function title(marker: Marker, what: string, at: number): string {
  const stamp = `${weekday.format(at)} ${clock.format(at)}`;
  const full = `${marker} ${what} · ${stamp}`;
  if (full.length <= TITLE_LIMIT) {
    return full;
  }

  const room = TITLE_LIMIT - `${marker} … · ${stamp}`.length;
  return `${marker} ${what.slice(0, Math.max(room, 0)).trim()}… · ${stamp}`;
}

// The slug and the build, the two things in a message that name a machine rather than tell
// Tim anything. Plain text in it: the subtext line already says it is machine detail, and a
// code span inside subtext is a second mark for the same thing and reads badly on a phone.
export function machine(host: string, version: string): string {
  const parts: string[] = [];
  if (host !== "") {
    parts.push(host);
  }
  if (version !== "") {
    parts.push(`hachiko build ${version}`);
  }
  return parts.join(" · ");
}

export function size(gb: number): string {
  if (!Number.isFinite(gb) || gb < 0) {
    return "";
  }

  const mb = Math.round(gb * 1024);
  if (mb < 1024) {
    return `${mb} MB`;
  }
  return `${Math.round(gb * 10) / 10} GB`;
}

// In words, because `6h 15m` is a log line. Rounded to the minute: a watch that reports every
// five is never asked for a second. Two units at most, and the minutes are dropped once there
// are days — a Mac that has been off since Friday is not news by the minute, and `51 hours` is
// a number Tim would have to divide.
export function duration(ms: number): string {
  const minutes = Math.round(ms / 60_000);
  if (minutes < 1) {
    return "less than a minute";
  }
  if (minutes < 60) {
    return plural(minutes, "minute", "minutes");
  }

  const hours = Math.floor(minutes / 60);
  if (hours >= 48) {
    const days = Math.floor(hours / 24);
    const spare = hours % 24;
    if (spare === 0) {
      return plural(days, "day", "days");
    }
    return `${plural(days, "day", "days")} ${plural(spare, "hour", "hours")}`;
  }

  const rest = minutes % 60;
  if (rest === 0) {
    return plural(hours, "hour", "hours");
  }
  return `${plural(hours, "hour", "hours")} ${plural(rest, "minute", "minutes")}`;
}

// Tim's own zone and never a date he has to work out: a time he has to convert is a time he
// misreads at four in the morning, and "17:20" with no day is a time he reads as today.
export function when(at: number, now: number): string {
  const day = londonDay(at);
  const today = londonDay(now);

  if (day === today) {
    return `at ${clock.format(at)}`;
  }
  if (day === today - 1) {
    return `yesterday at ${clock.format(at)}`;
  }
  return `on ${calendar.format(at)} at ${clock.format(at)}`;
}

export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

// A value Tim reads as typed: a path, a command, a host slug. Discord closes an inline code
// span at the first run of backticks as long as the one that opened it, so the fence is one
// longer than the longest run in the content; a value that starts or ends with a backtick
// gets the padding space Discord then eats again.
export function code(value: string): string {
  if (value === "") {
    return "";
  }

  let longest = 0;
  for (const run of value.match(/`+/g) ?? []) {
    longest = Math.max(longest, run.length);
  }

  const fence = "`".repeat(longest + 1);
  const pad = value.startsWith("`") || value.endsWith("`") ? " " : "";
  return `${fence}${pad}${value}${pad}${fence}`;
}

// The characters Discord gives a meaning to in a message body, and the escape that tells it
// to render one as itself. hachiko's own sender escapes the same set for the same reason.
const MARKDOWN = /[\\*_~|`>#[\]()]/g;

// A value hachiko sent that goes in a message outside a code span: the name the Mac calls
// itself, and the reason a check gave, which names paths whatever filled the disk called
// them. A reason reading `**offline**`, or `[click here](https://wherever)`, would otherwise
// arrive as hachiko's own emphasis or as a link Tim is invited to follow.
//
// Not in a post's title, which is plain text — Discord renders no markdown there, so an
// escape invisible in a message is a backslash in the title.
export function plain(value: string): string {
  return value.replace(MARKDOWN, "\\$&");
}

const clock = formatter({ hour: "2-digit", minute: "2-digit", hour12: false });
const weekday = formatter({ weekday: "short" });
const calendar = formatter({ weekday: "short", day: "numeric", month: "short" });
const ymd = new Intl.DateTimeFormat("en-GB", {
  timeZone: TZ,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});

function formatter(options: Intl.DateTimeFormatOptions): { format: (at: number) => string } {
  const format = new Intl.DateTimeFormat("en-GB", { timeZone: TZ, ...options });
  return { format: (at: number) => format.format(new Date(at)) };
}

// Which London day a moment falls on, as a day number, so "today" and "yesterday" are about
// the calendar Tim is looking at rather than twenty-four hours of milliseconds.
function londonDay(at: number): number {
  let year = 0;
  let month = 1;
  let day = 1;

  for (const part of ymd.formatToParts(new Date(at))) {
    if (part.type === "year") {
      year = Number(part.value);
    } else if (part.type === "month") {
      month = Number(part.value);
    } else if (part.type === "day") {
      day = Number(part.value);
    }
  }

  return Math.floor(Date.UTC(year, month - 1, day) / 86_400_000);
}
