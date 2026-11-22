import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer, type RequestListener } from "node:http";
import { test, type TestContext } from "node:test";
import {
  BadGatewayError,
  ServiceUnavailableError,
} from "../lib/errors/index.ts";
import { BridgeClient } from "./bridge.ts";

// Constants
const TO = "+15551234567";
const BRIDGE_API_TOKEN = "private-token";

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

test("failed bridge responses are mapped without leaking bodies or retrying", async (t) => {
  for (const status of [400, 401, 403, 500, 503]) {
    let requests = 0;
    const url = await listen(t, (req, res) => {
      requests++;
      req.resume();
      res.writeHead(status);
      res.end("private-provider-detail");
    });
    await assert.rejects(
      new BridgeClient(url, BRIDGE_API_TOKEN).createCall(TO),
      (error: unknown) => {
        assert.ok(
          error instanceof
            (status === 503 ? ServiceUnavailableError : BadGatewayError),
        );
        assert.ok(!error.message.includes("private-provider-detail"));
        return true;
      },
    );
    assert.equal(requests, 1);
  }
});

test("invalid acceptance replies cannot become successful API responses", async (t) => {
  for (const body of [
    "not JSON",
    "null",
    "{}",
    '{"callId":"wrong"}',
    '{"callId":123}',
  ]) {
    let requests = 0;
    const url = await listen(t, (req, res) => {
      requests++;
      req.resume();
      res.writeHead(202, { "Content-Type": "application/json" });
      res.end(body);
    });
    await assert.rejects(
      new BridgeClient(url, BRIDGE_API_TOKEN).createCall(TO),
      BadGatewayError,
    );
    assert.equal(requests, 1);
  }
});

test("deadlines cover both response headers and the response body", async (t) => {
  for (const sendHeaders of [false, true]) {
    let requests = 0;
    const url = await listen(t, (req, res) => {
      requests++;
      req.resume();
      if (sendHeaders) {
        res.writeHead(202, { "Content-Type": "application/json" });
        res.flushHeaders();
      }
    });
    await assert.rejects(
      new BridgeClient(url, BRIDGE_API_TOKEN, 100).createCall(TO),
      sendHeaders ? BadGatewayError : ServiceUnavailableError,
    );
    assert.equal(requests, 1);
  }
});

test("redirects cannot forward bridge credentials or start a second request", async (t) => {
  let forwarded = 0;
  const destination = await listen(t, (req, res) => {
    forwarded++;
    req.resume();
    res.end();
  });
  const url = await listen(t, (req, res) => {
    req.resume();
    res.writeHead(307, { Location: `${destination}/call` });
    res.end();
  });
  await assert.rejects(
    new BridgeClient(url, BRIDGE_API_TOKEN).createCall(TO),
    ServiceUnavailableError,
  );
  assert.equal(forwarded, 0);
});

test("connection failures become a service-unavailable error", async (t) => {
  const url = await listen(t, (req) => req.socket.destroy());
  await assert.rejects(
    new BridgeClient(url, BRIDGE_API_TOKEN).createCall(TO),
    ServiceUnavailableError,
  );
});
