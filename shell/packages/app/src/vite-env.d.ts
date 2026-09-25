/// <reference types="vite/client" />

interface ImportMetaEnv {
  // See .env.example for what this is and why it's build-time.
  readonly VITE_MAP_TILE_URL?: string;
  readonly VITE_SUPPORT_URL?: string;
  readonly VITE_DOCS_URL?: string;
  readonly VITE_API_REFERENCE_URL?: string;
  readonly VITE_STATUS_PAGE_URL?: string;
  readonly VITE_CHANGELOG_URL?: string;
}
