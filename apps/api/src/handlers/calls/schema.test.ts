import assert from "node:assert/strict";
import { test } from "node:test";
import { BadRequestError } from "../../lib/errors/index.ts";
import { parseCreateCallRequest } from "./schema.ts";

/**
 * expectBadRequest checks that parsing body fails with a BadRequestError.
 *
 * @param body - The invalid request body.
 * @param code - The error code the body should produce.
 */
function expectBadRequest(body: unknown, code: string): void {
  assert.throws(
    () => parseCreateCallRequest(body),
    (error: unknown) => {
      assert.ok(error instanceof BadRequestError);
      assert.equal(error.code, code);
      return true;
    },
    `Body: ${JSON.stringify(body)}`,
  );
}

test("parseCreateCallRequest accepts correctly formatted international numbers", () => {
  // Check formatting independently of the endpoint's destination allowlist
  for (const to of [
    "+19452412002",
    "+15551234567",
    "+919452412002",
    "+123456789012345",
  ]) {
    assert.deepEqual(parseCreateCallRequest({ to }), { to });
  }
});

test("parseCreateCallRequest rejects non-object bodies with invalid_request", () => {
  for (const body of [undefined, null, [], true, 123, "+15551234567"]) {
    expectBadRequest(body, "invalid_request");
  }
});

test("parseCreateCallRequest rejects missing or invalid destinations with invalid_phone_number", () => {
  expectBadRequest({}, "invalid_phone_number");
  for (const to of [
    undefined,
    null,
    15551234567,
    true,
    [],
    {},
    "",
    "5551234567",
    "9452412002",
    "+1",
    "+0123456789",
    "+1234567890123456",
    " +15551234567",
    "+15551234567\n",
    "sip:someone@example.com",
    "+19452412002;ext=123",
  ]) {
    expectBadRequest({ to }, "invalid_phone_number");
  }
});

test("parseCreateCallRequest rejects extra fields with unknown_field", () => {
  for (const extra of [{ carrier: "telnyx" }, { from: "+15551230000" }]) {
    expectBadRequest({ to: "+19452412002", ...extra }, "unknown_field");
  }
});
