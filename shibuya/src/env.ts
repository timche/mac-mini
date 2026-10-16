import type { Switch } from "./switch";

// `wrangler types` would generate this from the bindings in wrangler.jsonc. Two of the five
// are secrets, which are in no file at all, so it is written here instead — in the namespace
// the runtime and `cloudflare:test` both read `env`'s type out of, so there is one of it.
declare global {
  namespace Cloudflare {
    interface Env {
      SWITCH: DurableObjectNamespace<Switch>;
      GRACE_MINUTES: string;
      PING_TOKEN: string;
      DISCORD_FORUM: string;
      // Optional in the type because a Worker deployed before secrets.sh ran has none, and
      // then every message waits in the queue rather than the object failing to start.
      DISCORD_BOT_TOKEN?: string;
    }
  }
}

export type Env = Cloudflare.Env;

// A grace of nothing is a Worker that calls the Mac dead the moment it checks in, so a
// var that has been emptied or mistyped falls back to the quarter of an hour the alert
// text is written for.
export function graceMs(env: Env): number {
  const minutes = Number(env.GRACE_MINUTES);
  return (Number.isFinite(minutes) && minutes > 0 ? minutes : 15) * 60_000;
}
