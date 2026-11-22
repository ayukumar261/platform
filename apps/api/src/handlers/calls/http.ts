import type { RequestHandler } from "express";
import type { Bridge } from "../../clients/bridge.types.ts";
import type { Database } from "../../clients/database.ts";
import {
  ForbiddenError,
  NotFoundError,
  UnsupportedMediaTypeError,
} from "../../lib/errors/index.ts";
import { parseCreateCallRequest } from "./schema.ts";

// Constants
// TODO: Delete when Corbyn AI is live
const ALLOWED_PHONE_NUMBER = "+19452412002";

/**
 * createCall handles POST /call, which asks the bridge to call a number.
 *
 * The steps are:
 * 1. Check the body format, destination number, and agent ID.
 * 2. Require the owner's test number as the destination.
 * 3. Find the agent that talks on the call.
 * 4. Ask the bridge to accept the call and return HTTP 202 with the call ID.
 *
 * This development endpoint has no caller authentication. Anyone who can
 * reach it can request a call, but only to the number allowed above.
 *
 * Acceptance means the bridge scheduled the work in memory. It does not mean
 * the phone answered, and pending work does not survive a bridge restart.
 * Later dialing failures appear in the bridge logs under the same call ID.
 *
 * @param bridge - The client that sends requests to the Go bridge.
 * @param database - The PostgreSQL database that stores agents.
 * @returns An Express handler whose errors reach the shared error middleware.
 */
export function createCall(bridge: Bridge, database: Database): RequestHandler {
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

    // Validate the destination and agent ID before contacting the bridge
    const request = parseCreateCallRequest(req.body);

    // Limit test calls to the owner's phone
    if (request.to !== ALLOWED_PHONE_NUMBER) {
      throw new ForbiddenError({
        code: "destination_not_allowed",
        message: "Calls are limited to the configured test number.",
      });
    }

    // Find the agent before contacting the bridge
    const agent = await database
      .selectFrom("agents")
      .select(["provider", "providerAgentId"])
      .where("id", "=", request.agentId)
      .executeTakeFirst();
    if (!agent) {
      throw new NotFoundError({
        code: "agent_not_found",
        message: "No agent exists with this agentId.",
      });
    }

    // Wait for acceptance without waiting for the conversation
    const call = await bridge.createCall({
      to: request.to,
      provider: agent.provider,
      providerAgentId: agent.providerAgentId,
    });
    res.status(202).json(call);
  };
}
