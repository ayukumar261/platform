import assert from "node:assert/strict";
import { test } from "node:test";
import { createDatabase } from "./database.ts";

// Constants
const DATABASE_URL = "postgres://postgres:postgres@127.0.0.1:5432/postgres";

test("queries map camelCase properties to snake_case columns without connecting", async (t) => {
  const database = createDatabase(DATABASE_URL);
  t.after(() => database.destroy());

  // Compile a query without sending it
  const query = database
    .selectFrom("agents")
    .select(["id", "providerAgentId"])
    .where("provider", "=", "cartesia")
    .compile();
  assert.equal(
    query.sql,
    'select "id", "provider_agent_id" from "agents" where "provider" = $1',
  );
  assert.deepEqual(query.parameters, ["cartesia"]);
});
