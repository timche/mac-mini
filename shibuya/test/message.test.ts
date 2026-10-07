import { expect, it } from "vitest";

import { code, duration, machine, markers, plain, render, size, title, when } from "../src/message";

// Fixed moments in Tim's own zone, because every rule in here is about what he reads rather
// than about what the clock says. 6 October 2026 is a Tuesday, and London is on BST.
const tuesdayEvening = Date.parse("2026-10-06T17:35:00+01:00");
const tuesdayAfternoon = Date.parse("2026-10-06T17:20:00+01:00");
const mondayAfternoon = Date.parse("2026-10-05T17:20:00+01:00");
const sundayAfternoon = Date.parse("2026-10-04T17:20:00+01:00");

it("says a size in the unit it is worth saying it in", () => {
  expect(size(4.3)).toBe("4.3 GB");
  expect(size(4.25)).toBe("4.3 GB");
  expect(size(787)).toBe("787 GB");
  expect(size(1)).toBe("1 GB");
  expect(size(0.9)).toBe("922 MB");
  expect(size(0)).toBe("0 MB");
});

it("says a duration in words, with the right half of every plural", () => {
  expect(duration(20_000)).toBe("less than a minute");
  expect(duration(60_000)).toBe("1 minute");
  expect(duration(15 * 60_000)).toBe("15 minutes");
  expect(duration(60 * 60_000)).toBe("1 hour");
  expect(duration(65 * 60_000)).toBe("1 hour 5 minutes");
  expect(duration(61 * 60_000)).toBe("1 hour 1 minute");
  expect(duration(3 * 60 * 60_000)).toBe("3 hours");
  expect(duration(6 * 60 * 60_000)).toBe("6 hours");
  expect(duration(25 * 60 * 60_000)).toBe("25 hours");

  // A Mac that has been off since Friday is not news by the minute, and `51 hours` is a
  // number Tim would have to divide.
  expect(duration(47 * 60 * 60_000)).toBe("47 hours");
  expect(duration(48 * 60 * 60_000)).toBe("2 days");
  expect(duration((51 * 60 + 30) * 60_000)).toBe("2 days 3 hours");
  expect(duration(72 * 60 * 60_000)).toBe("3 days");
});

// Three forms and no more: a date he does not need is a date he reads past, and a date he
// does need missing is a time he reads as today.
it("says a time as today, as yesterday, or with the day on it", () => {
  expect(when(tuesdayAfternoon, tuesdayEvening)).toBe("at 17:20");
  expect(when(mondayAfternoon, tuesdayEvening)).toBe("yesterday at 17:20");
  expect(when(sundayAfternoon, tuesdayEvening)).toBe("on Sun 4 Oct at 17:20");
});

it("names a post with its marker and the time it opened", () => {
  expect(title(markers.down, "Mac mini offline", tuesdayEvening)).toBe("🔴 Mac mini offline · Tue 17:35");
  expect(title(markers.warn, "Mac mini: hachiko's checks are failing", tuesdayEvening)).toBe(
    "⚠️ Mac mini: hachiko's checks are failing · Tue 17:35",
  );

  // Discord refuses a name over a hundred characters, and what the display name is called is
  // the Mac's to decide.
  const long = title(markers.down, `${"M".repeat(120)} offline`, tuesdayEvening);
  expect(long.length).toBeLessThanOrEqual(100);
  expect(long.startsWith("🔴 MMM")).toBe(true);
  expect(long.endsWith("… · Tue 17:35")).toBe(true);
});

// Discord closes an inline code span at the first run of backticks as long as the one that
// opened it, so a value holding backticks needs a longer fence and a value that starts or
// ends with one needs the padding Discord eats again.
it("puts a value in a code span whatever backticks are in it", () => {
  expect(code("~/Library/Logs/hachiko.log")).toBe("`~/Library/Logs/hachiko.log`");
  expect(code("tims-mac-mini")).toBe("`tims-mac-mini`");
  expect(code("a `b` c")).toBe("``a `b` c``");
  expect(code("a ``b`` c")).toBe("```a ``b`` c```");
  expect(code("`quoted`")).toBe("`` `quoted` ``");
  expect(code("")).toBe("");
});

it("renders a message as a headline, its detail, what to do and the machine", () => {
  const text = render({
    marker: markers.down,
    lead: "Mac mini has gone quiet — no check-in for 15 minutes",
    details: [
      { label: "Last check-in", value: "at 17:20" },
      { label: "Last reading", value: "" },
    ],
    action: "Check that the Mac is powered up and on the network.",
    machine: machine("tims-mac-mini", "1a2b3c4d5e6f"),
  });

  expect(text).toBe(
    [
      "🔴 Mac mini has gone quiet — no check-in for 15 minutes",
      "**Last check-in:** at 17:20",
      "",
      "Check that the Mac is powered up and on the network.",
      "-# tims-mac-mini · hachiko build 1a2b3c4d5e6f",
    ].join("\n"),
  );
});

// Plain text, both of them: the subtext line already says this is machine detail, and a code
// span inside subtext is a second mark for the same thing.
it("leaves the build out of the machine line when no check-in has named one", () => {
  expect(machine("tims-mac-mini", "")).toBe("tims-mac-mini");
  expect(render({ marker: markers.clear, lead: "Mac mini is back", action: "Nothing to do." })).toBe(
    "🟢 Mac mini is back\n\nNothing to do.",
  );
});

// What hachiko sends reaches a lead line and a labelled line, where Discord renders markdown:
// a reason or a name shaped like a link has to arrive as itself.
it("lets nothing hachiko sent style a message or fake a link", () => {
  expect(plain("the walk ran out of its seconds")).toBe("the walk ran out of its seconds");
  expect(plain("[Fix it](https://wherever)")).toBe("\\[Fix it\\]\\(https://wherever\\)");
  expect(plain("**Mac mini**")).toBe("\\*\\*Mac mini\\*\\*");
  expect(plain("# heading > quote ~x~ _u_ |spoiler| `code`")).toBe(
    "\\# heading \\> quote \\~x\\~ \\_u\\_ \\|spoiler\\| \\`code\\`",
  );
  expect(plain("a\\b")).toBe("a\\\\b");
  expect(plain("ドキュメント")).toBe("ドキュメント");

  // A title is plain text in Discord, so the name goes in unescaped: an escape nothing
  // renders is a backslash Tim reads.
  expect(title(markers.down, "**Mac mini** offline", tuesdayEvening)).toBe("🔴 **Mac mini** offline · Tue 17:35");
});
