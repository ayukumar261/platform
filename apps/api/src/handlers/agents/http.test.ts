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
const AGENT = {
  name: "Support",
  provider: "cartesia",
  providerAgentId: "agent_123",
};
const STORED_AGENT = {
  id: "019a8f3e-7c1d-7b2a-9e4f-3c5d6e7f8a9b",
  name: "Support",
  provider: "cartesia",
  provider_agent_id: "agent_123",
  created_at: new Date("2026-11-21T12:00:00.000Z"),
  updated_at: new Date("2026-11-21T12:00:00.000Z"),
};

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
 * postAgent sends POST /agents with a JSON content type.
 *
 * @param url - The API's base URL.
 * @param body - The raw request body.
 * @param headers - Headers that add to or replace the JSON content type.
 * @returns The API's response.
 */
function postAgent(
  url: string,
  body: string,
  headers: Record<string, string> = {},
): Promise<Response> {
  return fetch(`${url}/agents`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...headers },
    body,
  });
}

test("POST /agents stores the agent and returns it with HTTP 201", async (t) => {
  const { database, queries } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database),
  );
  const response = await postAgent(url, JSON.stringify(AGENT));

  assert.equal(response.status, 201);
  assert.deepEqual(await response.json(), {
    id: STORED_AGENT.id,
    name: "Support",
    provider: "cartesia",
    providerAgentId: "agent_123",
    createdAt: "2026-11-21T12:00:00.000Z",
    updatedAt: "2026-11-21T12:00:00.000Z",
  });
  assert.deepEqual(
    queries.map(({ sql, parameters }) => ({ sql, parameters })),
    [
      {
        sql: 'insert into "agents" ("name", "provider", "provider_agent_id") values ($1, $2, $3) on conflict ("provider", "provider_agent_id") do nothing returning *',
        parameters: ["Support", "cartesia", "agent_123"],
      },
    ],
  );
});

test("POST /agents rejects an already registered provider agent ID with HTTP 409", async (t) => {
  const { database } = fakeDatabase([]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database),
  );
  const response = await postAgent(url, JSON.stringify(AGENT));

  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), {
    error: {
      code: "agent_exists",
      message: "An agent with this provider agent ID already exists.",
    },
  });
});

test("POST /agents rejects invalid input with HTTP 400 before querying", async (t) => {
  const { database, queries } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database),
  );

  // Check empty bodies, malformed JSON, and a schema validation failure
  for (const scenario of [
    { body: "", code: "invalid_name" },
    { body: "{", code: "invalid_json" },
    {
      body: JSON.stringify({ ...AGENT, provider: "elevenlabs" }),
      code: "unsupported_provider",
    },
  ]) {
    const response = await postAgent(url, scenario.body);
    assert.equal(response.status, 400, scenario.body);
    const result = (await response.json()) as { error: { code: string } };
    assert.equal(result.error.code, scenario.code, scenario.body);
  }
  assert.equal(queries.length, 0);
});

test("POST /agents rejects oversized and unsupported bodies before querying", async (t) => {
  const { database, queries } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database),
  );

  const scenarios: {
    body: string;
    headers: Record<string, string>;
    status: number;
    code: string;
  }[] = [
    {
      body: JSON.stringify({ ...AGENT, name: "x".repeat(5000) }),
      headers: {},
      status: 413,
      code: "payload_too_large",
    },
    {
      body: JSON.stringify(AGENT),
      headers: { "Content-Type": "text/plain" },
      status: 415,
      code: "unsupported_media_type",
    },
  ];
  for (const scenario of scenarios) {
    const response = await postAgent(url, scenario.body, scenario.headers);
    assert.equal(response.status, scenario.status, scenario.code);
    const result = (await response.json()) as { error: { code: string } };
    assert.equal(result.error.code, scenario.code);
  }
  assert.equal(queries.length, 0);
});
