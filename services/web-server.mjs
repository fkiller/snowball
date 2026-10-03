import http from "node:http";
import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import path from "node:path";
import { Readable } from "node:stream";
import { fileURLToPath, pathToFileURL } from "node:url";

const listenHost = process.env.WEB_LISTEN_HOST || "127.0.0.1";
const listenPort = Number(process.env.WEB_LISTEN_PORT || 3000);
const serviceDirectory = path.dirname(fileURLToPath(import.meta.url));
const applicationRoot = path.resolve(serviceDirectory, "..");
const assetRoot = path.join(applicationRoot, "dist", "client");
const workerPath = path.join(applicationRoot, "dist", "server", "index.js");
const worker = (await import(pathToFileURL(workerPath).href)).default;

const contentTypes = new Map([
  [".css", "text/css; charset=utf-8"],
  [".html", "text/html; charset=utf-8"],
  [".ico", "image/x-icon"],
  [".js", "text/javascript; charset=utf-8"],
  [".json", "application/json; charset=utf-8"],
  [".map", "application/json; charset=utf-8"],
  [".png", "image/png"],
  [".svg", "image/svg+xml"],
  [".txt", "text/plain; charset=utf-8"],
  [".webmanifest", "application/manifest+json"],
  [".woff", "font/woff"],
  [".woff2", "font/woff2"],
]);

function safeAssetPath(url) {
  let pathname;
  try {
    pathname = decodeURIComponent(new URL(url).pathname);
  } catch {
    return null;
  }
  const relative = pathname.replace(/^\/+/, "");
  const candidate = path.resolve(assetRoot, relative);
  if (candidate !== assetRoot && !candidate.startsWith(`${assetRoot}${path.sep}`)) return null;
  return candidate;
}

const assets = {
  async fetch(request) {
    const filePath = safeAssetPath(request.url);
    if (!filePath) return new Response("Not found", { status: 404 });
    try {
      const details = await stat(filePath);
      if (!details.isFile()) return new Response("Not found", { status: 404 });
      const stream = Readable.toWeb(createReadStream(filePath));
      return new Response(stream, {
        headers: {
          "content-length": String(details.size),
          "content-type": contentTypes.get(path.extname(filePath).toLowerCase()) || "application/octet-stream",
        },
      });
    } catch {
      return new Response("Not found", { status: 404 });
    }
  },
};

const environment = {
  ASSETS: assets,
};

function requestURL(request) {
  const forwarded = request.headers["x-forwarded-proto"];
  const protocol = typeof forwarded === "string" ? forwarded : "https";
  const host = request.headers.host || "snowball.local";
  return `${protocol}://${host}${request.url || "/"}`;
}

const server = http.createServer(async (request, response) => {
  try {
    const method = request.method || "GET";
    const url = requestURL(request);
    const pathname = new URL(url).pathname;
    if ((method === "GET" || method === "HEAD") && pathname.startsWith("/_next/")) {
      const assetResponse = await assets.fetch(new Request(url, { method }));
      response.writeHead(assetResponse.status, Object.fromEntries(assetResponse.headers));
      if (!assetResponse.body || method === "HEAD") response.end();
      else Readable.fromWeb(assetResponse.body).pipe(response);
      return;
    }
    const body = method === "GET" || method === "HEAD" ? undefined : Readable.toWeb(request);
    const webRequest = new Request(url, {
      method,
      headers: request.headers,
      body,
      ...(body ? { duplex: "half" } : {}),
    });
    const pending = [];
    const context = {
      waitUntil(promise) { pending.push(Promise.resolve(promise)); },
      passThroughOnException() {},
    };
    const webResponse = await worker.fetch(webRequest, environment, context);
    response.writeHead(webResponse.status, Object.fromEntries(webResponse.headers));
    if (!webResponse.body || method === "HEAD") response.end();
    else Readable.fromWeb(webResponse.body).pipe(response);
    if (pending.length > 0) void Promise.allSettled(pending);
  } catch (error) {
    console.error("Web request failed:", error);
    if (!response.headersSent) response.writeHead(500, { "content-type": "text/plain; charset=utf-8" });
    response.end("Snowball web service failed to render this request.");
  }
});

server.listen(listenPort, listenHost, () => {
  console.log(`snowball web listening on http://${listenHost}:${listenPort}`);
});

function shutdown() {
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(1), 5000).unref();
}

process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);
