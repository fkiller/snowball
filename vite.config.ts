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
      {
        // vinext detects the worker bundler and injects Cloudflare tracing.
        // We execute the bundle in Node; only that optional integration is empty.
        name: "snowball:node-tracing",
        enforce: "pre",
        resolveId(id) {
          if (id === "vinext/internal/server/cloudflare-workers-tracing") {
            return "\0snowball:node-tracing";
          }
        },
        load(id) {
          if (id === "\0snowball:node-tracing") return "export {};";
        },
      },
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
