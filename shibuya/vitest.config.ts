import { cloudflareTest } from "@cloudflare/vitest-pool-workers";
import { defineConfig } from "vitest/config";

// The real wrangler config, so a binding or a migration that is wrong in the deploy is
// wrong in the tests too. The two secrets are the exception: they are set with
// `wrangler secret put` against the deployed Worker and exist nowhere on disk.
export default defineConfig({
  // The default reporter keeps what a passing test printed to itself, and one test here
  // prints every message shibuya can send so that a reviewer reads the six of them out of
  // `npm test` rather than out of the assertions about them.
  test: { reporters: ["verbose"] },
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.jsonc" },
      miniflare: {
        bindings: {
          PING_TOKEN: "a-token-only-the-tests-know",
          DISCORD_WEBHOOK_URL: "https://discord.invalid/api/webhooks/1/secret",
        },
      },
    }),
  ],
});
