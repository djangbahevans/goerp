import { useContext } from "react";

import { AuthContext } from "./auth-provider.js";
import type { AuthContextValue } from "./types.js";

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext);
  if (!value) {
    throw new Error("useAuth must be called within an AuthProvider");
  }
  return value;
}
