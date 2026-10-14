import type { ModuleApis, PermissionName } from "@goerp/sdk";
import type { CurrentUser } from "@goerp/sdk/auth";
import { ModuleNavigationProvider } from "@goerp/sdk/react";
import { buildViewRegistry, filterViewByCapability, resolveViewDeclaration, resolveViewPath } from "@goerp/sdk/schema";
import { ShellProvider } from "@goerp/sdk/testing";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useRouterState,
} from "@tanstack/react-router";
import { act, type RenderResult, render, waitFor } from "@testing-library/react";
import { useLayoutEffect, useMemo } from "react";
import { ViewDispatch } from "../renderers/view-dispatch.js";
import { viewTestConfiguration } from "./fixture.js";
import { createViewRequestHandler } from "./mock-routes.js";

export interface RenderViewOptions<M extends keyof ModuleApis> {
  api?: Partial<ModuleApis[M]>;
  apiMocks?: Record<string, unknown>;
  user?: Partial<CurrentUser>;
  permissions?: PermissionName[];
  recordId?: string;
  search?: string;
}

function QueryProbe({ ready }: { ready: (client: QueryClient) => void }) {
  const client = useQueryClient();
  useLayoutEffect(() => ready(client), [client, ready]);
  return null;
}

export async function renderView<M extends keyof ModuleApis>(
  viewName: `${M & string}.${string}`,
  options: RenderViewOptions<NoInfer<M>> = {},
): Promise<RenderResult> {
  const qualifiedName = String(viewName);
  const dot = qualifiedName.indexOf(".");
  const module = qualifiedName.slice(0, dot);
  const { fixture, schema } = viewTestConfiguration(module);
  const view = resolveViewDeclaration(schema, viewName, module);
  if (!view) throw new Error(`Unknown declared view "${viewName}"`);
  const initialView = view;
  const registry = buildViewRegistry(schema);
  filterViewByCapability(view, registry.resources.get(view.resource));
  const path =
    options.recordId !== undefined
      ? schema.modules[module]?.routes
          .find((route) => route.view === view.name && route.method === "GET" && route.path.includes("{id}"))
          ?.path.replace("{id}", encodeURIComponent(options.recordId))
      : resolveViewPath(schema, viewName, module);
  const modulePath = path ?? `/${module}/${view.name}`;
  const search = options.search ? `?${options.search.replace(/^\?/, "")}` : "";
  const root = createRootRoute({ component: Outlet });
  function RenderedView() {
    const location = useRouterState({ select: (state) => state.location });
    const params = useMemo(() => new URLSearchParams(location.searchStr), [location.searchStr]);
    const resolved = registry.resolveRoute(location.pathname.replace(/^\/_m/, ""));
    const recordId = resolved ? resolved.recordId : options.recordId;
    return (
      <ViewDispatch
        view={resolved?.declaration ?? initialView}
        module={resolved?.module ?? module}
        {...(recordId !== undefined ? { recordId } : {})}
        params={params}
      />
    );
  }
  const route = createRoute({
    getParentRoute: () => root,
    path: "/$",
    component: RenderedView,
  });
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [`/_m${modulePath}${search}`] }),
  });
  await router.load();
  let queryClient: QueryClient | undefined;
  const ready = (client: QueryClient) => {
    queryClient = client;
  };
  const result = render(
    <ShellProvider
      module={module}
      schema={schema}
      requestHandler={createViewRequestHandler(fixture, options.api)}
      {...(options.apiMocks ? { apiMocks: options.apiMocks } : {})}
      {...(options.user ? { user: options.user } : {})}
      {...(options.permissions ? { permissions: options.permissions } : {})}
    >
      <ModuleNavigationProvider
        navigate={(to, navigationOptions) => {
          void router.navigate({
            to,
            ...(navigationOptions?.replace !== undefined ? { replace: navigationOptions.replace } : {}),
          });
        }}
      >
        <QueryProbe ready={ready} />
        <RouterProvider router={router} />
      </ModuleNavigationProvider>
    </ShellProvider>,
  );
  try {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    await waitFor(() => {
      if (queryClient?.isFetching() !== 0 || result.container.querySelector("[data-skeleton]"))
        throw new Error(`Waiting for "${viewName}" data`);
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    return result;
  } catch (error) {
    result.unmount();
    throw error;
  }
}
