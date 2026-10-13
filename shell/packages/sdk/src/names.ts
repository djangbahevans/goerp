import type { FilterParamValue } from "./http/filter-params.js";

// biome-ignore lint/suspicious/noEmptyInterface: generated clients extend this registry
export interface ModuleApis {}
// biome-ignore lint/suspicious/noEmptyInterface: generated clients extend this registry
export interface Resources {}
// biome-ignore lint/suspicious/noEmptyInterface: generated clients extend this registry
export interface Actions {}
// biome-ignore lint/suspicious/noEmptyInterface: generated clients extend this registry
export interface Permissions {}
// biome-ignore lint/suspicious/noEmptyInterface: generated clients extend this registry
export interface ErrorCodes {}

declare const resourceBrand: unique symbol;
declare const actionBrand: unique symbol;
declare const permissionBrand: unique symbol;

export type ResourceRef<T extends Record<string, unknown> = Record<string, unknown>> = string & {
  readonly [resourceBrand]: T;
};
export type ActionRef<TResult = unknown, TVariables = unknown> = string & {
  readonly [actionBrand]: { result: TResult; variables: TVariables };
};
export type PermissionRef = string & { readonly [permissionBrand]: true };

// ModuleApis distinguishes runtime-only compilations from generated modules
// whose individual name registries may be empty.
type Registered<T, Ref = never> = keyof ModuleApis extends never ? string : (keyof T & string) | Ref;
export type ResourceName = Registered<Resources, ResourceRef>;
export type ActionName = Registered<Actions, ActionRef>;
export type PermissionName = Registered<Permissions, PermissionRef>;
export type ErrorCodeName = Registered<ErrorCodes>;

type ResourceEntry<R, K extends "record" | "create" | "update"> = R extends keyof Resources
  ? Resources[R] extends Record<K, infer T>
    ? T
    : never
  : Record<string, unknown>;

export type RecordOf<R extends ResourceName> = R extends ResourceRef<infer T> ? T : ResourceEntry<R, "record">;
export type CreateOf<R extends ResourceName> = R extends ResourceRef<infer T> ? Partial<T> : ResourceEntry<R, "create">;
export type UpdateOf<R extends ResourceName> = R extends ResourceRef<infer T> ? Partial<T> : ResourceEntry<R, "update">;
export type FieldOf<R extends ResourceName> = keyof RecordOf<R> & string;
export type FilterOf<R extends ResourceName> = Partial<Record<FieldOf<R>, FilterParamValue>>;
export type SortOf<R extends ResourceName> = FieldOf<R> | `-${FieldOf<R>}`;

type ActionEntry<A, K extends "variables" | "result"> = A extends keyof Actions
  ? Actions[A] extends Record<K, infer T>
    ? T
    : never
  : unknown;
export type VariablesOf<A extends ActionName> = A extends ActionRef<unknown, infer V> ? V : ActionEntry<A, "variables">;
export type ResultOf<A extends ActionName> = A extends ActionRef<infer T, unknown> ? T : ActionEntry<A, "result">;
// biome-ignore lint/suspicious/noConfusingVoidType: NoBody actions use void variables and accept an empty argument tuple
export type VariablesArgs<A extends ActionName> = [VariablesOf<A>] extends [void] ? [] : [variables: VariablesOf<A>];

export function resourceRef<T extends Record<string, unknown> = Record<string, unknown>>(name: string): ResourceRef<T> {
  return name as ResourceRef<T>;
}

export function actionRef<TResult = unknown, TVariables = unknown>(name: string): ActionRef<TResult, TVariables> {
  return name as ActionRef<TResult, TVariables>;
}

export function permissionRef(name: string): PermissionRef {
  return name as PermissionRef;
}
