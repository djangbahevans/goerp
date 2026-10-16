export const SDK_VERSION = "0.0.0";

export type { ListParams } from "./api/index.js";
export { callAction, resourceApi } from "./api/index.js";

export type {
  APIClient,
  APIClientConfig,
  APIExchange,
  APIResponse,
  DeleteOptions,
  FetchParquetPagesOptions,
  FilterIsNull,
  FilterLike,
  FilterParamValue,
  FilterRange,
  PagedResponse,
  PagedResponseWire,
  RefreshedTokens,
  RefreshOutcome,
  RequestOptions,
  ResponseObserver,
  SessionRefresher,
} from "./http/index.js";
export {
  apiClient,
  downloadBlob,
  FetchAPIClient,
  fetchAllParquetPages,
  flattenFilterParams,
  isFilterIsNull,
  isFilterLike,
  isFilterRange,
  toPagedResponse,
} from "./http/index.js";
export { defineModule } from "./module/define-module.js";
export type { ModuleDefinition } from "./module/module-types.js";

export type {
  ActionName,
  ActionRef,
  Actions,
  CreateOf,
  ErrorCodeName,
  ErrorCodes,
  FieldOf,
  FilterOf,
  ModuleApis,
  PermissionName,
  PermissionRef,
  Permissions,
  RecordOf,
  ResourceName,
  ResourceRef,
  Resources,
  ResultOf,
  SortOf,
  UpdateOf,
  VariablesArgs,
  VariablesOf,
} from "./names.js";
export { actionRef, permissionRef, resourceRef } from "./names.js";
