import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer, type RequestListener } from "node:http";
import { test, type TestContext } from "node:test";
import { createApp } from "../../index.ts";

/** Serve a test handler on an unused local port and close it afterward. */
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

test("POST /call accepts the allowed number without a caller token and authenticates to the bridge", async (t) => {
  const callId = "7f89fb58-646e-44f1-b91c-a430c06bc912";
  const bridgeApiToken = "test-bridge-token";
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
    res.end(JSON.stringify({ callId, internal: "private-detail" }));
  });

  // Connect the real API and bridge client to the local substitute
  const url = await listen(
    t,
    createApp({ port: 3000, bridgeURL, bridgeApiToken }),
  );
  const response = await fetch(`${url}/call`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ to: "+19452412002" }),
  });

  assert.equal(response.status, 202);
  assert.deepEqual(await response.json(), { callId });
  assert.deepEqual(requests, [
    {
      method: "POST",
      path: "/call",
      authorization: `Bearer ${bridgeApiToken}`,
      body: { to: "+19452412002" },
    },
  ]);
});

test("POST /call rejects invalid input with HTTP 400 before contacting the bridge", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: "unexpected-call" }));
  const url = await listen(
    t,
    createApp(
      {
        port: 3000,
        bridgeURL: "http://127.0.0.1:8080",
        bridgeApiToken: "test-bridge-token",
      },
      { createCall },
    ),
  );

  // Check empty bodies, malformed JSON, and a schema validation failure
  for (const body of ["", "{", '{"to":"9452412002"}']) {
    const response = await fetch(`${url}/call`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    });
    assert.equal(response.status, 400, body);
    const result = (await response.json()) as {
      error: { code: string; message: string };
    };
    assert.deepEqual(Object.keys(result.error).sort(), ["code", "message"]);
  }
  assert.equal(createCall.mock.callCount(), 0);
});
