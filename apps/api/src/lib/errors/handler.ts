import type { ErrorRequestHandler } from "express";
import {
  BadRequestError,
  HttpError,
  PayloadTooLargeError,
  UnsupportedMediaTypeError,
} from "./index.ts";

/**
 * requestError translates Express body-parser failures into our public errors.
 *
 * @param error - The value passed to the error middleware.
 * @returns A safe HTTP error, or undefined for an unexpected failure.
 */
function requestError(error: unknown): HttpError | undefined {
  if (error instanceof HttpError) return error;
  if (!(error instanceof Error) || !("type" in error)) return;

  // Translate known parser failures without exposing their request bodies
  switch (error.type) {
    case "entity.parse.failed":
    case "request.size.invalid":
    case "request.aborted":
      return new BadRequestError({
        code: "invalid_json",
        message: "Provide a complete JSON object as the request body.",
      });
    case "entity.too.large":
      return new PayloadTooLargeError({
        code: "payload_too_large",
        message: "Keep the request body within the size limit.",
      });
    case "encoding.unsupported":
    case "charset.unsupported":
      return new UnsupportedMediaTypeError({
        code: "unsupported_media_type",
        message: "Send uncompressed JSON encoded as UTF-8.",
      });
  }
}

/**
 * errorHandler sends every failure using the same JSON envelope.
 *
 * Register it after the routes. Express recognizes error middleware by its
 * four arguments, including next. Only our explicit HTTP errors expose their
 * messages; unexpected failures receive a generic HTTP 500 response.
 *
 * @param error - The failure thrown by a route or by body parsing.
 * @param res - Receives the JSON error envelope.
 * @param next - Passes the failure to Express if a response has already started.
 */
export const errorHandler: ErrorRequestHandler = (
  error: unknown,
  _req,
  res,
  next,
) => {
  // Delegate failures after a response has already started
  if (res.headersSent) {
    next(error);
    return;
  }

  const known = requestError(error);
  const status = known?.status ?? 500;
  const code = known?.code ?? "internal_error";

  // Log code locations without bodies, credentials, or provider messages
  if (status >= 500) {
    console.error("Request failed", {
      status,
      code,
      frames:
        error instanceof Error
          ? error.stack
              ?.split("\n")
              .filter((line) => line.trimStart().startsWith("at "))
          : undefined,
    });
  }

  // Return only the public error fields
  res.status(status).json({
    error: {
      code,
      message: known?.message ?? "An unexpected error occurred.",
    },
  });
};
