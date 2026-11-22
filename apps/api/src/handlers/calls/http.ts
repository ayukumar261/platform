import type { RequestHandler } from "express";
import type { Bridge } from "../../clients/bridge.types.ts";
import {
  ForbiddenError,
  UnsupportedMediaTypeError,
} from "../../lib/errors/index.ts";
import { parseCreateCallRequest } from "./schema.ts";

const allowedPhoneNumber = "+19452412002";

/**
 * createCall handles POST /call with { "to": "+19452412002" }.
 *
 * The steps are:
 * 1. Check the body format and destination number.
 * 2. Require the owner's test number as the destination.
 * 3. Ask the bridge to accept the call and return HTTP 202 with { callId }.
 *
 * This development endpoint has no caller authentication. Anyone who can
 * reach it can request a call, but only to the number allowed above.
 *
 * Acceptance means the bridge scheduled the work in memory. It does not mean
 * the phone answered, and pending work does not survive a bridge restart.
 * Later dialing failures appear in the bridge logs under the same call ID.
 *
 * @param bridge - The client that sends requests to the Go bridge.
 * @returns An Express handler whose errors reach the shared error middleware.
 */
export function createCall(bridge: Bridge): RequestHandler {
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

    // Validate the destination before contacting the bridge
    const request = parseCreateCallRequest(req.body);

    // Limit test calls to the owner's phone
    if (request.to !== allowedPhoneNumber) {
      throw new ForbiddenError({
        code: "destination_not_allowed",
        message: "Calls are limited to the configured test number.",
      });
    }

    // Wait for acceptance without waiting for the conversation
    const call = await bridge.createCall(request.to);
    res.status(202).json(call);
  };
}
