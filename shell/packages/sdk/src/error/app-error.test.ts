import { describe, expect, it } from "vitest";
import { AppError, isAppError } from "./app-error.js";

function makeError(overrides: Partial<ConstructorParameters<typeof AppError>[0]> = {}): AppError {
  return new AppError({
    code: "contacts.contact.not_found",
    message: "Contact not found",
    httpStatus: 404,
    ...overrides,
  });
}

describe("AppError", () => {
  it("exposes the documented fields", () => {
    const err = makeError({
      details: { id: "01j9zx" },
      requestId: "req_1",
      traceId: "trace_1",
    });

    expect(err.code).toBe("contacts.contact.not_found");
    expect(err.message).toBe("Contact not found");
    expect(err.details).toEqual({ id: "01j9zx" });
    expect(err.httpStatus).toBe(404);
    expect(err.requestId).toBe("req_1");
    expect(err.traceId).toBe("trace_1");
    expect(err instanceof Error).toBe(true);
  });

  it("defaults details, requestId, and traceId to null when omitted", () => {
    const err = makeError();

    expect(err.details).toBeNull();
    expect(err.requestId).toBeNull();
    expect(err.traceId).toBeNull();
  });

  it.each([
    ["isNotFound", 404, "isNotFound"],
    ["isConflict", 409, "isConflict"],
    ["isForbidden", 403, "isForbidden"],
    ["isUnauth", 401, "isUnauth"],
    ["isValidation", 422, "isValidation"],
    ["isRateLimited", 429, "isRateLimited"],
  ] as const)("%s is true only for httpStatus %i", (_label, status, method) => {
    const matching = makeError({ httpStatus: status });
    const nonMatching = makeError({ httpStatus: status === 404 ? 409 : 404 });

    expect(matching[method]()).toBe(true);
    expect(nonMatching[method]()).toBe(false);
  });

  it("isServerError is true for any httpStatus >= 500", () => {
    expect(makeError({ httpStatus: 500 }).isServerError()).toBe(true);
    expect(makeError({ httpStatus: 503 }).isServerError()).toBe(true);
    expect(makeError({ httpStatus: 499 }).isServerError()).toBe(false);
  });

  it("populates fieldErrors only when httpStatus is 422", () => {
    const validationError = makeError({
      httpStatus: 422,
      fieldErrors: { email: ["Invalid email format"], name: ["Required"] },
    });
    expect(validationError.fieldErrors).toEqual({
      email: ["Invalid email format"],
      name: ["Required"],
    });

    const notFoundError = makeError({
      httpStatus: 404,
      fieldErrors: { email: ["Invalid email format"] },
    });
    expect(notFoundError.fieldErrors).toBeNull();
  });

  it("defaults fieldErrors to null on a 422 with none supplied", () => {
    expect(makeError({ httpStatus: 422 }).fieldErrors).toBeNull();
  });
});

describe("isAppError", () => {
  it("narrows an AppError instance", () => {
    const err: unknown = makeError();
    expect(isAppError(err)).toBe(true);
  });

  it("rejects a plain Error and non-error values", () => {
    expect(isAppError(new Error("boom"))).toBe(false);
    expect(isAppError("boom")).toBe(false);
    expect(isAppError(null)).toBe(false);
    expect(isAppError(undefined)).toBe(false);
  });
});
