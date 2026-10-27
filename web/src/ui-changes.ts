import { useSyncExternalStore } from "react";

export interface UIChange {
  cursor: number;
  resync: boolean;
  changes: Record<string, string[]>;
}
const listeners = new Set<() => void>();
const versions = new Map<string, number>();
export function publishUIChange(change: UIChange) {
  for (const [domain, ids] of Object.entries(change.changes)) {
    versions.set(domain, change.cursor);
    for (const id of ids) versions.set(domain + ":" + id, change.cursor);
  }
  if (change.resync) versions.set("*", change.cursor);
  if (!document.hidden) for (const listener of listeners) listener();
}
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) for (const listener of listeners) listener();
});
export function useUIVersion(domains: string[], id?: string) {
  const key = domains.join(",");
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () =>
      Math.max(
        versions.get("*") ?? 0,
        ...key.split(",").map((domain) => {
          if (!id) return versions.get(domain) ?? 0;
          return versions.get(domain + ":" + id) ?? 0;
        }),
      ),
  );
}
