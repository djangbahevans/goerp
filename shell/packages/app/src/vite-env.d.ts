/// <reference types="vite/client" />

interface ImportMetaEnv {
  // See .env.example for what this is and why it's build-time.
  readonly VITE_MAP_TILE_URL?: string;
  readonly VITE_SUPPORT_URL?: string;
}
