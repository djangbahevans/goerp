import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { ModelRegistry, ResourceRegistry } from "../schema/index.js";
import { modelRegistry, resourceRegistry } from "../schema/index.js";
import type { useRecordAvailability } from "./use-record-availability.js";

interface PreviewOptions<T> {
  record: T | undefined;
  edits: Partial<T>;
  availability: ReturnType<typeof useRecordAvailability>;
  models?: Pick<ModelRegistry, "resolve"> | undefined;
  registry?: Pick<ResourceRegistry, "resolve"> | undefined;
  client?: Pick<APIClient, "post"> | undefined;
}

// Call invalidate before local edits and reset after discarding edits or accepting a save.
export function useRecordPreview<T extends Record<string, unknown>>(
  resource: string,
  id: string | undefined,
  {
    record,
    edits,
    availability,
    models = modelRegistry,
    registry = resourceRegistry,
    client = apiClient,
  }: PreviewOptions<T>,
) {
  const configuration = useQuery({
    queryKey: ["record-preview", resource],
    queryFn: async () => {
      const model = await models.resolve(resource);
      const fields = model.fields.filter((field) => field.depends_on?.length);
      if (!model.enabled_ops.includes("preview") || fields.length === 0) return null;
      const route = await registry.resolve(resource);
      if (!route.previewPath) return null;
      const triggers = new Set(
        fields.flatMap((field) =>
          (field.depends_on ?? []).map((path) => {
            const [relation, remote] = path.split(".");
            return remote === undefined ? path : `${relation}_id`;
          }),
        ),
      );
      return { path: route.previewPath, fields: fields.map((field) => field.name), triggers };
    },
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
    enabled: !availability.isUnavailable,
  });
  const [result, setResult] = useState<{ signal: AbortSignal; values: Partial<T> }>();
  const [activeRequest, setActiveRequest] = useState<AbortController>();
  const revision = useRef(0);
  const previousEdits = useRef<Partial<T>>({});
  const pending = useRef(false);
  const controller = useRef<AbortController | undefined>(undefined);

  const invalidate = () => {
    revision.current++;
    controller.current?.abort();
  };

  const reset = () => {
    invalidate();
    pending.current = false;
    previousEdits.current = {};
    setResult(undefined);
    setActiveRequest(undefined);
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: a resource/id selection owns its preview lifetime.
  useEffect(() => {
    previousEdits.current = {};
    pending.current = false;
    return () => {
      revision.current++;
      controller.current?.abort();
    };
  }, [resource, id]);

  useEffect(() => {
    const config = configuration.data;
    if (!config || availability.isUnavailable) return;
    if (
      Object.keys(edits).some((key) => config.triggers.has(key) && !Object.is(edits[key], previousEdits.current[key]))
    ) {
      pending.current = true;
    }
    previousEdits.current = edits;
    if (!pending.current) return;

    const requestRevision = ++revision.current;
    const requestController = new AbortController();
    controller.current = requestController;
    const timer = setTimeout(async () => {
      if (!availability.isAvailable() || requestController.signal.aborted) return;
      setActiveRequest(requestController);
      try {
        const response = await client.post<T>(
          config.path,
          { ...record, ...edits, ...(id === undefined ? {} : { id }) },
          { signal: AbortSignal.any([availability.signal, requestController.signal]) },
        );
        if (!availability.isAvailable() || requestController.signal.aborted || revision.current !== requestRevision)
          return;
        const computed: Partial<T> = {};
        for (const key of config.fields as (keyof T)[]) {
          if (Object.hasOwn(response, key)) computed[key] = response[key];
        }
        pending.current = false;
        setResult({ signal: availability.signal, values: computed });
      } catch {
        // Incomplete drafts can fail validation; saving remains the authoritative error path.
        if (revision.current === requestRevision) pending.current = false;
      } finally {
        if (revision.current === requestRevision) setActiveRequest(undefined);
      }
    }, 300);
    return () => {
      clearTimeout(timer);
      requestController.abort();
    };
  }, [
    configuration.data,
    edits,
    record,
    id,
    client,
    availability.isUnavailable,
    availability.isAvailable,
    availability.signal,
  ]);

  return {
    values: result?.signal === availability.signal ? result.values : ({} as Partial<T>),
    isPreviewing: !availability.isUnavailable && activeRequest !== undefined && !activeRequest.signal.aborted,
    invalidate,
    reset,
  };
}
