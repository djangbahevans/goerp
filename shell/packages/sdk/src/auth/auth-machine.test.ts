import { describe, expect, it } from "vitest";
import { authTransition, AuthMachine } from "./auth-machine.js";
import type { AuthState, CurrentTenant, CurrentUser } from "./types.js";

const user: CurrentUser = { id: "u1", email: "a@example.com", roles: ["admin"], amr: ["pwd"], mfaVerifiedAt: null };
const tenant: CurrentTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

describe("authTransition", () => {
  it("idle → checking on check_session", () => {
    const next = authTransition({ status: "idle" }, { type: "check_session" });
    expect(next).toEqual({ status: "checking" });
  });

  it("checking → authenticated on session_checked", () => {
    const next = authTransition({ status: "checking" }, { type: "session_checked", user, tenant });
    expect(next).toEqual({ status: "authenticated", user, tenant });
  });

  it("checking → unauthenticated on session_check_failed", () => {
    const next = authTransition({ status: "checking" }, { type: "session_check_failed" });
    expect(next).toEqual({ status: "unauthenticated" });
  });

  it("unauthenticated → checking on login_started", () => {
    const next = authTransition({ status: "unauthenticated" }, { type: "login_started" });
    expect(next).toEqual({ status: "checking" });
  });

  it("checking → authenticated on login_succeeded", () => {
    const next = authTransition({ status: "checking" }, { type: "login_succeeded", user, tenant });
    expect(next).toEqual({ status: "authenticated", user, tenant });
  });

  it("checking → mfa_required on login_requires_mfa", () => {
    const next = authTransition(
      { status: "checking" },
      { type: "login_requires_mfa", challengeToken: "tok", methods: ["totp"] },
    );
    expect(next).toEqual({ status: "mfa_required", challengeToken: "tok", methods: ["totp"] });
  });

  it("checking → unauthenticated on login_failed", () => {
    const next = authTransition({ status: "checking" }, { type: "login_failed" });
    expect(next).toEqual({ status: "unauthenticated" });
  });

  it("mfa_required → authenticated on mfa_verified", () => {
    const start: AuthState = { status: "mfa_required", challengeToken: "tok", methods: ["totp"] };
    const next = authTransition(start, { type: "mfa_verified", user, tenant });
    expect(next).toEqual({ status: "authenticated", user, tenant });
  });

  it("mfa_required → unauthenticated on mfa_failed (challenge cannot be retried)", () => {
    const start: AuthState = { status: "mfa_required", challengeToken: "tok", methods: ["totp"] };
    const next = authTransition(start, { type: "mfa_failed" });
    expect(next).toEqual({ status: "unauthenticated" });
  });

  it("authenticated → refreshing → authenticated preserves user/tenant", () => {
    const start: AuthState = { status: "authenticated", user, tenant };
    const refreshing = authTransition(start, { type: "refresh_started" });
    expect(refreshing).toEqual({ status: "refreshing", user, tenant });
    const authed = authTransition(refreshing, { type: "refresh_succeeded" });
    expect(authed).toEqual({ status: "authenticated", user, tenant });
  });

  it("refreshing → unauthenticated on refresh_failed", () => {
    const start: AuthState = { status: "refreshing", user, tenant };
    const next = authTransition(start, { type: "refresh_failed" });
    expect(next).toEqual({ status: "unauthenticated" });
  });

  it("authenticated → unauthenticated on session_expired", () => {
    const start: AuthState = { status: "authenticated", user, tenant };
    const next = authTransition(start, { type: "session_expired" });
    expect(next).toEqual({ status: "unauthenticated" });
  });

  it("refreshing → unauthenticated on session_expired", () => {
    const start: AuthState = { status: "refreshing", user, tenant };
    const next = authTransition(start, { type: "session_expired" });
    expect(next).toEqual({ status: "unauthenticated" });
  });

  it("authenticated → logging_out → unauthenticated", () => {
    const start: AuthState = { status: "authenticated", user, tenant };
    const loggingOut = authTransition(start, { type: "logout_started" });
    expect(loggingOut).toEqual({ status: "logging_out" });
    const next = authTransition(loggingOut, { type: "logout_complete" });
    expect(next).toEqual({ status: "unauthenticated" });
  });

  it("ignores events that don't apply to the current status", () => {
    const start: AuthState = { status: "idle" };
    expect(authTransition(start, { type: "session_expired" })).toBe(start);
    expect(authTransition(start, { type: "logout_complete" })).toBe(start);
    expect(authTransition({ status: "unauthenticated" }, { type: "mfa_failed" })).toEqual({
      status: "unauthenticated",
    });
  });
});

describe("AuthMachine", () => {
  it("starts idle and notifies subscribers only on a real transition", () => {
    const machine = new AuthMachine();
    expect(machine.getState()).toEqual({ status: "idle" });

    let notifications = 0;
    const unsubscribe = machine.subscribe(() => {
      notifications += 1;
    });

    machine.transition({ type: "check_session" });
    expect(machine.getState()).toEqual({ status: "checking" });
    expect(notifications).toBe(1);

    // login_started doesn't apply from "checking" — no-op, no notification.
    machine.transition({ type: "login_started" });
    expect(notifications).toBe(1);

    unsubscribe();
    machine.transition({ type: "session_check_failed" });
    expect(notifications).toBe(1);
    expect(machine.getState()).toEqual({ status: "unauthenticated" });
  });
});
