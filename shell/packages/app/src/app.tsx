import { AuthProvider, PermissionProvider } from "@goerp/sdk/auth";
import { translationLoader } from "@goerp/sdk/i18n";
import { ViewRegistryProvider } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { router } from "./router";
import { AuthRouterProvider } from "./router/auth-router-provider.js";

const queryClient = new QueryClient();

// A schema.updated after a hot reload may carry changed translation files
// even when the bundle is unchanged (l10n-guide.md §7 "Translation loading").
function onRegistryUpdate() {
  void translationLoader.refreshLoaded();
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <PermissionProvider>
          <ViewRegistryProvider onUpdate={onRegistryUpdate}>
            <AuthRouterProvider router={router} />
          </ViewRegistryProvider>
        </PermissionProvider>
      </AuthProvider>
    </QueryClientProvider>
  );
}
