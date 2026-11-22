import process from "node:process";
import express from "express";
import { BridgeClient } from "./clients/bridge.ts";
import type { Bridge } from "./clients/bridge.types.ts";
import { createCall } from "./handlers/calls/http.ts";
import { NotFoundError } from "./lib/errors/index.ts";
import { errorHandler } from "./lib/errors/handler.ts";

export interface Configuration {
  port: number;
  bridgeURL: string;
  bridgeApiToken: string;
}

function main(): void {
  // Read the settings before accepting requests
  const configuration = loadConfiguration();
  const app = createApp(configuration);

  // Start the HTTP server
  const server = app.listen(configuration.port, () => {
    console.info(`API listening on port ${configuration.port}`);
  });

  // Let active requests finish when the process stops
  for (const signal of ["SIGINT", "SIGTERM"] as const) {
    process.once(signal, () => {
      server.close();
      setTimeout(() => server.closeAllConnections(), 5000).unref();
    });
  }
}

/**
 * loadConfiguration reads and checks the API's environment variables.
 *
 * The development command loads the repository's .env file before this runs.
 * Docker supplies these values directly to the process.
 *
 * @param environment - Variables to read; defaults to the current process.
 * @returns Settings ready to use without contacting another service.
 * @throws Error when required settings are missing or malformed.
 */
export function loadConfiguration(
  environment: typeof process.env = process.env,
): Configuration {
  // Collect the names of invalid settings
  const invalid = new Set<string>();
  const required = (name: string): string => {
    const value = environment[name] ?? "";
    if (value.trim() === "") invalid.add(name);
    return value;
  };

  // Read the private bridge credential without including it in errors
  const bridgeApiToken = required("BRIDGE_API_TOKEN");
  if (/\s/.test(bridgeApiToken)) invalid.add("BRIDGE_API_TOKEN");

  // Require an HTTP origin without embedded credentials or a path
  const bridgeURL = required("BRIDGE_URL");
  try {
    const url = new URL(bridgeURL);
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      url.pathname !== "/"
    )
      invalid.add("BRIDGE_URL");
  } catch {
    invalid.add("BRIDGE_URL");
  }

  // Require a valid listening port
  const portText = required("API_PORT");
  const port = Number(portText);
  if (!/^\d+$/.test(portText) || port < 1 || port > 65535) {
    invalid.add("API_PORT");
  }

  // Report every invalid setting at once
  if (invalid.size > 0) {
    throw new Error(
      `Invalid environment variables: ${[...invalid].join(", ")}`,
    );
  }
  return { port, bridgeURL, bridgeApiToken };
}

/**
 * createApp assembles the API without opening a listening socket.
 *
 * Keeping construction separate from startup lets tests use a local bridge
 * substitute, so checking the API cannot accidentally place a real call.
 *
 * @param configuration - Validated settings for this process.
 * @param bridge - An optional bridge substitute for tests.
 * @returns The configured Express application.
 */
export function createApp(
  configuration: Configuration,
  bridge: Bridge = new BridgeClient(
    configuration.bridgeURL,
    configuration.bridgeApiToken,
  ),
): express.Express {
  const app = express();
  app.disable("x-powered-by");

  // Allow health probes without credentials
  app.get("/health", (_req, res) => {
    res.json({ status: "ok" });
  });

  // Read call requests before checking the destination allowlist
  app.post(
    "/call",
    express.json({ limit: "4kb", inflate: false }),
    createCall(bridge),
  );

  // Report unmatched routes using the same error format
  app.use((_req, _res) => {
    throw new NotFoundError({
      code: "not_found",
      message: "The requested endpoint does not exist.",
    });
  });

  // Handle errors after every route
  app.use(errorHandler);
  return app;
}

// Start the server only when this file is run directly
if (import.meta.main) main();
