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
import type { Database } from "./clients/database.ts";
import type { DB } from "./clients/database.types.ts";
import { createApp, loadConfiguration } from "./index.ts";
import { ForbiddenError } from "./lib/errors/index.ts";

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

test("health probes need no token and unknown routes return JSON", async (t) => {
  const createCall = t.mock.fn(async () => ({ callId: TEST_CALL_ID }));
  const { database } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database, { createCall }),
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
  const { database } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database, {
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
    JSON.stringify({ to: ALLOWED_PHONE_NUMBER, agentId: AGENT_ID }),
  );
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), {
    error: { code: "call_not_allowed", message: "This call is not permitted." },
  });
});

test("unexpected errors receive a generic 500 without leaking their details", async (t) => {
  const logs = t.mock.method(console, "error", () => {});
  const { database } = fakeDatabase([STORED_AGENT]);
  const url = await listen(
    t,
    createApp(loadConfiguration(TEST_ENVIRONMENT), database, {
      async createCall() {
        throw Object.assign(new Error("private-secret"), { status: 400 });
      },
    }),
  );
  const response = await postCall(
    url,
    JSON.stringify({ to: ALLOWED_PHONE_NUMBER, agentId: AGENT_ID }),
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
    databaseURL: "postgres://postgres:postgres@127.0.0.1:5432/postgres",
  });
  assert.equal(
    loadConfiguration({ ...TEST_ENVIRONMENT, API_PORT: "4500" }).port,
    4500,
  );
});

test("configuration reports all missing settings without their values", () => {
  assert.throws(
    () => loadConfiguration({}),
    /BRIDGE_API_TOKEN, BRIDGE_URL, API_PORT, DATABASE_URL/,
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
  for (const DATABASE_URL of [
    "invalid",
    "postgres://",
    "mysql://postgres:private-secret@127.0.0.1:3306/postgres",
    "http://postgres:private-secret@127.0.0.1:5432/postgres",
    "postgres://postgres:private-secret@:5432/postgres",
    "postgres://postgres:private-secret@127.0.0.1/postgres",
    "postgres://:private-secret@127.0.0.1:5432/postgres",
    "postgres://postgres@127.0.0.1:5432/postgres",
    "postgres://postgres:private-secret@127.0.0.1:5432",
    "postgres://postgres:private-secret@127.0.0.1:5432/",
  ]) {
    assert.throws(
      () => loadConfiguration({ ...TEST_ENVIRONMENT, DATABASE_URL }),
      (error: unknown) => {
        assert.ok(error instanceof Error);
        assert.equal(
          error.message,
          "Invalid environment variables: DATABASE_URL",
        );
        return true;
      },
    );
  }
  assert.equal(
    loadConfiguration({
      ...TEST_ENVIRONMENT,
      DATABASE_URL: "postgresql://postgres:postgres@postgres:5432/postgres",
    }).databaseURL,
    "postgresql://postgres:postgres@postgres:5432/postgres",
  );
});
