import { AuthProvider, PermissionProvider } from "@goerp/sdk/auth";
import { translationLoader } from "@goerp/sdk/i18n";
import { configureUIExecutor } from "@goerp/sdk/realtime";
import { ViewRegistryProvider } from "@goerp/sdk/schema";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "./query-client.js";
import { router } from "./router";
import { AuthRouterProvider } from "./router/auth-router-provider.js";

const queryClient = createQueryClient();
configureUIExecutor({ queryClient, navigate: (href) => router.navigate({ href }) });

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
