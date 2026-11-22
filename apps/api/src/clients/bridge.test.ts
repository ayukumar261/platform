import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer, type RequestListener } from "node:http";
import { test, type TestContext } from "node:test";
import { BridgeClient } from "./bridge.ts";
import {
  BadGatewayError,
  ServiceUnavailableError,
} from "../lib/errors/index.ts";

/**
 * serve runs a bridge substitute that cannot contact a phone carrier.
 *
 * @param t - The test that closes the server when it ends.
 * @param handler - Answers each request in place of the bridge.
 * @returns The substitute's base URL.
 */
async function serve(
  t: TestContext,
  handler: RequestListener,
): Promise<string> {
  const server = createServer(handler);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(async () => {
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  return `http://127.0.0.1:${address.port}`;
}

test("failed bridge responses are mapped without leaking bodies or retrying", async (t) => {
  for (const status of [400, 401, 403, 500, 503]) {
    let requests = 0;
    const url = await serve(t, (req, res) => {
      requests++;
      req.resume();
      res.writeHead(status);
      res.end("private-provider-detail");
    });
    await assert.rejects(
      new BridgeClient(url, "private-token").createCall("+15551234567"),
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
    const url = await serve(t, (req, res) => {
      requests++;
      req.resume();
      res.writeHead(202, { "Content-Type": "application/json" });
      res.end(body);
    });
    await assert.rejects(
      new BridgeClient(url, "private-token").createCall("+15551234567"),
      BadGatewayError,
    );
    assert.equal(requests, 1);
  }
});

test("deadlines cover both response headers and the response body", async (t) => {
  for (const sendHeaders of [false, true]) {
    let requests = 0;
    const url = await serve(t, (req, res) => {
      requests++;
      req.resume();
      if (sendHeaders) {
        res.writeHead(202, { "Content-Type": "application/json" });
        res.flushHeaders();
      }
    });
    await assert.rejects(
      new BridgeClient(url, "private-token", 100).createCall("+15551234567"),
      sendHeaders ? BadGatewayError : ServiceUnavailableError,
    );
    assert.equal(requests, 1);
  }
});

test("redirects cannot forward bridge credentials or start a second request", async (t) => {
  let forwarded = 0;
  const destination = await serve(t, (req, res) => {
    forwarded++;
    req.resume();
    res.end();
  });
  const url = await serve(t, (req, res) => {
    req.resume();
    res.writeHead(307, { Location: `${destination}/call` });
    res.end();
  });
  await assert.rejects(
    new BridgeClient(url, "private-token").createCall("+15551234567"),
    ServiceUnavailableError,
  );
  assert.equal(forwarded, 0);
});

test("connection failures become a service-unavailable error", async (t) => {
  const url = await serve(t, (req) => req.socket.destroy());
  await assert.rejects(
    new BridgeClient(url, "private-token").createCall("+15551234567"),
    ServiceUnavailableError,
  );
});
