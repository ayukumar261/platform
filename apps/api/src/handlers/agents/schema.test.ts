import assert from "node:assert/strict";
import { test } from "node:test";
import { BadRequestError } from "../../lib/errors/index.ts";
import { parseCreateAgentRequest } from "./schema.ts";

// Constants
const AGENT = {
  name: "Support",
  provider: "cartesia",
  providerAgentId: "agent_123",
};

/**
 * expectBadRequest checks that parsing body fails with a BadRequestError.
 *
 * @param body - The invalid request body.
 * @param code - The error code the body should produce.
 */
function expectBadRequest(body: unknown, code: string): void {
  assert.throws(
    () => parseCreateAgentRequest(body),
    (error: unknown) => {
      assert.ok(error instanceof BadRequestError);
      assert.equal(error.code, code);
      return true;
    },
    `Body: ${JSON.stringify(body)}`,
  );
}

test("parseCreateAgentRequest accepts a named agent from a supported provider", () => {
  assert.deepEqual(parseCreateAgentRequest(AGENT), AGENT);
});

test("parseCreateAgentRequest rejects non-object bodies with invalid_request", () => {
  for (const body of [undefined, null, [], true, 123, "agent_123"]) {
    expectBadRequest(body, "invalid_request");
  }
});

test("parseCreateAgentRequest rejects extra fields with unknown_field", () => {
  for (const extra of [{ id: "1" }, { provider_agent_id: "agent_123" }]) {
    expectBadRequest({ ...AGENT, ...extra }, "unknown_field");
  }
});

test("parseCreateAgentRequest rejects missing or blank names with invalid_name", () => {
  expectBadRequest(
    { provider: "cartesia", providerAgentId: "agent_123" },
    "invalid_name",
  );
  for (const name of [null, 123, [], "", "   "]) {
    expectBadRequest({ ...AGENT, name }, "invalid_name");
  }
});

test("parseCreateAgentRequest rejects missing or unknown providers with unsupported_provider", () => {
  expectBadRequest(
    { name: "Support", providerAgentId: "agent_123" },
    "unsupported_provider",
  );
  for (const provider of [null, 1, "", "Cartesia", "elevenlabs"]) {
    expectBadRequest({ ...AGENT, provider }, "unsupported_provider");
  }
});

test("parseCreateAgentRequest rejects missing or malformed provider agent IDs with invalid_provider_agent_id", () => {
  expectBadRequest(
    { name: "Support", provider: "cartesia" },
    "invalid_provider_agent_id",
  );
  for (const providerAgentId of [null, 123, "", " ", " agent_123", "a b"]) {
    expectBadRequest(
      { ...AGENT, providerAgentId },
      "invalid_provider_agent_id",
    );
  }
});
