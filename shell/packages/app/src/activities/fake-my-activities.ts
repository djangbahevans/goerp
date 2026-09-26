import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";

// An in-memory stand-in for GET /_meta/scheduled-activities/mine and POST
// .../{id}/done, installed over apiClient by the My activities tests and
// stories. It orders and pages like the engine: open activities soonest
// due first, ties by id, paged by a (due_date, id) cursor.

export interface FakeActivitySeed {
  dueDate: string;
  summary?: string;
  type?: string;
  model?: string;
  recordId?: string;
  recordName?: string | null;
}

export interface FakeMyActivities {
  // "GET /_meta/scheduled-activities/mine" and "POST .../{id}/done", in order.
  requests: string[];
  // The body of each completion that succeeded.
  completions: { id: string; body: unknown }[];
  // The next completion rejects with error instead of succeeding.
  failNextDone: (error: AppError) => void;
  restore: () => void;
}

export function installFakeMyActivities(seeds: FakeActivitySeed[], client = apiClient): FakeMyActivities {
  const user = { id: "u1", name: "Ama Owusu", avatar_url: null };
  const activities = seeds.map((seed, i) => ({
    id: `s${String(i + 1).padStart(4, "0")}`,
    model: seed.model ?? "sales.order",
    record_id: seed.recordId ?? "o1",
    type: seed.type ?? "call",
    summary: seed.summary ?? `Activity ${i + 1}`,
    note: null,
    due_date: seed.dueDate,
    assignee: user,
    created_by: user,
    created_at: "2026-09-24T10:00:00Z",
    done_at: null as string | null,
    done_by: null as typeof user | null,
    feedback: null as string | null,
    record_name: seed.recordName === undefined ? "SO-0001" : seed.recordName,
  }));
  const open = () =>
    activities
      .filter((a) => a.done_at === null)
      .sort((a, b) => a.due_date.localeCompare(b.due_date) || a.id.localeCompare(b.id));

  const requests: string[] = [];
  const completions: { id: string; body: unknown }[] = [];
  let nextDoneError: AppError | null = null;
  const original = { get: client.get, post: client.post };

  client.get = (async (path: string, options?: { params?: Record<string, unknown> }) => {
    if (!path.endsWith("/mine")) {
      throw new AppError({ code: "not_found", message: `no fake for GET ${path}`, httpStatus: 404 });
    }
    requests.push(`GET ${path}`);
    const params = options?.params ?? {};
    const limit = Number(params.limit ?? 50);
    const cursor = params.cursor as string | undefined;
    const after = cursor ? open().filter((a) => `${a.due_date},${a.id}` > cursor) : open();
    const page = after.slice(0, limit);
    const hasMore = after.length > limit;
    const last = page.at(-1);
    return {
      data: page,
      meta: { cursor: hasMore && last ? `${last.due_date},${last.id}` : null, has_more: hasMore },
    };
  }) as typeof client.get;

  client.post = (async (path: string, body?: unknown) => {
    requests.push(`POST ${path}`);
    const activity = activities.find((a) => path === `/_meta/scheduled-activities/${a.id}/done`);
    if (!activity) throw new AppError({ code: "not_found", message: "not found", httpStatus: 404 });
    if (nextDoneError) {
      const error = nextDoneError;
      nextDoneError = null;
      throw error;
    }
    activity.done_at = new Date().toISOString();
    activity.done_by = user;
    activity.feedback = (body as { feedback?: string }).feedback ?? null;
    completions.push({ id: activity.id, body });
    return { ...activity };
  }) as typeof client.post;

  return {
    requests,
    completions,
    failNextDone: (error) => {
      nextDoneError = error;
    },
    restore: () => {
      client.get = original.get;
      client.post = original.post;
    },
  };
}
