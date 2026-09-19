import { AuthContext, PermissionContext } from "@goerp/sdk/auth";
import { evaluateCondition, evaluateValue, type UserBindings } from "@goerp/sdk/domain";
import { useContext, useMemo } from "react";

type RecordState = Readonly<Record<string, unknown>>;

const EMPTY_RECORD: RecordState = Object.freeze({});
const NO_PERMISSIONS: ReadonlySet<string> = new Set();

export type ComputedValue = { ok: true; value: number | string } | { ok: false; message: string };

export interface ConditionEvaluator {
  // No condition shows the element; a malformed or unevaluable one hides it.
  isVisible(condition: string | undefined, location: string, record?: RecordState): boolean;
  // No condition leaves the element editable; a malformed or unevaluable one locks it.
  isReadonly(condition: string | undefined, location: string, record?: RecordState): boolean;
  // A malformed or unevaluable expression is a failure, never a blank or a guessed value; only a malformed one is reported to the console.
  computeValue(expression: string, location: string, record?: RecordState): ComputedValue;
}

const reported = new Set<string>();

export function resetReportedConditionErrors(): void {
  reported.clear();
}

// Conditions re-evaluate on every keystroke; each broken expression is reported once.
function reportOnce(scope: string, location: string, source: string, message: string): void {
  const key = JSON.stringify([scope, location, source]);
  if (reported.has(key)) return;
  reported.add(key);
  console.error(`${scope} › ${location}: ${message} (expression: ${source})`);
}

function useUserBindings(): UserBindings {
  const auth = useContext(AuthContext);
  const permissions = useContext(PermissionContext)?.permissions ?? NO_PERMISSIONS;
  const user = auth?.user;
  const tenant = auth?.tenant;
  return useMemo(
    () => ({
      id: user?.id ?? "",
      contactId: user?.contactId ?? null,
      tenantId: tenant?.id ?? "",
      roles: user?.roles ?? [],
      permissions,
    }),
    [user, tenant, permissions],
  );
}

// Shared by every manifest `condition`/`readonly_condition` call site. `scope`
// names the view or resource the manifest declares the condition on.
export function useConditionEvaluator(scope: string): ConditionEvaluator {
  const user = useUserBindings();
  return useMemo(() => {
    function evaluate(
      condition: string | undefined,
      location: string,
      record: RecordState,
      failedValue: boolean,
    ): boolean | undefined {
      if (condition === undefined) return undefined;
      const result = evaluateCondition(condition, { record, user });
      if (result.ok) return result.value;
      reportOnce(scope, location, condition, result.error.message);
      return failedValue;
    }
    return {
      isVisible: (condition, location, record = EMPTY_RECORD) => evaluate(condition, location, record, false) ?? true,
      isReadonly: (condition, location, record = EMPTY_RECORD) => evaluate(condition, location, record, true) ?? false,
      computeValue(expression, location, record = EMPTY_RECORD) {
        const result = evaluateValue(expression, { record });
        if (result.ok) return result;
        // An evaluation failure (an empty operand, a division by zero) is data state the field itself shows.
        if (result.error.phase === "parse") reportOnce(scope, location, expression, result.error.message);
        return { ok: false, message: result.error.message };
      },
    };
  }, [scope, user]);
}
