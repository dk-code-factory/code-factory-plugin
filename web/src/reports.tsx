import React, { useCallback, useEffect, useRef, useState } from "react";
import { call, openLink } from "./bridge";
import type { IssueReport, ReportPage, ReportFreshness } from "./types";

const statuses = [
  "queued",
  "investigating",
  "needs_information",
  "filing",
  "complete",
  "blocked",
  "needs_attention",
];
const label = (text: string) => text.replaceAll("_", " ");
const knownDate = (text?: string) =>
  text && !text.startsWith("0001-")
    ? new Date(text).toLocaleString()
    : "Unknown";
function Freshness({
  title,
  value,
}: {
  title: string;
  value: ReportFreshness;
}) {
  return (
    <p>
      {title} last verified: {knownDate(value.lastSuccess)}
      {value.error && (
        <span role="status">
          {" "}
          · {value.error} · retry after {knownDate(value.retryAt)}
        </span>
      )}
    </p>
  );
}
export function ReportedIssues({ cursor }: { cursor: number }) {
  const [page, setPage] = useState<ReportPage>({ items: [], nextCursor: "" });
  const [selected, setSelected] = useState("");
  const [detail, setDetail] = useState<IssueReport>();
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [component, setComponent] = useState("");
  const [version, setVersion] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [evidence, setEvidence] = useState("");
  const [note, setNote] = useState("");
  const [reconciled, setReconciled] = useState(false);
  const evidenceKey = useRef(crypto.randomUUID());
  const [loading, setLoading] = useState(false);
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const latestLoad = useRef<(more?: boolean) => Promise<void>>(async () => {});
  useEffect(() => {
    let active = true;
    let inFlight = false;
    let refreshPending = false;
    let pages = 1;
    async function load(more = false) {
      if (more) pages++;
      refreshPending = true;
      if (inFlight) return;
      inFlight = true;
      setLoading(true);
      try {
        // Journal changes queue a refresh rather than cancelling a pending page.
        while (active && refreshPending) {
          refreshPending = false;
          const items = new Map<string, IssueReport>();
          let nextCursor = "";
          for (let i = 0; i < pages; i++) {
            const data = await call<ReportPage>("list_issue_reports", {
              search,
              status,
              component,
              version,
              cursor: nextCursor,
              limit: 30,
            });
            if (!active) return;
            for (const item of data.items) items.set(item.id, item);
            nextCursor = data.nextCursor;
            if (!nextCursor) break;
          }
          setPage({ items: [...items.values()], nextCursor });
          setError("");
        }
      } catch (e) {
        if (active) setError(String(e));
      } finally {
        inFlight = false;
        if (active) setLoading(false);
      }
    }
    latestLoad.current = load;
    setPage({ items: [], nextCursor: "" });
    void load();
    return () => {
      active = false;
    };
  }, [search, status, component, version]);
  const previousCursor = useRef(cursor);
  useEffect(() => {
    if (previousCursor.current !== cursor) {
      previousCursor.current = cursor;
      void latestLoad.current();
    }
  }, [cursor]);
  useEffect(() => {
    setEvidence("");
    setNote("");
    setReconciled(false);
    evidenceKey.current = crypto.randomUUID();
    setDetail(undefined);
  }, [selected]);
  const read = useCallback(
    async () =>
      selected
        ? call<IssueReport>("get_issue_report", { id: selected })
        : undefined,
    [selected],
  );
  useEffect(() => {
    let active = true;
    void read()
      .then((r) => {
        if (active) setDetail(r);
      })
      .catch((e) => {
        if (active) setError(String(e));
      });
    return () => {
      active = false;
    };
  }, [read, cursor]);
  async function mutate(name: string, args: Record<string, unknown>) {
    const target = selected;
    setBusy(true);
    try {
      await call(name, args);
      if (name === "add_issue_evidence" && selectedRef.current === target) {
        setEvidence("");
        evidenceKey.current = crypto.randomUUID();
      }
      const updated = await read();
      if (selectedRef.current === target) setDetail(updated);
      await latestLoad.current();
      setError("");
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Reported issues">
      <div className="toolbar">
        <div>
          <h2>Reported issues</h2>
          <small>Investigation, GitHub state and verified delivery</small>
        </div>
        <button
          disabled={busy}
          onClick={() => void mutate("refresh_issue_reports", {})}
        >
          Refresh issues and releases
        </button>
      </div>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <div className="toolbar">
        <label>
          Search reports
          <input value={search} onChange={(e) => setSearch(e.target.value)} />
        </label>
        <label>
          Processing
          <select
            aria-label="Processing"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
          >
            <option value="">All states</option>
            {statuses.map((s) => (
              <option key={s} value={s}>
                {label(s)}
              </option>
            ))}
          </select>
        </label>
        <label>
          Component
          <input
            value={component}
            onChange={(e) => setComponent(e.target.value)}
            placeholder="All components"
          />
        </label>
        <label>
          Version
          <input
            value={version}
            onChange={(e) => setVersion(e.target.value)}
            placeholder="All versions"
          />
        </label>
      </div>
      <div className="report-layout">
        <div aria-label="Report list">
          {page.items.length === 0 && <p>No reports match these filters.</p>}
          {page.items.map((r) => (
            <article key={r.id}>
              <button
                aria-pressed={r.id === selected}
                onClick={() => setSelected(r.id)}
              >
                {r.observation.surface}: {r.observation.actual}
              </button>
              <p>
                {label(r.processing)} · {r.component || "Unknown component"} ·{" "}
                {r.id}
              </p>
            </article>
          ))}
          {page.nextCursor && (
            <button
              disabled={loading}
              onClick={() => void latestLoad.current(true)}
            >
              Load more reports
            </button>
          )}
        </div>
        {detail && (
          <article aria-label="Report details" key={detail.id}>
            <h3>{detail.observation.surface}</h3>
            <p>Report {detail.id}</p>
            <dl>
              <dt>Processing</dt>
              <dd>{label(detail.processing)}</dd>
              <dt>Outcome</dt>
              <dd>{label(detail.outcome || "Unknown")}</dd>
              <dt>Observed</dt>
              <dd>{knownDate(detail.observation.observedAt)}</dd>
              <dt>Expected</dt>
              <dd>{detail.observation.expected}</dd>
              <dt>Actual</dt>
              <dd>{detail.observation.actual}</dd>
              <dt>Reproduction</dt>
              <dd>
                {detail.observation.steps?.join(" → ") ||
                  detail.observation.reproductionUnknown ||
                  "Unknown"}
              </dd>
            </dl>
            <p>{detail.message}</p>
            {detail.findings && <p>{detail.findings.summary}</p>}
            {detail.findings?.missingInformation && (
              <p role="status">
                Information requested: {detail.findings.missingInformation}
              </p>
            )}
            <h4>GitHub</h4>
            {detail.issue ? (
              <>
                <button
                  onClick={() =>
                    void openLink(detail.issue!.url).catch((e) =>
                      setError(String(e)),
                    )
                  }
                >
                  Open GitHub issue #{detail.issue.number}
                </button>
                <p>
                  State: {detail.issue.state} · Closure reason:{" "}
                  {detail.issue.closureReason || "Unknown"}
                </p>
              </>
            ) : (
              <p>Unknown</p>
            )}
            <h4>Fixes and delivery</h4>
            {detail.fixes.map((f) => (
              <button
                key={f.repository + f.commit}
                onClick={() =>
                  void openLink(
                    f.pr ||
                      `https://github.com/${f.repository}/commit/${f.commit}`,
                  ).catch((e) => setError(String(e)))
                }
              >
                {f.pr ? "Open fix PR" : "Open fix commit"}
              </button>
            ))}
            {detail.releases.map((r) => (
              <button
                key={r.repository + r.version}
                onClick={() =>
                  void openLink(r.url).catch((e) => setError(String(e)))
                }
              >
                Open {r.prerelease ? "prerelease" : "release"} {r.version}
              </button>
            ))}

            <p>
              Linked fix:{" "}
              {detail.fixes.length
                ? detail.fixes
                    .map(
                      (f) =>
                        `${f.repository} ${f.commit} (${f.version || "version Unknown"})`,
                    )
                    .join(", ")
                : "Unknown"}
            </p>
            <p>
              Stable availability:{" "}
              {detail.releases
                .filter((r) => !r.prerelease)
                .map(
                  (r) =>
                    `${r.repository} ${r.version} (component ${r.componentVersion}, ${knownDate(r.publishedAt)})`,
                )
                .join(", ") || "Unknown"}
            </p>
            <p>
              Prerelease availability:{" "}
              {detail.releases
                .filter((r) => r.prerelease)
                .map(
                  (r) =>
                    `${r.repository} ${r.version} (component ${r.componentVersion}, ${knownDate(r.publishedAt)})`,
                )
                .join(", ") || "Unknown"}
            </p>
            <p>
              Observed installed Code Factory version:{" "}
              {detail.installedVersion || "Unknown"}
            </p>
            {Object.entries(detail.versions).map(([name, v]) => (
              <p key={name}>
                {name} observed version: {v || "Unknown"}
              </p>
            ))}
            <Freshness title="GitHub" value={detail.issueSync} />
            <Freshness title="Releases" value={detail.releaseSync} />
            <h4>Additional host evidence</h4>
            {detail.additions.map((text, i) => (
              <p key={i}>{text}</p>
            ))}
            <label>
              What did the host display?
              <textarea
                disabled={busy}
                value={evidence}
                onChange={(e) => setEvidence(e.target.value)}
              />
            </label>
            <button
              disabled={busy || !evidence.trim()}
              onClick={() =>
                void mutate("add_issue_evidence", {
                  id: detail.id,
                  idempotencyKey: evidenceKey.current,
                  text: evidence,
                })
              }
            >
              Add evidence
            </button>
            {["blocked", "needs_attention", "needs_information"].includes(
              detail.processing,
            ) && (
              <div>
                {detail.processing === "needs_attention" && (
                  <>
                    <label>
                      Recovery note
                      <textarea
                        value={note}
                        onChange={(e) => setNote(e.target.value)}
                      />
                    </label>
                    <label>
                      <input
                        type="checkbox"
                        checked={reconciled}
                        onChange={(e) => setReconciled(e.target.checked)}
                      />
                      I inspected the retained workflow and reconciled its
                      effects
                    </label>
                  </>
                )}
                <button
                  disabled={
                    busy ||
                    (detail.processing === "needs_attention" &&
                      !!detail.workflowId &&
                      (!reconciled || !note.trim()))
                  }
                  onClick={() =>
                    void mutate("resume_issue_report", {
                      id: detail.id,
                      reconciled,
                      note,
                    })
                  }
                >
                  Resume or reconcile report
                </button>
              </div>
            )}
            <details>
              <summary>Evidence manifest and history</summary>
              {detail.evidence.map((e, i) => (
                <p key={i}>
                  {e.origin} · {knownDate(e.at)} · {e.sha256 || e.omission}{" "}
                  {e.truncated && "· truncated"}
                </p>
              ))}
              {detail.history.map((h, i) => (
                <p key={i}>
                  {knownDate(h.at)} · {label(h.status)} · {h.detail}
                </p>
              ))}
            </details>
          </article>
        )}
      </div>
    </section>
  );
}
