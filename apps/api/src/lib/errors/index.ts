/** Describe a failure using a stable code and a message safe for the caller. */
export interface ErrorDetails {
  code: string;
  message: string;
}

/**
 * HttpError carries the information shared by our named HTTP errors.
 *
 * Use a subclass in endpoint code. Its status describes the HTTP category,
 * while its code identifies the particular problem. Keep credentials and
 * provider responses in neither the code nor the message.
 */
export abstract class HttpError extends Error {
  readonly status: number;
  readonly code: string;

  protected constructor(status: number, details: ErrorDetails) {
    super(details.message);
    this.name = new.target.name;
    this.status = status;
    this.code = details.code;
  }
}

/** BadRequestError reports missing or invalid request data with HTTP 400. */
export class BadRequestError extends HttpError {
  constructor(details: ErrorDetails) {
    super(400, details);
  }
}

/** UnauthorizedError reports missing or invalid authentication with HTTP 401. */
export class UnauthorizedError extends HttpError {
  constructor(details: ErrorDetails) {
    super(401, details);
  }
}

/** ForbiddenError reports insufficient permission with HTTP 403. */
export class ForbiddenError extends HttpError {
  constructor(details: ErrorDetails) {
    super(403, details);
  }
}

/** NotFoundError reports a resource that does not exist with HTTP 404. */
export class NotFoundError extends HttpError {
  constructor(details: ErrorDetails) {
    super(404, details);
  }
}

/** ConflictError reports a conflict with the current state with HTTP 409. */
export class ConflictError extends HttpError {
  constructor(details: ErrorDetails) {
    super(409, details);
  }
}

/** PayloadTooLargeError reports a body over the size limit with HTTP 413. */
export class PayloadTooLargeError extends HttpError {
  constructor(details: ErrorDetails) {
    super(413, details);
  }
}

/** UnsupportedMediaTypeError reports an unsupported body format with HTTP 415. */
export class UnsupportedMediaTypeError extends HttpError {
  constructor(details: ErrorDetails) {
    super(415, details);
  }
}

/** BadGatewayError reports an invalid reply from another service with HTTP 502. */
export class BadGatewayError extends HttpError {
  constructor(details: ErrorDetails) {
    super(502, details);
  }
}

/** ServiceUnavailableError reports a temporarily unavailable service with HTTP 503. */
export class ServiceUnavailableError extends HttpError {
  constructor(details: ErrorDetails) {
    super(503, details);
  }
}
