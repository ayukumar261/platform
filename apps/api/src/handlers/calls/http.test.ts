import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer, type RequestListener } from "node:http";
import { test, type TestContext } from "node:test";
import { createDatabase } from "../../clients/database.ts";
import { createApp, loadConfiguration } from "../../index.ts";

// Constants
const TEST_ENVIRONMENT = {
  API_PORT: "3000",
  BRIDGE_API_TOKEN: "test-bridge-token",
  BRIDGE_URL: "http://127.0.0.1:8080",
  DATABASE_URL: "postgres://postgres:postgres@127.0.0.1:5432/postgres",
};
const TEST_CALL_ID = "7f89fb58-646e-44f1-b91c-a430c06bc912";
const ALLOWED_PHONE_NUMBER = "+19452412002";
const DATABASE = createDatabase(TEST_ENVIRONMENT.DATABASE_URL);

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

test("POST /call accepts the allowed number without a caller token and authenticates to the bridge", async (t) => {
  const requests: {
    method?: string;
    path?: string;
    authorization?: string;
    body: unknown;
  }[] = [];

  // Record bridge requests without contacting a phone carrier
  const bridgeURL = await listen(t, async (req, res) => {
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(Buffer.from(chunk));
    requests.push({
      method: req.method,
      path: req.url,
      authorization: req.headers.authorization,
      body: JSON.parse(Buffer.concat(chunks).toString()),
    });
    res.writeHead(202, { "Content-Type": "application/json" });
    res.end(
      JSON.stringify({ callId: TEST_CALL_ID, internal: "private-detail" }),
    );
  });

  // Connect the real API and bridge client to the local substitute
  const url = await listen(
    t,
    createApp(
      loadConfiguration({ ...TEST_ENVIRONMENT, BRIDGE_URL: bridgeURL }),
      DATABASE,
    ),
  );
  const response = await postCall(
    url,
    JSON.stringify({ to: ALLOWED_PHONE_NUMBER }),
  );

  assert.equal(response.status, 202);
  assert.deepEqual(await response.json(), { callId: TEST_CALL_ID });
  assert.deepEqual(requests, [
    {
      method: "POST",
      path: "/call",
      authorization: `Bearer ${TEST_ENVIRONMENT.BRIDGE_API_TOKEN}`,
      body: { to: ALLOWED_PHONE_NUMBER },
    },
  ]);
});

test("POST /call rejects other destination numbers with HTTP 403 before contacting the bridge", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: TEST_CALL_ID }));
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), DATABASE, { createCall }),
  );

  // Check numbers that are close to the allowed number
  for (const to of [
    "+15551234567",
    "+919452412002",
    "+19452412003",
    "+194524120020",
  ]) {
    const response = await postCall(url, JSON.stringify({ to }));
    assert.equal(response.status, 403, to);
    assert.deepEqual(await response.json(), {
      error: {
        code: "destination_not_allowed",
        message: "Calls are limited to the configured test number.",
      },
    });
  }
  assert.equal(createCall.mock.callCount(), 0);
});

test("POST /call rejects invalid input with HTTP 400 before contacting the bridge", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: TEST_CALL_ID }));
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), DATABASE, { createCall }),
  );

  // Check empty bodies, malformed JSON, and a schema validation failure
  for (const scenario of [
    { body: "", code: "invalid_phone_number" },
    { body: "{", code: "invalid_json" },
    { body: '{"to":"9452412002"}', code: "invalid_phone_number" },
  ]) {
    const response = await postCall(url, scenario.body);
    assert.equal(response.status, 400, scenario.body);
    const result = (await response.json()) as {
      error: { code: string; message: string };
    };
    assert.deepEqual(Object.keys(result.error).sort(), ["code", "message"]);
    assert.equal(result.error.code, scenario.code, scenario.body);
  }
  assert.equal(createCall.mock.callCount(), 0);
});

test("POST /call rejects oversized and unsupported bodies before contacting the bridge", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: TEST_CALL_ID }));
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), DATABASE, { createCall }),
  );

  const scenarios: {
    body: string;
    headers: Record<string, string>;
    status: number;
    code: string;
  }[] = [
    {
      body: JSON.stringify({ to: "x".repeat(5000) }),
      headers: {},
      status: 413,
      code: "payload_too_large",
    },
    {
      body: "{}",
      headers: { "Content-Type": "text/plain" },
      status: 415,
      code: "unsupported_media_type",
    },
    {
      body: "{}",
      headers: { "Content-Type": "application/json; charset=iso-8859-1" },
      status: 415,
      code: "unsupported_media_type",
    },
    {
      body: "{}",
      headers: { "Content-Encoding": "gzip" },
      status: 415,
      code: "unsupported_media_type",
    },
  ];
  for (const scenario of scenarios) {
    const response = await postCall(url, scenario.body, scenario.headers);
    assert.equal(response.status, scenario.status, scenario.code);
    const result = (await response.json()) as { error: { code: string } };
    assert.equal(result.error.code, scenario.code);
  }
  assert.equal(createCall.mock.callCount(), 0);
});
