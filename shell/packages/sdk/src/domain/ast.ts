// Boolean subset of the domain expression language the shell interprets
// (manifest-spec.md §8). LIKE/ILIKE and child_of/parent_of have no node here.

export type UserAttribute = "id" | "contact_id" | "tenant_id";

export type Literal = null | boolean | string | number;

export type ComparisonOp = "=" | "!=" | "<" | ">" | "<=" | ">=";

export type Expr =
  | { kind: "record"; field: string }
  | { kind: "user"; attr: UserAttribute }
  | { kind: "role"; role: string }
  | { kind: "permission"; permission: string }
  | { kind: "literal"; value: Literal }
  | { kind: "compare"; op: ComparisonOp; left: Expr; right: Expr }
  | { kind: "and"; left: Expr; right: Expr }
  | { kind: "or"; left: Expr; right: Expr }
  | { kind: "not"; operand: Expr }
  | { kind: "isNull"; operand: Expr; negated: boolean }
  | { kind: "in"; operand: Expr; values: Expr[] };

// The `computed_display` value subset.
export type ValueFunction = "ROUND" | "PERCENT";

export type ValueExpr =
  | { kind: "number"; value: number }
  | { kind: "record"; field: string }
  | { kind: "negate"; operand: ValueExpr }
  | { kind: "arithmetic"; op: "+" | "-" | "*" | "/"; left: ValueExpr; right: ValueExpr }
  | { kind: "call"; fn: ValueFunction; args: ValueExpr[] };
