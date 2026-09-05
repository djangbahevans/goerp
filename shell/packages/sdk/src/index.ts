export const SDK_VERSION = "0.0.0";

export type {
  APIClient,
  APIClientConfig,
  RefreshedTokens,
  RefreshOutcome,
  RequestOptions,
  SessionRefresher,
} from "./http/index.js";
export { apiClient, FetchAPIClient } from "./http/index.js";
