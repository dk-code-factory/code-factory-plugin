import { useEffect, useRef, useState } from "react";
import { useUIVersion } from "./ui-changes";
import { call, openLink } from "./bridge";
import type { PRReviews } from "./types";

export function PostedReviews({
  itemId,
  head,
  version,
  repository,
}: {
  itemId: string;
  head: string;
  version: string;
  repository: string;
}) {
  const uiVersion = useUIVersion(["pr-reviews", "observation"], itemId);
  const repositoryVersion = useUIVersion(["repository"], repository);
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
  }, [itemId, version, refresh, uiVersion, repositoryVersion]);
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

export function PRReviewPolicyControls({
  itemId,
  repository,
}: {
  itemId: string;
  repository: string;
}) {
  const policyVersion = useUIVersion(["pr-review-policy"], itemId);
  const repositoryVersion = useUIVersion(["repository"], repository);
  const configVersion = useUIVersion(["config"]);
  const target = useRef({ itemId, repository, active: false });
  if (target.current.itemId !== itemId || target.current.repository !== repository)
    target.current = { itemId, repository, active: false };
  const observation = useRef(0);
  const [policy, setPolicy] = useState<{
    itemId: string;
    autoPostReview: boolean | null;
    effective: { enabled: boolean; source: string };
  }>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const current = target.current;
    current.active = true;
    setBusy(false);
    setError("");
    return () => { current.active = false; };
  }, [itemId, repository]);
  useEffect(() => {
    let alive = true;
    observation.current++;
    setPolicy(undefined);
    void call<typeof policy>("read_review_policy", { id: itemId })
      .then((p) => {
        if (alive) {
          setPolicy(p);
          setError("");
        }
      })
      .catch((e) => {
        if (alive) setError(String(e));
      });
    return () => {
      alive = false;
    };
  }, [itemId, policyVersion, repositoryVersion, configVersion]);
  return (
    <section aria-label="PR review posting policy">
      <h2>Review posting</h2>
      {policy && (
        <>
          <p>
            Automatic posting: {policy.effective.enabled ? "On" : "Off"} ·
            Source: {policy.effective.source}
          </p>
          <label>
            PR automatic review posting
            <select
              aria-label="PR automatic review posting"
              disabled={busy}
              value={
                policy.autoPostReview == null
                  ? "inherit"
                  : String(policy.autoPostReview)
              }
              onChange={(e) => {
                const autoPostReview =
                  e.target.value === "inherit"
                    ? null
                    : e.target.value === "true";
                const current = target.current;
                const observed = observation.current;
                const ownsTarget = () => target.current === current && current.active;
                setBusy(true);
                void call<NonNullable<typeof policy>>("save_review_policy", {
                  id: itemId,
                  autoPostReview,
                })
                  .then((p) => {
                    if (!ownsTarget() || observation.current !== observed) return;
                    setPolicy(p);
                    setError("");
                  })
                  .catch((e) => {
                    if (ownsTarget() && observation.current === observed)
                      setError(String(e));
                  })
                  .finally(() => {
                    if (ownsTarget()) setBusy(false);
                  });
              }}
            >
              <option value="inherit">
                Inherit repository or global policy
              </option>
              <option value="false">Off</option>
              <option value="true">On</option>
            </select>
          </label>
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

export function RetainedReviewStatus({
  run,
  changed,
}: {
  run: import("./types").Run;
  changed: () => Promise<unknown>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!run.reviewWorkflow) return null;
  const policy = run.effectiveAutoPostReview;
  async function publish(reconcile = false) {
    setBusy(true);
    setError("");
    try {
      await call(
        reconcile ? "reconcile_review_publication" : "post_retained_review",
        reconcile
          ? { id: run.postingOperation }
          : { runId: run.id, artifact: run.reviewArtifact },
      );
      await changed();
    } catch (e) {
      setError(
        `${String(e)} Check the retained operation before trying again.`,
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Retained review workflow">
      <h2>Review workflow</h2>
      <p>
        Stage: {run.reviewStage ?? "resolve-configuration"} ·{" "}
        {run.reviewArtifact
          ? run.reviewComplete
            ? "Complete review"
            : "Incomplete review"
          : "Review has not completed"}
      </p>
      {policy && (
        <p>
          Prepared automatic posting: {policy.enabled ? "On" : "Off"} · Source:{" "}
          {policy.source}. Current policy is checked before submission.
        </p>
      )}
      {run.reviewResult?.skipped_reviewers.map((s) => (
        <p key={s.reviewer}>
          Skipped {s.reviewer}: {s.reason}
        </p>
      ))}
      {run.reviewResult?.failed_reviewers?.map((s) => (
        <p key={s.reviewer} role="alert">
          Incomplete {s.reviewer}: {s.reason}
        </p>
      ))}
{run.reviewComplete&&run.reviewResult?.complete&&run.reviewResult.finding_count===0&&<p>No actionable findings.</p>}
      <p>Posting: {run.postingState ?? "Not submitted"}</p>
      {run.reviewOmissions?.map((o) => (
        <p key={o.finding}>
          Finding {o.finding + 1} omitted: {o.reason}
        </p>
      ))}
      {run.postingReceipt && (
        <p>
          <a
            href={run.postingReceipt.url}
            onClick={(e) => {
              e.preventDefault();
              void openLink(run.postingReceipt!.url);
            }}
          >
            Verified review receipt
          </a>{" "}
          · {run.postingReceipt.comments} inline comments · Reviewed head{" "}
          {run.postingReceipt.head.slice(0, 12)}
        </p>
      )}
      {run.reviewArtifact && run.reviewComplete && !run.postingReceipt && (
        <button
          disabled={
            busy ||
            run.postingState === "uncertain" ||
            run.postingState === "submitting"
          }
          onClick={() => void publish()}
        >
          Post retained review
        </button>
      )}
      {run.postingOperation &&
        ["uncertain", "submitting"].includes(run.postingState ?? "") && (
          <button disabled={busy} onClick={() => void publish(true)}>
            Reconcile review publication
          </button>
        )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
