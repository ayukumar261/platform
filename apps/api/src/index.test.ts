import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer, type RequestListener } from "node:http";
import { test, type TestContext } from "node:test";
import { createApp, loadConfiguration } from "./index.ts";
import { ForbiddenError } from "./lib/errors/index.ts";

// Constants
const TEST_ENVIRONMENT = {
  API_PORT: "3000",
  BRIDGE_API_TOKEN: "test-bridge-token",
  BRIDGE_URL: "http://127.0.0.1:8080",
};
const TEST_CALL_ID = "7f89fb58-646e-44f1-b91c-a430c06bc912";
const ALLOWED_PHONE_NUMBER = "+19452412002";

/**
 * listen serves a test handler on an unused local port.
 *
 * @param t - The test that closes the server when it ends.
 * @param handler - Answers each request.
 * @returns The server's base URL.
 */
async function listen(
  t: TestContext,
  handler: RequestListener,
): Promise<string> {
  const server = createServer(handler);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(async () => {
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) => {
      server.close((error) => (error ? reject(error) : resolve()));
    });
  });
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  return `http://127.0.0.1:${address.port}`;
}

/**
 * postCall sends POST /call with a JSON content type.
 *
 * @param url - The API's base URL.
 * @param body - The raw request body.
 * @param headers - Headers that add to or replace the JSON content type.
 * @returns The API's response.
 */
function postCall(
  url: string,
  body: string,
  headers: Record<string, string> = {},
): Promise<Response> {
  return fetch(`${url}/call`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...headers },
    body,
  });
}

test("health probes need no token and unknown routes return JSON", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: TEST_CALL_ID }));
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), { createCall }),
  );
  const health = await fetch(`${url}/health`);
  assert.equal(health.status, 200);
  assert.deepEqual(await health.json(), { status: "ok" });
  const missing = await fetch(`${url}/missing`);
  assert.equal(missing.status, 404);
  assert.deepEqual(await missing.json(), {
    error: {
      code: "not_found",
      message: "The requested endpoint does not exist.",
    },
  });
  assert.equal(createCall.mock.callCount(), 0);
});

test("named errors expose only their safe fields", async (t) => {
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), {
      async createCall() {
        throw new ForbiddenError({
          code: "call_not_allowed",
          message: "This call is not permitted.",
        });
      },
    }),
  );
  const response = await postCall(
    url,
    JSON.stringify({ to: ALLOWED_PHONE_NUMBER }),
  );
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), {
    error: { code: "call_not_allowed", message: "This call is not permitted." },
  });
});

test("unexpected errors receive a generic 500 without leaking their details", async (t) => {
  const logs = t.mock.method(console, "error", () => {});
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), {
      async createCall() {
        throw Object.assign(new Error("private-secret"), { status: 400 });
      },
    }),
  );
  const response = await postCall(
    url,
    JSON.stringify({ to: ALLOWED_PHONE_NUMBER }),
  );
  assert.equal(response.status, 500);
  assert.deepEqual(await response.json(), {
    error: { code: "internal_error", message: "An unexpected error occurred." },
  });
  assert.equal(logs.mock.callCount(), 1);
  assert.ok(
    !JSON.stringify(logs.mock.calls[0]?.arguments).includes("private-secret"),
  );
});

test("configuration uses the explicitly configured port", () => {
  assert.deepEqual(loadConfiguration(TEST_ENVIRONMENT), {
    port: 3000,
    bridgeApiToken: "test-bridge-token",
    bridgeURL: "http://127.0.0.1:8080",
  });
  assert.equal(
    loadConfiguration({ ...TEST_ENVIRONMENT, API_PORT: "4500" }).port,
    4500,
  );
});

test("configuration reports all missing settings without their values", () => {
  assert.throws(
    () => loadConfiguration({}),
    /BRIDGE_API_TOKEN, BRIDGE_URL, API_PORT/,
  );
  for (const name of Object.keys(TEST_ENVIRONMENT)) {
    for (const value of ["", " ", undefined]) {
      assert.throws(
        () => loadConfiguration({ ...TEST_ENVIRONMENT, [name]: value }),
        new RegExp(name),
      );
    }
  }
});

test("configuration rejects malformed ports, URLs, and tokens", () => {
  for (const API_PORT of ["", "0", "65536", "NaN", "3.5", "3000abc"]) {
    assert.throws(
      () => loadConfiguration({ ...TEST_ENVIRONMENT, API_PORT }),
      /API_PORT/,
    );
  }
  for (const BRIDGE_URL of [
    "invalid",
    "ws://bridge:8080",
    "http://bridge:8080/path",
    "http://user:private-secret@bridge:8080",
    "http://bridge:8080?token=private-secret",
  ]) {
    assert.throws(
      () => loadConfiguration({ ...TEST_ENVIRONMENT, BRIDGE_URL }),
      (error: unknown) => {
        assert.ok(error instanceof Error);
        assert.equal(
          error.message,
          "Invalid environment variables: BRIDGE_URL",
        );
        return true;
      },
    );
  }
  assert.throws(
    () =>
      loadConfiguration({
        ...TEST_ENVIRONMENT,
        BRIDGE_API_TOKEN: "private secret",
      }),
    /BRIDGE_API_TOKEN/,
  );
});
