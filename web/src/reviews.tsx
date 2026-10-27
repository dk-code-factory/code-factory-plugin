import { useEffect, useState } from "react";
import { useUIVersion } from "./ui-changes";
import { call, openLink } from "./bridge";
import type { PRReviews } from "./types";

export function PostedReviews({
  itemId,
  head,
  version,
}: {
  itemId: string;
  head: string;
  version: string;
}) {
  const uiVersion = useUIVersion(["pr-reviews"], itemId);
  const [data, setData] = useState<PRReviews>();
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let alive = true;

    async function load() {
      try {
        const result = await call<PRReviews>("read_pr_reviews", {
          id: itemId,
          saved: true,
        });

        if (alive) {
          setData(result);
          setError("");
        }
      } catch (e) {
        if (alive) setError(String(e));
      }
    }
    void load();
    return () => {
      alive = false;
    };
  }, [itemId, version, refresh, uiVersion]);
  function link(url: string, label: string) {
    return (
      <a
        href={url}
        onClick={(e) => {
          e.preventDefault();
          void openLink(url)
            .then((r) => {
              if (r.isError) setError("The host could not open GitHub.");
            })
            .catch(() => setError("Could not open GitHub."));
        }}
      >
        {label}
      </a>
    );
  }
  const saved = data?.itemId === itemId ? data : undefined;
  const verified = saved?.syncedAt && !saved.syncedAt.startsWith("0001-");
  return (
    <section aria-label="Posted reviews">
      <h2>Posted reviews</h2>
      <button onClick={() => setRefresh((v) => v + 1)}>Refresh reviews</button>
      {(error || saved?.error) && <p role="alert">{error || saved?.error}</p>}
      {!saved && !error && <p role="status">Loading posted reviews…</p>}
      {saved && (
        <p>
          {verified
            ? `Last checked ${new Date(saved.syncedAt).toLocaleString()}`
            : "Publication has not been verified."}
        </p>
      )}
      {saved?.refresh === "pending" && (
        <p role="status">Refreshing saved reviews…</p>
      )}
      {saved?.refresh === "paused" && <p>Repository monitoring is paused.</p>}
      {saved?.refresh === "cooldown" && (
        <p>Refresh is waiting for its retry time.</p>
      )}
      {verified && !saved?.reviews.length && (
        <p>No posted reviews found at the last successful check.</p>
      )}
      {saved?.reviews.map((r) => (
        <article key={r.id} className="posted-review">
          <p>
            {link(r.url, `Review by ${r.author}`)} ·{" "}
            {r.state.replaceAll("_", " ")} ·{" "}
            <time dateTime={r.submittedAt}>
              {new Date(r.submittedAt).toLocaleString()}
            </time>
          </p>
          <p>
            Commit <code>{r.head.slice(0, 12)}</code> ·{" "}
            {!head
              ? "Head comparison unavailable"
              : r.head === head
                ? "Current head"
                : "Earlier head"}
          </p>
          {r.body && <p className="body">{r.body}</p>}
          {r.comments.map((c) => (
            <div key={c.id}>
              <p>
                {link(c.url, `${c.path}${c.line ? `:${c.line}` : ""}`)} ·{" "}
                {c.author}
              </p>
              <p className="body">{c.body}</p>
            </div>
          ))}
        </article>
      ))}
    </section>
  );
}
