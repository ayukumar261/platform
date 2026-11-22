import type { RequestHandler } from "express";
import type { Database } from "../../clients/database.ts";
import {
  ConflictError,
  UnsupportedMediaTypeError,
} from "../../lib/errors/index.ts";
import { parseCreateAgentRequest } from "./schema.ts";

/**
 * createAgent handles POST /agents, which records an agent built at a provider.
 *
 * The steps are:
 * 1. Check the body's name, provider, and provider agent ID.
 * 2. Add the agent unless its provider agent ID is already registered.
 * 3. Return HTTP 201 with the stored agent.
 *
 * This development endpoint has no caller authentication. Anyone who can
 * reach it can add agents.
 *
 * @param database - The PostgreSQL database that stores agents.
 * @returns An Express handler whose errors reach the shared error middleware.
 */
export function createAgent(database: Database): RequestHandler {
  return async (req, res) => {
    // Require a JSON request body
    const contentType = req
      .get("content-type")
      ?.split(";")[0]
      ?.trim()
      .toLowerCase();
    if (contentType !== "application/json") {
      throw new UnsupportedMediaTypeError({
        code: "unsupported_media_type",
        message: "Set Content-Type to application/json.",
      });
    }

    // Validate the agent before writing it
    const request = parseCreateAgentRequest(req.body);

    // Add the agent, skipping one whose provider agent ID already exists
    const agent = await database
      .insertInto("agents")
      .values(request)
      .onConflict((conflict) =>
        conflict.columns(["provider", "providerAgentId"]).doNothing(),
      )
      .returningAll()
      .executeTakeFirst();
    if (!agent) {
      throw new ConflictError({
        code: "agent_exists",
        message: "An agent with this provider agent ID already exists.",
      });
    }
    res.status(201).json(agent);
  };
}
