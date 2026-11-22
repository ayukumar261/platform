import {
  BadGatewayError,
  ServiceUnavailableError,
} from "../lib/errors/index.ts";
import type { Bridge } from "./bridge.types.ts";

export class BridgeClient implements Bridge {
  private readonly url: URL;
  private readonly token: string;
  private readonly timeoutMs: number;

  constructor(url: string, token: string, timeoutMs = 5000) {
    this.url = new URL("/call", url);
    this.token = token;
    this.timeoutMs = timeoutMs;
  }

  /**
   * createCall asks the bridge to start one outbound call through Twilio.
   *
   * The bridge owns the conversation after acceptance. A timeout can happen
   * after it has accepted the request, so this client never retries a POST.
   * Each new request can place another call.
   *
   * @param to - The destination number, already checked by the request schema.
   * @returns The bridge's ID for the accepted call.
   * @throws ServiceUnavailableError when the bridge is unreachable or stopping.
   * @throws BadGatewayError when the bridge rejects or misformats its reply.
   */
  async createCall(to: string): Promise<{ callId: string }> {
    let response: Response;
    try {
      // Send one request with a deadline and a private credential
      response = await fetch(this.url, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ to }),
        redirect: "error",
        signal: AbortSignal.timeout(this.timeoutMs),
      });
    } catch {
      throw new ServiceUnavailableError({
        code: "bridge_unavailable",
        message:
          "Could not confirm call acceptance. The call may already have started.",
      });
    }

    // Reject unsuccessful replies without exposing their contents
    if (response.status !== 202) {
      await response.body?.cancel();
      if (response.status === 503) {
        throw new ServiceUnavailableError({
          code: "bridge_unavailable",
          message: "The bridge is not accepting calls.",
        });
      }
      throw new BadGatewayError({
        code: "invalid_bridge_response",
        message: "The bridge could not accept the call request.",
      });
    }

    // Check the reply before forwarding it to the caller
    let body: unknown;
    try {
      body = await response.json();
    } catch {
      throw new BadGatewayError({
        code: "invalid_bridge_response",
        message:
          "Could not read call acceptance. The call may already have started.",
      });
    }
    if (
      typeof body !== "object" ||
      body === null ||
      !("callId" in body) ||
      typeof body.callId !== "string" ||
      !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
        body.callId,
      )
    ) {
      throw new BadGatewayError({
        code: "invalid_bridge_response",
        message:
          "Could not read call acceptance. The call may already have started.",
      });
    }
    return { callId: body.callId };
  }
}
