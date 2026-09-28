import { apiClient } from "@goerp/sdk";

// The channels a user can switch off. in_app has no preference: it is
// always delivered (notification-system.md §8).
export type ToggleChannel = "email" | "sms" | "push";
export const TOGGLE_CHANNELS: readonly ToggleChannel[] = ["email", "sms", "push"];

export type ChannelSettings = Record<ToggleChannel, boolean>;
export type ChannelPatch = Partial<ChannelSettings>;

// GET /_notif/preferences (notification-system.md §8). `types` holds the
// types with their own settings; the rest follow `global`.
export interface NotificationPreferences {
  availableChannels: string[];
  global: ChannelSettings;
  types: Record<string, ChannelSettings>;
}

export interface NotificationPreferencesPatch {
  global?: ChannelPatch;
  types?: Record<string, ChannelPatch>;
}

interface NotificationPreferencesWire {
  available_channels: string[];
  global: ChannelSettings;
  types: Record<string, ChannelSettings>;
}

function fromWire(wire: NotificationPreferencesWire): NotificationPreferences {
  return { availableChannels: wire.available_channels, global: wire.global, types: wire.types };
}

// Injectable for stories and tests; the route uses the real API.
export interface NotificationPreferencesClient {
  get: (signal?: AbortSignal) => Promise<NotificationPreferences>;
  update: (patch: NotificationPreferencesPatch) => Promise<NotificationPreferences>;
}

export const notificationPreferencesClient: NotificationPreferencesClient = {
  get: async (signal) =>
    fromWire(await apiClient.get<NotificationPreferencesWire>("/_notif/preferences", signal ? { signal } : undefined)),
  update: async (patch) => fromWire(await apiClient.patch<NotificationPreferencesWire>("/_notif/preferences", patch)),
};

export function typeSettings(prefs: NotificationPreferences, type: string): ChannelSettings {
  return prefs.types[type] ?? prefs.global;
}

// Applies patch the way the engine does: global first, then each type's
// patch onto its current settings, or onto the updated global settings for
// a type that has none of its own. A type patched to equal global drops its
// own settings and follows global again.
export function applyPreferencesPatch(
  prefs: NotificationPreferences,
  patch: NotificationPreferencesPatch,
): NotificationPreferences {
  const global = { ...prefs.global, ...patch.global };
  const types = { ...prefs.types };
  for (const [type, p] of Object.entries(patch.types ?? {})) {
    const next = { ...(prefs.types[type] ?? global), ...p };
    if (TOGGLE_CHANNELS.every((c) => next[c] === global[c])) delete types[type];
    else types[type] = next;
  }
  return { ...prefs, global, types };
}
