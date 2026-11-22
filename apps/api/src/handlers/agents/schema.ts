import { BadRequestError } from "../../lib/errors/index.ts";

// Constants
const PROVIDERS = ["cartesia"];
const FIELDS = ["name", "provider", "providerAgentId"];

export interface CreateAgentRequest {
  name: string;
  provider: string;
  providerAgentId: string;
}

/**
 * parseCreateAgentRequest checks the JSON body before it can add an agent.
 *
 * The agent must already exist at its provider, such as Cartesia. This only
 * records the provider's agent ID so calls can refer to it later. The
 * supported providers match the agents.provider CHECK in db/schema.sql.
 *
 * @param body - The untrusted value parsed from the HTTP request.
 * @returns The agent's name, provider, and provider agent ID.
 * @throws BadRequestError when the body or one of its fields is invalid.
 */
export function parseCreateAgentRequest(body: unknown): CreateAgentRequest {
  // Require one JSON object
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    throw new BadRequestError({
      code: "invalid_request",
      message:
        "Provide a JSON object containing name, provider, and providerAgentId.",
    });
  }

  // Reject fields that this endpoint does not support
  if (Object.keys(body).some((key) => !FIELDS.includes(key))) {
    throw new BadRequestError({
      code: "unknown_field",
      message:
        "Only the name, provider, and providerAgentId fields are supported.",
    });
  }

  // Require a name that is not blank
  if (
    !("name" in body) ||
    typeof body.name !== "string" ||
    body.name.trim() === ""
  ) {
    throw new BadRequestError({
      code: "invalid_name",
      message: "Provide name as a non-empty string.",
    });
  }

  // Require a supported provider
  if (
    !("provider" in body) ||
    typeof body.provider !== "string" ||
    !PROVIDERS.includes(body.provider)
  ) {
    throw new BadRequestError({
      code: "unsupported_provider",
      message: `Provide provider as one of: ${PROVIDERS.join(", ")}.`,
    });
  }

  // Require the provider's agent ID without whitespace
  if (
    !("providerAgentId" in body) ||
    typeof body.providerAgentId !== "string" ||
    !/^\S+$/.test(body.providerAgentId)
  ) {
    throw new BadRequestError({
      code: "invalid_provider_agent_id",
      message: "Provide providerAgentId as a string without spaces.",
    });
  }
  return {
    name: body.name,
    provider: body.provider,
    providerAgentId: body.providerAgentId,
  };
}
