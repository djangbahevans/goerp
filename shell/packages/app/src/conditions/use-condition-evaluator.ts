import { AuthContext, PermissionContext } from "@goerp/sdk/auth";
import { evaluateCondition, type UserBindings } from "@goerp/sdk/domain";
import { useContext, useMemo } from "react";

type RecordState = Readonly<Record<string, unknown>>;

const EMPTY_RECORD: RecordState = Object.freeze({});
const NO_PERMISSIONS: ReadonlySet<string> = new Set();

export interface ConditionEvaluator {
  // No condition shows the element; a malformed or unevaluable one hides it.
  isVisible(condition: string | undefined, location: string, record?: RecordState): boolean;
  // No condition leaves the element editable; a malformed or unevaluable one locks it.
  isReadonly(condition: string | undefined, location: string, record?: RecordState): boolean;
}

const reported = new Set<string>();

export function resetReportedConditionErrors(): void {
  reported.clear();
}

// Conditions re-evaluate on every keystroke; each broken expression is reported once.
function reportOnce(scope: string, location: string, condition: string, message: string): void {
  const key = JSON.stringify([scope, location, condition]);
  if (reported.has(key)) return;
  reported.add(key);
  console.error(`${scope} › ${location}: ${message} (condition: ${condition})`);
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
    };
  }, [scope, user]);
}
