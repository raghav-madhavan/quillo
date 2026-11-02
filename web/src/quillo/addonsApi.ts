// Quillo: client for the addon manager API (/api/quillo/admin). It sits
// outside Silo's generated v2 client because these routes are Quillo's own.
import { fetchWithSession } from "@/api/client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

export interface AddonSummary {
  catalogs: number;
  home_row_catalogs: number;
  search: boolean;
  metadata: boolean;
  streams: boolean;
  types: string[];
  configurable: boolean;
}

export interface Addon {
  id: string;
  manifest_url: string;
  manifest_host: string;
  addon_id: string;
  name: string;
  version: string;
  description: string;
  logo?: string;
  enabled: boolean;
  position: number;
  summary: AddonSummary;
  last_checked_at: string;
  last_error: string | null;
}

export interface AddonPreview {
  manifest_url: string;
  addon_id: string;
  name: string;
  version: string;
  description: string;
  logo?: string;
  summary: AddonSummary;
}

export interface AddonPreset {
  key: string;
  name: string;
  description: string;
  manifest_url: string;
  installed: boolean;
}

/** An error whose message is a plain sentence the page can show as it is. */
export class AddonApiError extends Error {}

const BASE = "/api/quillo/admin";

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const { res } = await fetchWithSession(BASE + path, {
    method,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const message =
      data && typeof data.message === "string"
        ? data.message
        : "Something went wrong on the server. Try again.";
    throw new AddonApiError(message);
  }
  return data as T;
}

const addonsKey = ["quillo", "addons"] as const;
const presetsKey = ["quillo", "addon-presets"] as const;

export function useAddons() {
  return useQuery({
    queryKey: addonsKey,
    queryFn: () => call<{ items: Addon[] }>("GET", "/addons").then((d) => d.items),
  });
}

export function useAddonPresets() {
  return useQuery({
    queryKey: presetsKey,
    queryFn: () => call<{ items: AddonPreset[] }>("GET", "/addons/presets").then((d) => d.items),
  });
}

function useInvalidate() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: addonsKey });
    void qc.invalidateQueries({ queryKey: presetsKey });
  };
}

export function usePreviewAddon() {
  return useMutation({
    mutationFn: (url: string) => call<AddonPreview>("POST", "/addons/preview", { url }),
  });
}

export function useInstallAddon() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (url: string) => call<Addon>("POST", "/addons", { url }),
    onSuccess: invalidate,
  });
}

export function useSetAddonEnabled() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      call<Addon>("PATCH", `/addons/${id}`, { enabled }),
    onSuccess: invalidate,
  });
}

export function useRefreshAddon() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (id: string) => call<Addon>("POST", `/addons/${id}/refresh`),
    onSuccess: invalidate,
  });
}

export function useRemoveAddon() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (id: string) => call<void>("DELETE", `/addons/${id}`),
    onSuccess: invalidate,
  });
}

export function useReorderAddons() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (ids: string[]) => call<void>("PUT", "/addons/order", { ids }),
    onSettled: invalidate,
  });
}
