import { AuthProvider, PermissionProvider } from "@goerp/sdk/auth";
import { ViewRegistryProvider } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { router } from "./router";

const queryClient = new QueryClient();

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <PermissionProvider>
          {/* onUpdate re-runs every active route's beforeLoad/loader after a
              rebuild (login, logout, schema.updated, module.installed) so a
              route already on screen picks up the fresh registry without a
              manual refresh — shell-architecture.md §9 "Registry updates on
              hot reload" step 5. */}
          <ViewRegistryProvider onUpdate={() => router.invalidate()}>
            <RouterProvider router={router} />
          </ViewRegistryProvider>
        </PermissionProvider>
      </AuthProvider>
    </QueryClientProvider>
  );
}
