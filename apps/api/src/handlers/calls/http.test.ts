import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer, type RequestListener } from "node:http";
import { test, type TestContext } from "node:test";
import {
  CamelCasePlugin,
  type CompiledQuery,
  DummyDriver,
  Kysely,
  PostgresAdapter,
  PostgresIntrospector,
  PostgresQueryCompiler,
  type QueryResult,
} from "kysely";
import type { Database } from "../../clients/database.ts";
import type { DB } from "../../clients/database.types.ts";
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
const AGENT_ID = "019a8f3e-7c1d-7b2a-9e4f-3c5d6e7f8a9b";
const STORED_AGENT = { provider: "cartesia", provider_agent_id: "agent_123" };

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
 * fakeDatabase builds a database that records each query instead of sending it.
 *
 * @param rows - The snake_case rows that every query returns, as PostgreSQL would.
 * @returns The database and the queries it has received.
 */
function fakeDatabase(rows: Record<string, unknown>[]): {
  database: Database;
  queries: CompiledQuery[];
} {
  const queries: CompiledQuery[] = [];

  // Answer every query with the given rows
  const driver = new DummyDriver();
  driver.acquireConnection = async () => ({
    async executeQuery<R>(query: CompiledQuery): Promise<QueryResult<R>> {
      queries.push(query);
      return { rows: rows as R[] };
    },
    async *streamQuery() {},
  });

  // Compile PostgreSQL with the same column mapping as createDatabase
  const database = new Kysely<DB>({
    dialect: {
      createAdapter: () => new PostgresAdapter(),
      createDriver: () => driver,
      createIntrospector: (db) => new PostgresIntrospector(db),
      createQueryCompiler: () => new PostgresQueryCompiler(),
    },
    plugins: [new CamelCasePlugin()],
  });
  return { database, queries };
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
  const { database, queries } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(
      loadConfiguration({ ...TEST_ENVIRONMENT, BRIDGE_URL: bridgeURL }),
      database,
    ),
  );
  const response = await postCall(
    url,
    JSON.stringify({ to: ALLOWED_PHONE_NUMBER, agentId: AGENT_ID }),
  );

  assert.equal(response.status, 202);
  assert.deepEqual(await response.json(), { callId: TEST_CALL_ID });
  assert.deepEqual(requests, [
    {
      method: "POST",
      path: "/call",
      authorization: `Bearer ${TEST_ENVIRONMENT.BRIDGE_API_TOKEN}`,
      body: {
        to: ALLOWED_PHONE_NUMBER,
        provider: "cartesia",
        providerAgentId: "agent_123",
      },
    },
  ]);
  assert.deepEqual(
    queries.map(({ sql, parameters }) => ({ sql, parameters })),
    [
      {
        sql: 'select "provider", "provider_agent_id" from "agents" where "id" = $1',
        parameters: [AGENT_ID],
      },
    ],
  );
});

test("POST /call rejects an unknown agentId with HTTP 404 before contacting the bridge", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: TEST_CALL_ID }));
  const { database } = fakeDatabase([]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database, { createCall }),
  );
  const response = await postCall(
    url,
    JSON.stringify({ to: ALLOWED_PHONE_NUMBER, agentId: AGENT_ID }),
  );

  assert.equal(response.status, 404);
  assert.deepEqual(await response.json(), {
    error: {
      code: "agent_not_found",
      message: "No agent exists with this agentId.",
    },
  });
  assert.equal(createCall.mock.callCount(), 0);
});

test("POST /call rejects other destination numbers with HTTP 403 before contacting the bridge", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: TEST_CALL_ID }));
  const { database } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database, { createCall }),
  );

  // Check numbers that are close to the allowed number
  for (const to of [
    "+15551234567",
    "+919452412002",
    "+19452412003",
    "+194524120020",
  ]) {
    const response = await postCall(
      url,
      JSON.stringify({ to, agentId: AGENT_ID }),
    );
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
  const { database } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database, { createCall }),
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
  const { database } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database, { createCall }),
  );

  const scenarios: {
    body: string;
    headers: Record<string, string>;
    status: number;
    code: string;
  }[] = [
    {
      body: JSON.stringify({ to: "x".repeat(5000), agentId: AGENT_ID }),
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
