import { BadRequestError } from "../../lib/errors/index.ts";

export interface CreateCallRequest {
  to: string;
}

/**
 * parseCreateCallRequest checks the JSON body before it can initiate a phone call.
 *
 * E.164 notation starts with +, followed by a country code and number, with
 * at most 15 digits. This checks the notation, not whether a number exists.
 * The server chooses the carrier, caller ID, and agent for this first version.
 *
 * @param body - The untrusted value parsed from the HTTP request.
 * @returns The destination number in the accepted request shape.
 * @throws BadRequestError when the body or destination is invalid.
 */
export function parseCreateCallRequest(body: unknown): CreateCallRequest {
  // Require one JSON object
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    throw new BadRequestError({
      code: "invalid_request",
      message: "Provide a JSON object containing to.",
    });
  }

  // Reject fields that this endpoint does not support
  if (Object.keys(body).some((key) => key !== "to")) {
    throw new BadRequestError({
      code: "unknown_field",
      message: "Only the to field is supported.",
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
  return { to: body.to };
}
