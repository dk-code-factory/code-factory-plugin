import { useSyncExternalStore } from "react";

export interface UIChange {
  cursor: number;
  resync: boolean;
  changes: Record<string, string[]>;
}
const listeners = new Set<() => void>();
const versions = new Map<string, number>();
let revision = 0;
export function publishUIChange(change: UIChange) {
  // Subscription revisions must advance even when a restored database has an
  // earlier journal cursor. The cursor is only a server reconnect position.
  const next = ++revision;
  for (const [domain, ids] of Object.entries(change.changes)) {
    versions.set(domain, next);
    for (const id of ids) versions.set(domain + ":" + id, next);
  }
  if (change.resync) versions.set("*", next);
  if (!document.hidden) for (const listener of listeners) listener();
}
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) for (const listener of listeners) listener();
});
export function useUIResyncVersion() {
  return useUIVersion(["*"]);
}
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
