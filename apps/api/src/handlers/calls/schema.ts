import { BadRequestError } from "../../lib/errors/index.ts";

// Constants
const FIELDS = ["to", "agentId"];

export interface CreateCallRequest {
  to: string;
  agentId: string;
}

/**
 * parseCreateCallRequest checks the JSON body before it can initiate a phone call.
 *
 * E.164 notation starts with +, followed by a country code and number, with
 * at most 15 digits. This checks the notation, not whether a number exists.
 * The agent ID must be a UUID, such as the id returned by POST /agents. The
 * server chooses the carrier and caller ID for this first version.
 *
 * @param body - The untrusted value parsed from the HTTP request.
 * @returns The destination number and agent ID in the accepted request shape.
 * @throws BadRequestError when the body, destination, or agent ID is invalid.
 */
export function parseCreateCallRequest(body: unknown): CreateCallRequest {
  // Require one JSON object
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    throw new BadRequestError({
      code: "invalid_request",
      message: "Provide a JSON object containing to and agentId.",
    });
  }

  // Reject fields that this endpoint does not support
  if (Object.keys(body).some((key) => !FIELDS.includes(key))) {
    throw new BadRequestError({
      code: "unknown_field",
      message: "Only the to and agentId fields are supported.",
    });
  }

  // Require an international destination number
  if (
    !("to" in body) ||
    typeof body.to !== "string" ||
    !/^\+[1-9]\d{1,14}$/.test(body.to)
  ) {
    throw new BadRequestError({
      code: "invalid_phone_number",
      message: "Provide to in international format, such as +15551234567.",
    });
  }

  // Require the ID of the agent that talks on the call
  if (
    !("agentId" in body) ||
    typeof body.agentId !== "string" ||
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      body.agentId,
    )
  ) {
    throw new BadRequestError({
      code: "invalid_agent_id",
      message: "Provide agentId as the UUID of a registered agent.",
    });
  }
  return { to: body.to, agentId: body.agentId };
}
