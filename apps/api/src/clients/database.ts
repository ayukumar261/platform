import { CamelCasePlugin, Kysely, PostgresDialect } from "kysely";
import { Pool } from "pg";
import type { DB } from "./database.types.ts";

export type Database = Kysely<DB>;

/**
 * createDatabase prepares a PostgreSQL connection pool without connecting.
 *
 * Atlas owns the schema in db/schema.sql. Run `pnpm --filter api db:types`
 * after a migration to regenerate database.types.ts from the database. The
 * CamelCasePlugin maps columns such as provider_agent_id to providerAgentId.
 *
 * @param url - The PostgreSQL connection URL, already checked by the configuration.
 * @returns A Kysely instance; call destroy() to close its connections.
 */
export function createDatabase(url: string): Database {
  const pool = new Pool({
    connectionString: url,
    connectionTimeoutMillis: 5000,
  });

  // Log idle connection failures instead of crashing the process
  pool.on("error", (error) => {
    console.error("Idle database connection failed", {
      code: "code" in error ? error.code : undefined,
    });
  });

  return new Kysely<DB>({
    dialect: new PostgresDialect({ pool }),
    plugins: [new CamelCasePlugin()],
  });
}
