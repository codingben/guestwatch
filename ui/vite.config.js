import { statSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const mockObservationsPath = fileURLToPath(
  new URL("./mock/observations.js", import.meta.url),
);

async function loadMockModule() {
  const mtimeMs = statSync(mockObservationsPath).mtimeMs;
  return import(`${pathToFileURL(mockObservationsPath).href}?mtime=${mtimeMs}`);
}

// Simulates the real endpoint's latency so VITE_MOCK=true also exercises
// the "Investigating…" state.
const TRIAGE_DELAY_MS = 2500;
const TRIAGE_PATH_RE = /^\/api\/vms\/([^/]+)\/([^/]+)\/triage$/;

const pendingTriage = new Map();

function mockApiPlugin() {
  return {
    name: "guestwatch-mock-api",
    configureServer(server) {
      if (process.env.VITE_MOCK !== "true") return;

      server.middlewares.use("/api/observations", async (req, res) => {
        const { buildMockObservations } = await loadMockModule();
        res.setHeader("Content-Type", "application/json");
        res.end(JSON.stringify(buildMockObservations()));
      });

      server.middlewares.use(async (req, res, next) => {
        const match = req.url.match(TRIAGE_PATH_RE);
        if (!match) {
          next();
          return;
        }
        const [, namespace, name] = match;
        const key = `${decodeURIComponent(namespace)}/${decodeURIComponent(name)}`;

        if (req.method === "DELETE") {
          const timer = pendingTriage.get(key);
          if (!timer) {
            res.statusCode = 404;
            res.end("no investigation is currently running for this VM");
            return;
          }
          clearTimeout(timer);
          pendingTriage.delete(key);
          res.statusCode = 202;
          res.end();
          return;
        }

        if (req.method !== "POST") {
          next();
          return;
        }

        const { buildMockTriageRecord } = await loadMockModule();
        const timer = setTimeout(() => {
          pendingTriage.delete(key);

          if (res.writableEnded) return;
          res.setHeader("Content-Type", "application/json");
          res.end(
            JSON.stringify(
              buildMockTriageRecord(
                decodeURIComponent(namespace),
                decodeURIComponent(name),
              ),
            ),
          );
        }, TRIAGE_DELAY_MS);
        pendingTriage.set(key, timer);
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), mockApiPlugin()],
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
});
