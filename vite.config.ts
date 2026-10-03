import vinext from "vinext";
import { defineConfig } from "vite";

export default defineConfig(async () => {
  // The existing worker bundler produces the fetch entry used by our local
  // Node server. No Cloudflare deployment, database or storage binding is used.
  process.env.WRANGLER_WRITE_LOGS ??= "false";
  process.env.WRANGLER_LOG_PATH ??= ".wrangler/wrangler.log";
  process.env.MINIFLARE_REGISTRY_PATH ??= ".wrangler/registry";
  const { cloudflare } = await import("@cloudflare/vite-plugin");

  return {
    plugins: [
      vinext(),
      cloudflare({
        viteEnvironment: { name: "rsc", childEnvironments: ["ssr"] },
        config: {
          main: "./services/web-entry.ts",
          compatibility_flags: ["nodejs_compat"],
        },
      }),
    ],
  };
});
