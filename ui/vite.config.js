import { statSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const mockObservationsPath = fileURLToPath(
  new URL("./mock/observations.js", import.meta.url),
);

async function loadMockObservations() {
  const mtimeMs = statSync(mockObservationsPath).mtimeMs;
  const module = await import(
    `${pathToFileURL(mockObservationsPath).href}?mtime=${mtimeMs}`
  );
  return module.buildMockObservations();
}

function mockApiPlugin() {
  return {
    name: "guestwatch-mock-api",
    configureServer(server) {
      if (process.env.VITE_MOCK !== "true") return;
      server.middlewares.use("/api/observations", async (req, res) => {
        const data = await loadMockObservations();
        res.setHeader("Content-Type", "application/json");
        res.end(JSON.stringify(data));
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
