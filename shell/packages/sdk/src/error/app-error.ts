export interface AppErrorInit {
  code: string;
  message: string;
  details?: Record<string, unknown> | null;
  httpStatus: number;
  requestId?: string | null;
  traceId?: string | null;
  fieldErrors?: Record<string, string[]> | null;
}

export class AppError extends Error {
  readonly code: string;
  readonly details: Record<string, unknown> | null;
  readonly httpStatus: number;
  readonly requestId: string | null;
  readonly traceId: string | null;
  readonly fieldErrors: Record<string, string[]> | null;

  constructor(init: AppErrorInit) {
    super(init.message);
    this.name = "AppError";
    this.code = init.code;
    this.details = init.details ?? null;
    this.httpStatus = init.httpStatus;
    this.requestId = init.requestId ?? null;
    this.traceId = init.traceId ?? null;
    // Only a 422 response carries per-field validation errors — any other
    // status ignores a caller-supplied fieldErrors rather than trusting it.
    this.fieldErrors = init.httpStatus === 422 ? (init.fieldErrors ?? null) : null;
  }

  isNotFound(): boolean {
    return this.httpStatus === 404;
  }

  isConflict(): boolean {
    return this.httpStatus === 409;
  }

  isForbidden(): boolean {
    return this.httpStatus === 403;
  }

  isUnauth(): boolean {
    return this.httpStatus === 401;
  }

  isValidation(): boolean {
    return this.httpStatus === 422;
  }

  isRateLimited(): boolean {
    return this.httpStatus === 429;
  }

  isServerError(): boolean {
    return this.httpStatus >= 500;
  }
}

export function isAppError(err: unknown): err is AppError {
  return err instanceof AppError;
}
