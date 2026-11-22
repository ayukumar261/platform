export interface ErrorDetails {
  code: string;
  message: string;
}

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

export class BadRequestError extends HttpError {
  constructor(details: ErrorDetails) {
    super(400, details);
  }
}

export class UnauthorizedError extends HttpError {
  constructor(details: ErrorDetails) {
    super(401, details);
  }
}

export class ForbiddenError extends HttpError {
  constructor(details: ErrorDetails) {
    super(403, details);
  }
}

export class NotFoundError extends HttpError {
  constructor(details: ErrorDetails) {
    super(404, details);
  }
}

export class ConflictError extends HttpError {
  constructor(details: ErrorDetails) {
    super(409, details);
  }
}

export class PayloadTooLargeError extends HttpError {
  constructor(details: ErrorDetails) {
    super(413, details);
  }
}

export class UnsupportedMediaTypeError extends HttpError {
  constructor(details: ErrorDetails) {
    super(415, details);
  }
}

export class BadGatewayError extends HttpError {
  constructor(details: ErrorDetails) {
    super(502, details);
  }
}

export class ServiceUnavailableError extends HttpError {
  constructor(details: ErrorDetails) {
    super(503, details);
  }
}
