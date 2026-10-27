import React, {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { createRoot } from "react-dom/client";
import { call, isConnectionFailure, openLink } from "./bridge";
import { useModalFocus } from "./modal-focus";
import type {
  Attention,
  Config,
  Detail,
  Entry,
  EventPage,
  JournalEvent,
  Profile,
  Run,
  Snapshot,
  PullRequest,
  QuestionRecovery,
} from "./types";
import "./style.css";
import {
  publishUIChange,
  useUIVersion,
  useUIResyncVersion,
  type UIChange,
} from "./ui-changes";
const ReportedIssues = lazy(() =>
  import("./reports").then((m) => ({ default: m.ReportedIssues })),
);

const PostedReviews = lazy(() =>
  import("./reviews").then((m) => ({ default: m.PostedReviews })),
);
const Settings = lazy(() => import("./settings"));

function latestRun(runs: Run[], itemId: string) {
  const retained = runs
    .filter((r) => r.itemId === itemId && r.status !== "prepared")
    .sort((a, b) => Date.parse(b.created) - Date.parse(a.created));
  return retained.find((r) => r.status !== "cleaned") ?? retained[0];
}
function WorkStatus({
  run,
  blocked = false,
  status: summary,
}: {
  run?: Run;
  blocked?: boolean;
  status?: string;
}) {
  const status =
    summary ??
    (blocked
      ? "Waiting for you"
      : !run || run.status === "prepared"
        ? "Not started"
        : run.status === "deferred"
          ? "Waiting for GitHub"
          : ["queued", "starting"].includes(run.status)
            ? "Starting"
            : ["running", "waiting"].includes(run.status)
              ? "Active"
              : run.status === "failed"
                ? "Failed"
                : run.status === "terminated"
                  ? "Stopped"
                  : run.status === "cleaned"
                    ? "Finished"
                    : "Inactive");
  return (
    <span
      className="status"
      role="status"
      aria-label="Work status"
      aria-live="polite"
      aria-atomic="true"
    >
      {status}
    </span>
  );
}

function Factory() {
  const [snap, setSnap] = useState<Snapshot>();
  const settingsView =
    document.documentElement.dataset.codeFactoryView === "settings";
  const boot = (
    window as unknown as {
      factoryBoot?: { tab: string; search: string; filter: string };
    }
  ).factoryBoot;
  const [tab, setTab] = useState(
    settingsView ? "Settings" : (boot?.tab ?? "Inbox"),
  );
  const [runBack, setRunBack] = useState("Messages");
  const pageRef = useRef<HTMLElement>(null);
  const positions = useRef(
    new Map<string, { scroll: number; focus?: string }>(),
  );
  const [error, setError] = useState("");
  const [pollError, setPollError] = useState("");
  const [viewError, setViewError] = useState("");
  const viewRetry = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
  const viewBackoff = useRef(1000);
  const viewNeedsRetry = useRef(false);
  const latestRefresh = useRef<() => Promise<number>>(async () => 0);
  const [appliedQuery, setAppliedQuery] = useState("");
  const [connection, setConnection] = useState("Connecting to Work…");
  const [selected, setSelected] = useState("");
  const [runId, setRunId] = useState("");
  const [launch, setLaunch] = useState<Entry>();
  const [draftRun, setDraftRun] = useState<Run>();
  const [linkDrafts, setLinkDrafts] = useState<Record<string, string>>({});
  const [messageDrafts, setMessageDrafts] = useState<Record<string, string>>(
    {},
  );
  const [attentionId, setAttentionId] = useState("");
  const [consoleRuns, setConsoleRuns] = useState<Record<string, boolean>>({});
  const [queries, setQueries] = useState<
    Record<string, { filter: string; search: string }>
  >(boot ? { [boot.tab]: { search: boot.search, filter: boot.filter } } : {});
  const filter = queries[tab]?.filter ?? "open";
  const search = queries[tab]?.search ?? "";
  const [pages, setPages] = useState<Record<string, number>>({});
  const offset = pages[tab] ?? 0;
  const setOffset = (value: number) =>
    setPages((old) => ({ ...old, [tab]: value }));
  const setFilter = (filter: string) => {
    setOffset(0);
    setQueries((old) => ({
      ...old,
      [tab]: { search: old[tab]?.search ?? "", filter },
    }));
  };
  const setSearch = (search: string) => {
    setOffset(0);
    setQueries((old) => ({
      ...old,
      [tab]: {
        filter: old[tab]?.filter ?? "open",
        search,
      },
    }));
  };
  const [chatDrafts, setChatDrafts] = useState<Record<string, ChatDraft>>({});
  const activeView = useRef({ tab, selected });
  activeView.current = { tab, selected };
  const cursor = useRef(0);
  const mounted = useRef(true);
  const request = useRef(0);
  const appliedCursor = useRef(0);
  const hasData = useRef(false);
  const dirtyDomains = useRef(new Map<string, number>());
  const acceptedRuns = useRef(new Map<string, Run>());
  const launchOpen = Boolean(launch || draftRun);
  const queryRef = useRef({
    tab,
    filter,
    search,
    offset,
    selected,
    launchOpen,
  });
  queryRef.current = { tab, filter, search, offset, selected, launchOpen };
  const refresh = useCallback(async () => {
    const generation = ++request.current;
    const q = queryRef.current;
    const queue = (
      {
        Working: "working",
        Inbox: "issues",
        Mine: "mine",
        Review: "review",
      } as Record<string, string>
    )[q.tab];
    const dirty = new Map(dirtyDomains.current);
    const views = settingsView
      ? ["settings"]
      : [
          ...(!hasData.current ||
          ["identity", "repository", "profile", "config"].some((d) =>
            dirty.has(d),
          )
            ? ["shell"]
            : []),
          ...(!hasData.current ||
          ["run", "attention", "question-recovery"].some((d) => dirty.has(d)) ||
          ["Agent", "Messages"].includes(q.tab)
            ? ["messages"]
            : []),
          ...(queue ? ["inbox"] : []),
          ...(q.tab === "Agent" ? ["reviews"] : []),
          ...(q.launchOpen ? ["launch"] : []),
        ];
    if (!views.length) views.push("selection");
    let s: Partial<Snapshot>;
    try {
      s = await call<Partial<Snapshot>>("read_ui_state", {
        views,
        queue,
        filter: q.filter,
        search: q.search,
        offset: q.offset,
        limit: 50,
        id: q.selected,
      });
    } catch (error) {
      if (mounted.current && generation === request.current) {
        viewNeedsRetry.current = true;
        setViewError(
          isConnectionFailure(error)
            ? "Connection interrupted. Retrying saved view…"
            : String(error),
        );
        setConnection(
          isConnectionFailure(error)
            ? "Retrying saved view…"
            : "Saved view unavailable",
        );
        clearTimeout(viewRetry.current);
        if (isConnectionFailure(error)) {
          viewRetry.current = setTimeout(() => {
            if (mounted.current && !document.hidden)
              void latestRefresh.current().catch(() => {});
          }, viewBackoff.current);
          viewBackoff.current = Math.min(viewBackoff.current * 2, 30000);
        }
      }
      throw error;
    }
    if (
      mounted.current &&
      generation === request.current &&
      (s.cursor ?? 0) >= appliedCursor.current
    ) {
      appliedCursor.current = s.cursor ?? 0;
      if (queue && s.inbox)
        setAppliedQuery(JSON.stringify([q.tab, q.filter, q.search, q.offset]));
      clearTimeout(viewRetry.current);
      viewNeedsRetry.current = false;
      viewBackoff.current = 1000;
      setViewError("");
      hasData.current = true;
      const covered = new Set<string>();
      if (views.includes("shell"))
        for (const d of ["identity", "repository", "profile", "config"])
          covered.add(d);
      if (views.includes("messages"))
        for (const d of ["run", "attention", "question-recovery"])
          covered.add(d);
      if (views.includes("inbox"))
        for (const d of [
          "item",
          "user",
          "assessment",
          "relationship",
          "ticket",
          "linked-pr",
        ])
          covered.add(d);
      if (views.includes("reviews")) covered.add("pr-reviews");
      if (views.includes("launch") || views.includes("settings"))
        for (const d of ["repository", "profile", "config", "skill"])
          covered.add(d);
      if (views.includes("settings"))
        for (const d of ["github-usage", "github-notifications"])
          covered.add(d);
      for (const [domain, invalidatedAt] of dirty) {
        if (
          covered.has(domain) &&
          (s.cursor ?? 0) >= invalidatedAt &&
          dirtyDomains.current.get(domain) === invalidatedAt
        ) {
          dirtyDomains.current.delete(domain);
        }
      }
      let saved = s;
      if (s.runs) {
        const runs = new Map(s.runs.map((run) => [run.id, run]));
        for (const [id, accepted] of acceptedRuns.current) {
          const projected = runs.get(id);
          // Successful Start cannot return to prepared. A projection may still
          // precede acceptance; retain that local acknowledgement until it catches up.
          if (!projected || projected.status === "prepared")
            runs.set(id, accepted);
          else acceptedRuns.current.delete(id);
        }
        saved = { ...s, runs: [...runs.values()] };
      }
      setSnap(
        (old) =>
          ({
            inbox: [],
            runs: [],
            attention: [],
            repositories: [],
            profiles: [],
            skills: [],
            ...old,
            ...saved,
          }) as Snapshot,
      );
      setConnection("Connected");
      setPollError("");
    }
    return s.cursor ?? 0;
  }, [settingsView]);
  latestRefresh.current = refresh;
  const firstQuery = useRef(true);
  useEffect(() => {
    if (firstQuery.current) {
      firstQuery.current = false;
      return;
    }
    void refresh().catch(() => {});
  }, [tab, filter, search, offset, selected, launchOpen, refresh]);
  const action = useCallback(
    async (
      name: string,
      args: Record<string, unknown> = {},
      failed?: (message: string) => void,
    ) => {
      let succeeded = true;
      try {
        await call(name, args);
        setError("");
      } catch (e) {
        if (failed) failed(String(e));
        else setError(String(e));
        succeeded = false;
      }
      // Publication carries later state updates, including effects of failed
      // mutations. Acceptance does not wait for projection or provider completion.
      return succeeded;
    },
    [],
  );
  useEffect(() => {
    mounted.current = true;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    let busy = false;
    let initialized = false;
    let backoff = 1000;
    async function watch() {
      if (stopped || busy) return;
      busy = true;
      try {
        if (!initialized) {
          cursor.current = await refresh();
          initialized = true;
        } else {
          const change = await call<UIChange>("watch_ui_changes", {
            after: cursor.current,
            waitMs: 25000,
          });
          if (stopped) return;
          if (change.resync || Object.keys(change.changes).length) {
            if (change.resync) {
              // A hidden view does not refresh immediately, but any reply from
              // the previous state epoch must already be obsolete.
              request.current++;
              setAppliedQuery("");
              appliedCursor.current = 0;
              acceptedRuns.current.clear();
              dirtyDomains.current.clear();
            }
            // One publication updates panel subscribers and the affected active view.
            publishUIChange(change);
            const domains = Object.keys(change.changes);
            for (const domain of domains)
              dirtyDomains.current.set(domain, change.cursor);
            if (change.resync)
              for (const domain of [
                "identity",
                "repository",
                "profile",
                "config",
                "run",
                "attention",
                "question-recovery",
              ])
                dirtyDomains.current.set(domain, change.cursor);
            const q = queryRef.current;
            const inboxDomains = [
              "item",
              "user",
              "assessment",
              "relationship",
              "ticket",
              "linked-pr",
              "identity",
              "run",
              "attention",
              "repository",
              "config",
              "profile",
            ];
            const relevant = settingsView
              ? domains.some((d) =>
                  [
                    "repository",
                    "config",
                    "profile",
                    "skill",
                    "github-usage",
                  ].includes(d),
                )
              : q.tab === "Reported issues"
                ? domains.some((d) =>
                    ["run", "attention", "question-recovery"].includes(d),
                  )
                : domains.some(
                    (d) =>
                      inboxDomains.includes(d) ||
                      d === "question-recovery" ||
                      d === "pr-reviews",
                  );
            if (
              !document.hidden &&
              (change.resync ||
                relevant ||
                (q.launchOpen && domains.includes("skill")))
            )
              await refresh();
          }
          cursor.current = change.cursor;
        }
        if (!stopped) {
          if (!viewNeedsRetry.current) setConnection("Connected");
          setPollError("");
        }
        backoff = 1000;
      } catch (e) {
        if (!stopped) {
          setConnection("Reconnecting…");
          if (!isConnectionFailure(e)) setPollError(String(e));
          backoff = Math.min(backoff * 2, 30000);
        }
      } finally {
        busy = false;
        if (!stopped) timer = setTimeout(watch, backoff === 1000 ? 0 : backoff);
      }
    }
    void watch();
    const visible = () => {
      if (!document.hidden) {
        clearTimeout(timer);
        void refresh().catch((e) => setPollError(String(e)));
        void watch();
      }
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      stopped = true;
      mounted.current = false;
      clearTimeout(timer);
      clearTimeout(viewRetry.current);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [refresh]);
  const pending = settingsView
    ? []
    : (snap?.attention.filter((a) => a.status === "pending") ?? []);
  const waiting = pending.filter((a) =>
    snap?.runs.some(
      (r) =>
        r.id === a.runId &&
        (a.origin === "async" || a.origin === "text"
          ? !["prepared", "cleaned"].includes(r.status)
          : ["waiting", "running"].includes(r.status)),
    ),
  );
  const currentAttention = pending.find((a) => a.id === attentionId);
  const queue = ["Working", "Inbox", "Mine", "Review"].includes(tab);
  const viewer = snap?.viewer;
  const currentQuery = JSON.stringify([tab, filter, search, offset]);
  const queryReady = appliedQuery === currentQuery;
  const visibleItems = queryReady ? (snap?.inbox ?? []) : [];

  const pageKey = tab === "Agent" ? `Agent:${runId || selected}` : tab;
  function navigate(next: string) {
    positions.current.set(pageKey, {
      scroll: window.scrollY,
      focus:
        document.activeElement instanceof HTMLElement
          ? document.activeElement.dataset.focusKey
          : undefined,
    });
    setTab(next);
  }
  useLayoutEffect(() => {
    if (queue && !queryReady) return;
    const saved = positions.current.get(pageKey);
    const target = saved?.focus
      ? [...document.querySelectorAll<HTMLElement>("[data-focus-key]")].find(
          (el) => el.dataset.focusKey === saved.focus,
        )
      : undefined;
    const heading = pageRef.current?.querySelector<HTMLElement>("h1, h2");
    if (!target && heading) heading.tabIndex = -1;
    const formFocused =
      pageRef.current?.contains(document.activeElement) &&
      document.activeElement?.matches("input, select, textarea");
    const fallback = queue
      ? pageRef.current?.querySelector<HTMLElement>("input")
      : tab === "Messages"
        ? (pageRef.current?.querySelector<HTMLElement>(
            "button.item, summary",
          ) ?? document.querySelector<HTMLElement>("nav button[aria-current]"))
        : tab === "Settings"
          ? pageRef.current?.querySelector<HTMLElement>("summary")
          : (heading ?? pageRef.current);
    if (!formFocused) {
      (target ?? fallback)?.focus({ preventScroll: true });
      window.scrollTo(0, saved?.scroll ?? 0);
    }
  }, [pageKey, Boolean(snap), queryReady]);
  const openRun = (id: string) => {
    const run = snap?.runs.find((r) => r.id === id);
    if (tab !== "Agent") setRunBack("Messages");
    setSelected(run?.itemId ?? "");
    setRunId(id);
    navigate("Agent");
  };
  const openWorkItem = (entry: Entry) => {
    const runs =
      snap?.runs
        .filter((r) => r.itemId === entry.item.id && r.status !== "cleaned")
        .sort((a, b) => Date.parse(b.created) - Date.parse(a.created)) ?? [];
    const existing = runs.find((r) => r.status !== "prepared");
    const prepared = runs.find((r) => r.status === "prepared");
    const repository = snap?.repositories.find(
      (r) => r.id === entry.item.repository,
    );
    const review = tab === "Review";
    const defaultProfile = review
      ? repository?.reviewProfileId || snap?.config.reviewProfileId || "review"
      : repository?.implementationProfileId ||
        snap?.config.implementationProfileId ||
        "implement";
    const profile =
      prepared?.profile ?? snap?.profiles.find((p) => p.id === defaultProfile);
    setChatDrafts((old) =>
      old[entry.item.id]
        ? old
        : {
            ...old,
            [entry.item.id]: {
              profile,
              message: prepared?.prompt ?? expandPrompt(profile, entry),
              prepared,
              preparedMessage: prepared?.prompt,
            },
          },
    );
    setSelected(entry.item.id);
    setRunId(existing?.id ?? "");
    setRunBack(tab);
    navigate("Agent");
  };
  const acceptRun = (run: Run, itemId: string, origin: string) => {
    acceptedRuns.current.set(run.id, run);
    setSnap((old) =>
      old
        ? { ...old, runs: [...old.runs.filter((r) => r.id !== run.id), run] }
        : old,
    );
    if (
      activeView.current.tab === "Agent" &&
      activeView.current.selected === itemId
    ) {
      setRunId(run.id);
      setRunBack(origin);
    }
  };
  const openAttention = (id: string) => {
    const a = pending.find((a) => a.id === id);
    if (a) {
      openRun(a.runId);
      setAttentionId(id);
    }
  };
  const attentionRun = snap?.runs.find((r) => r.id === currentAttention?.runId);
  const currentRun = snap?.runs.find((r) => r.id === runId);
  const agentItemId = currentRun?.itemId ?? selected;
  const agentEntry =
    snap?.selectedEntry?.item.id === agentItemId
      ? snap.selectedEntry
      : snap?.inbox.find((e) => e.item.id === agentItemId);
  return (
    <main>
      {!settingsView && (
        <nav aria-label="Main navigation">
          {[
            { id: "Working", label: "Working" },
            { id: "Inbox", label: "Issues" },
            { id: "Mine", label: "My PRs" },
            { id: "Review", label: "Review PRs" },
            { id: "Messages", label: "Messages" },
            { id: "Reported issues", label: "Reported issues" },
          ].map((t) => (
            <button
              key={t.id}
              data-focus-key={`nav-${t.id}`}
              aria-current={
                (tab === "Agent" ? runBack : tab) === t.id ? "page" : undefined
              }
              onClick={() => navigate(t.id)}
            >
              {t.label}{" "}
              {t.id === "Messages" && waiting.length > 0 && (
                <b className="badge">{waiting.length}</b>
              )}
            </button>
          ))}
          <span
            className={connection === "Connected" ? "sr-only" : "connection"}
            role="status"
            aria-label="Connection status"
            aria-live="polite"
            aria-atomic="true"
          >
            {connection}
          </span>
        </nav>
      )}
      {settingsView && (
        <span
          className={connection === "Connected" ? "sr-only" : "connection"}
          role="status"
          aria-label="Connection status"
          aria-live="polite"
          aria-atomic="true"
        >
          {connection}
        </span>
      )}
      {pollError && (
        <div role="alert" className="error">
          {pollError}
        </div>
      )}
      {viewError && (
        <div role="alert" className="error">
          {viewError}
        </div>
      )}
      {error && (
        <div role="alert" className="error">
          {error}
          <button onClick={() => setError("")} aria-label="Dismiss error">
            ×
          </button>
        </div>
      )}
      <Suspense fallback={<p role="status">Loading view…</p>}>
        {!snap ? (
          <section className="page" aria-label="Loading saved view">
            {queue && (
              <div className="toolbar">
                <input
                  aria-label="Search inbox"
                  placeholder="Search"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                />
                <select
                  aria-label="Inbox filter"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                >
                  {["open", "all", "pending", "new", "unread", "acted-on"].map(
                    (f) => (
                      <option key={f}>{f}</option>
                    ),
                  )}
                </select>
              </div>
            )}
            <p role="status">Loading saved state…</p>
          </section>
        ) : (
          <>
            {tab === "Reported issues" && <ReportedIssues />}
            {queue && (
              <section
                className="page"
                aria-label={
                  tab === "Working"
                    ? "Working"
                    : tab === "Inbox"
                      ? "Issues"
                      : tab === "Mine"
                        ? "My PRs"
                        : "Review PRs"
                }
                ref={pageRef}
                tabIndex={-1}
              >
                <h1 className="sr-only">
                  {tab === "Working"
                    ? "Working"
                    : tab === "Inbox"
                      ? "Issues"
                      : tab === "Mine"
                        ? "My PRs"
                        : "Review PRs"}
                </h1>
                <div className="toolbar">
                  <input
                    aria-label="Search inbox"
                    placeholder="Search"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                  />
                  <select
                    aria-label="Inbox filter"
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                  >
                    {[
                      ...(tab === "Inbox" ? ["pending"] : []),
                      "open",
                      "all",
                      "new",
                      "unread",
                      "acted-on",
                    ].map((f) => (
                      <option key={f}>{f}</option>
                    ))}
                  </select>
                </div>
                {snap.repositories
                  .filter((r) => r.error)
                  .map((r) => (
                    <p className="error" role="alert" key={r.id}>
                      {r.id} · {r.error}
                    </p>
                  ))}
                {["Mine", "Review"].includes(tab) && !viewer?.login && (
                  <p role="alert">
                    GitHub identity unavailable. Check the connection in
                    Settings.
                  </p>
                )}
                {tab === "Review" && viewer?.teamError && (
                  <p role="alert">{viewer.teamError}</p>
                )}
                <div className="pagination">
                  <button
                    disabled={offset === 0}
                    onClick={() => setOffset(Math.max(0, offset - 50))}
                  >
                    Previous items
                  </button>
                  <span>{snap.page?.total ?? visibleItems.length} items</span>
                  <button
                    disabled={!snap.page?.hasMore}
                    onClick={() => setOffset(offset + 50)}
                  >
                    Next items
                  </button>
                </div>
                <section className="item-list" aria-label="Items">
                  {!queryReady && <p role="status">Loading saved items…</p>}
                  {visibleItems.map((v) => (
                    <div className="inbox-row" key={v.item.id}>
                      <button
                        className="item"
                        data-focus-key={`item-${v.item.id}`}
                        aria-describedby={`meta-${v.item.id}`}
                        onClick={() => {
                          openWorkItem(v);
                        }}
                      >
                        <b className={`priority p${v.priority}`}>
                          {v.priority === null ? "Pending" : `P${v.priority}`}
                        </b>
                        <span className="item-title">
                          {v.unread && <span className="dot" />}
                          {v.item.title}
                        </span>
                      </button>
                      <div className="item-meta" id={`meta-${v.item.id}`}>
                        {v.item.url ? (
                          <a
                            className="source-link"
                            href={v.item.url}
                            target="_blank"
                            rel="noopener noreferrer"
                            aria-label={`Open ${v.item.kind === "pr" ? "pull request" : "issue"} ${v.item.id} on GitHub`}
                            title="Open on GitHub"
                            onClick={(event) => {
                              event.preventDefault();
                              void openLink(v.item.url)
                                .then((result) => {
                                  if (result.isError)
                                    setError("The host could not open GitHub.");
                                })
                                .catch((e) =>
                                  setError(
                                    `Could not open GitHub: ${String(e)}`,
                                  ),
                                );
                            }}
                          >
                            {v.item.id}
                          </a>
                        ) : (
                          <span>{v.item.id}</span>
                        )}
                        <WorkStatus
                          status={v.work?.status}
                          run={latestRun(snap.runs, v.item.id)}
                          blocked={waiting.some(
                            (a) =>
                              a.blocking &&
                              snap.runs.find((r) => r.id === a.runId)
                                ?.itemId === v.item.id,
                          )}
                        />
                        {v.work && v.work.runIds.length > 1 && (
                          <span>{v.work.runIds.length} sessions</span>
                        )}
                        {v.item.pr && <PRStatus pr={v.item.pr} />}
                        {v.linkedPullRequests?.map((pr) => (
                          <span key={pr.id}>
                            {pr.id} · <PRStatus pr={pr} />
                          </span>
                        ))}
                        {v.item.kind === "pr" && <span>PR</span>}
                        {v.item.state !== "open" && <span>{v.item.state}</span>}
                        {v.item.draft && <span>Draft</span>}
                        {v.user.new && <b className="badge">New</b>}
                        {v.user.actions.length > 0 && <span>Acted on</span>}
                      </div>
                    </div>
                  ))}
                  {queryReady &&
                    (search !== "" || filter !== "open") &&
                    visibleItems.length === 0 && (
                      <div className="empty">
                        <p>No matching items</p>
                        <button
                          onClick={() => {
                            setSearch("");
                            setFilter("all");
                          }}
                        >
                          Clear filters
                        </button>
                      </div>
                    )}
                  {queryReady &&
                    !snap.inbox.length &&
                    search === "" &&
                    filter === "open" && (
                      <div className="empty">
                        {snap.repositories.length > 0 ? (
                          <p>No items yet.</p>
                        ) : (
                          <p>No repositories configured.</p>
                        )}
                      </div>
                    )}
                </section>
              </section>
            )}
            {tab === "Messages" && (
              <section
                className="page item-list"
                aria-label="Messages"
                ref={pageRef}
                tabIndex={-1}
              >
                <h1 className="sr-only">Messages</h1>
                <section aria-label="Waiting for you">
                  {waiting.map((a) => {
                    const run = snap.runs.find((r) => r.id === a.runId);
                    const item = snap.inbox.find(
                      (e) => e.item.id === run?.itemId,
                    );
                    return (
                      <button
                        className="item"
                        key={a.id}
                        onClick={() => openAttention(a.id)}
                      >
                        <span className="item-title">
                          {item?.item.title ?? run?.itemId ?? "Agent"}
                        </span>
                        <span>
                          {run?.itemId} ·{" "}
                          {a.method.includes("Approval")
                            ? "Approval needed"
                            : a.blocking
                              ? "Reply needed"
                              : "Question · agent continues"}
                        </span>
                        {a.params.command && <span>{a.params.command}</span>}
                        {a.params.questions?.[0]?.question && (
                          <span>{a.params.questions[0].question}</span>
                        )}
                      </button>
                    );
                  })}
                  {!waiting.length && (
                    <p className="empty">No messages waiting for you.</p>
                  )}
                </section>
              </section>
            )}
            {tab === "Agent" && (
              <section
                className="page detail"
                aria-label="Agent"
                ref={pageRef}
                tabIndex={-1}
              >
                <button
                  className="back"
                  data-focus-key="back-agent"
                  onClick={() => navigate(runBack)}
                >
                  ←{" "}
                  {runBack === "Inbox"
                    ? "Issues"
                    : runBack === "Mine"
                      ? "My PRs"
                      : runBack === "Review"
                        ? "Review PRs"
                        : runBack === "Working"
                          ? "Working"
                          : "Messages"}
                </button>
                {agentEntry ? (
                  <ItemDetail
                    key={agentEntry.item.id}
                    id={agentEntry.item.id}
                    reviewVersion={
                      (snap.reviewVersions?.[agentEntry.item.id] ?? "") +
                      JSON.stringify(
                        snap.runs
                          .filter((r) => r.itemId === agentEntry.item.id)
                          .map((r) => [r.id, r.status, r.turnId]),
                      )
                    }
                    entry={agentEntry}
                    action={action}
                    onLaunch={setLaunch}
                    workStatus={
                      <WorkStatus
                        status={agentEntry.work?.status}
                        run={
                          currentRun ?? latestRun(snap.runs, agentEntry.item.id)
                        }
                        blocked={waiting.some(
                          (a) => a.blocking && a.runId === runId,
                        )}
                      />
                    }
                    history={
                      <details className="work-history">
                        <summary>Work history</summary>
                        {!snap.runs.some(
                          (r) => r.itemId === agentEntry.item.id,
                        ) && <p>No work history.</p>}
                        {snap.runs
                          .filter(
                            (r) =>
                              r.itemId === agentEntry.item.id &&
                              r.status !== "prepared",
                          )
                          .map((r) => (
                            <button
                              className="item"
                              data-focus-key={`run-${r.id}`}
                              key={r.id}
                              onClick={() => openRun(r.id)}
                            >
                              {r.profile.name} ·{" "}
                              {new Date(r.created).toLocaleString()} ·{" "}
                              {r.status}
                            </button>
                          ))}
                        {snap.runs.some(
                          (r) =>
                            r.itemId === agentEntry.item.id &&
                            r.status === "prepared",
                        ) && (
                          <details>
                            <summary>Draft launches</summary>
                            {snap.runs
                              .filter(
                                (r) =>
                                  r.itemId === agentEntry.item.id &&
                                  r.status === "prepared",
                              )
                              .sort(
                                (a, b) =>
                                  Date.parse(b.created) - Date.parse(a.created),
                              )
                              .map((r) => (
                                <button
                                  className="item"
                                  key={r.id}
                                  onClick={() => setDraftRun(r)}
                                >
                                  {r.profile.name} ·{" "}
                                  {new Date(r.created).toLocaleString()}
                                </button>
                              ))}
                          </details>
                        )}
                      </details>
                    }
                    target={linkDrafts[agentEntry.item.id] ?? ""}
                    setTarget={(value) =>
                      setLinkDrafts((old) => ({
                        ...old,
                        [agentEntry.item.id]:
                          typeof value === "function"
                            ? value(old[agentEntry.item.id] ?? "")
                            : value,
                      }))
                    }
                    conversation={
                      currentRun ? (
                        <Agent
                          consoleView={!!consoleRuns[runId]}
                          setConsoleView={(value) =>
                            setConsoleRuns((old) => ({
                              ...old,
                              [runId]: value,
                            }))
                          }
                          recovery={snap.questionRecoveries?.find(
                            (r) =>
                              r.runId === runId &&
                              [
                                "checking",
                                "clarifying",
                                "requesting-form",
                                "awaiting-answer",
                                "answered",
                              ].includes(r.stage),
                          )}
                          key={runId}
                          embedded
                          run={currentRun}
                          title={
                            snap.inbox.find(
                              (e) => e.item.id === currentRun.itemId,
                            )?.item.title ?? currentRun.itemId
                          }
                          message={messageDrafts[runId] ?? ""}
                          setMessage={(value) =>
                            setMessageDrafts((old) => ({
                              ...old,
                              [runId]:
                                typeof value === "function"
                                  ? value(old[runId] ?? "")
                                  : value,
                            }))
                          }
                          attention={pending.filter((a) => a.runId === runId)}
                          action={action}
                          openAttention={openAttention}
                          onError={setError}
                        />
                      ) : (
                        <NewChat
                          profiles={snap.profiles}
                          entry={agentEntry}
                          draft={
                            chatDrafts[agentEntry.item.id] ?? {
                              message: expandPrompt(undefined, agentEntry),
                            }
                          }
                          setDraft={(update) =>
                            setChatDrafts((old) => ({
                              ...old,
                              [agentEntry.item.id]: update(
                                old[agentEntry.item.id] ?? {
                                  message: expandPrompt(undefined, agentEntry),
                                },
                              ),
                            }))
                          }
                          started={(run) =>
                            acceptRun(run, agentEntry.item.id, runBack)
                          }
                        />
                      )
                    }
                  />
                ) : currentRun ? (
                  <Agent
                    consoleView={!!consoleRuns[runId]}
                    setConsoleView={(value) =>
                      setConsoleRuns((old) => ({ ...old, [runId]: value }))
                    }
                    recovery={snap.questionRecoveries?.find(
                      (r) =>
                        r.runId === runId &&
                        [
                          "checking",
                          "clarifying",
                          "requesting-form",
                          "awaiting-answer",
                          "answered",
                        ].includes(r.stage),
                    )}
                    key={runId}
                    run={currentRun}
                    title={
                      snap.inbox.find((e) => e.item.id === currentRun.itemId)
                        ?.item.title ?? currentRun.itemId
                    }
                    message={messageDrafts[runId] ?? ""}
                    setMessage={(value) =>
                      setMessageDrafts((old) => ({
                        ...old,
                        [runId]:
                          typeof value === "function"
                            ? value(old[runId] ?? "")
                            : value,
                      }))
                    }
                    attention={pending.filter((a) => a.runId === runId)}
                    action={action}
                    openAttention={openAttention}
                    onError={setError}
                  />
                ) : (
                  <p className="empty">Agent unavailable.</p>
                )}
              </section>
            )}
            {settingsView && tab === "Settings" && (
              <section
                className="page"
                aria-label="Settings"
                ref={pageRef}
                tabIndex={-1}
              >
                <h1>Settings</h1>
                <Settings snap={snap} action={action} />
              </section>
            )}
            {(launch || draftRun) && (
              <Launch
                entry={
                  launch ??
                  snap.inbox.find((e) => e.item.id === draftRun?.itemId)
                }
                initialRun={draftRun}
                changed={async () => {
                  await refresh();
                }}
                snap={snap}
                close={() => {
                  setLaunch(undefined);
                  setDraftRun(undefined);
                }}
                started={(run) => {
                  acceptedRuns.current.set(run.id, run);
                  setLaunch(undefined);
                  setDraftRun(undefined);
                  setSnap((old) =>
                    old
                      ? {
                          ...old,
                          runs: [
                            ...old.runs.filter((r) => r.id !== run.id),
                            run,
                          ],
                        }
                      : old,
                  );
                  setSelected(run.itemId);
                  setRunBack(tab === "Agent" ? runBack : "Messages");
                  setRunId(run.id);
                  navigate("Agent");
                  void refresh().catch((e) => setError(String(e)));
                }}
              />
            )}
            {currentAttention && (
              <AttentionDialog
                key={currentAttention.id}
                attention={currentAttention}
                context={`${snap.inbox.find((e) => e.item.id === attentionRun?.itemId)?.item.title ?? attentionRun?.itemId ?? currentAttention.runId} · ${attentionRun?.profile.name ?? "Agent"}`}
                close={() => setAttentionId("")}
                dismiss={
                  currentAttention.origin === "text"
                    ? async () => {
                        await call("dismiss_question", {
                          id: currentAttention.id,
                        });
                        setAttentionId("");
                        await refresh();
                      }
                    : undefined
                }
                answer={async (answer) => {
                  await call("answer_request", {
                    id: currentAttention.id,
                    answer,
                  });
                  openRun(currentAttention.runId);
                  try {
                    await refresh();
                  } catch (e) {
                    setError(String(e));
                  }
                  setAttentionId("");
                }}
                chat={() => {
                  if (currentAttention.origin === "text")
                    setConsoleRuns((old) => ({
                      ...old,
                      [currentAttention.runId]: true,
                    }));
                  openRun(currentAttention.runId);
                  setAttentionId("");
                }}
              />
            )}
          </>
        )}
      </Suspense>
    </main>
  );
}
type Action = (
  name: string,
  args?: Record<string, unknown>,
  failed?: (message: string) => void,
) => Promise<boolean>;
type ChatDraft = {
  profile?: Profile;
  message: string;
  prepared?: Run;
  preparedMessage?: string;
  busy?: boolean;
  uncertain?: boolean;
  error?: string;
};
function expandPrompt(profile: Profile | undefined, entry: Entry) {
  return (profile?.prompt ?? "{{title}}\n\n{{body}}\n\n{{url}}")
    .replaceAll("{{title}}", entry.item.title)
    .replaceAll("{{body}}", entry.item.body)
    .replaceAll("{{url}}", entry.item.url);
}
function PRStatus({ pr }: { pr: PullRequest }) {
  return (
    <span className="pr-status" aria-label="Pull request status">
      {pr.state}
      {pr.draft ? " · Draft" : ""} · {pr.reviewDecision.replaceAll("_", " ")} ·
      CI {pr.ci}
      {pr.error && " · Status unavailable or stale"}
    </span>
  );
}
function PullRequestInfo({
  pr,
  linked = false,
}: {
  pr: PullRequest;
  linked?: boolean;
}) {
  const [error, setError] = useState("");
  return (
    <article className="pr-info">
      {linked && (
        <p>
          <a
            href={pr.url}
            onClick={(event) => {
              event.preventDefault();
              void openLink(pr.url)
                .then((result) => {
                  if (result.isError)
                    setError("The host could not open GitHub.");
                })
                .catch(() => setError("Could not open GitHub."));
            }}
          >
            {pr.id}
          </a>
          {pr.title && ` · ${pr.title}`} ·{" "}
          {pr.implementation ? "Implementation" : "Reference"}
        </p>
      )}
      <PRStatus pr={pr} />
      {pr.error && <p role="alert">{pr.error}</p>}
      {error && <p role="alert">{error}</p>}
      {pr.checks?.length > 0 && (
        <details>
          <summary>CI checks</summary>
          {pr.checks.map((check, index) => (
            <p key={`${check.name}:${index}`}>
              {check.url ? (
                <a
                  href={check.url}
                  onClick={(event) => {
                    event.preventDefault();
                    void openLink(check.url)
                      .then((result) => {
                        if (result.isError)
                          setError("The host could not open the check.");
                      })
                      .catch(() => setError("Could not open the check."));
                  }}
                >
                  {check.name}
                </a>
              ) : (
                check.name
              )}{" "}
              · {check.conclusion || check.status}
            </p>
          ))}
        </details>
      )}
    </article>
  );
}
function NewChat({
  entry,
  draft,
  setDraft,
  started,
  profiles,
}: {
  entry: Entry;
  draft: ChatDraft;
  setDraft: (update: (old: ChatDraft) => ChatDraft) => void;
  started: (run: Run) => void;
  profiles: Profile[];
}) {
  const update = (patch: Partial<ChatDraft>) =>
    setDraft((old) => ({ ...old, ...patch }));
  const profile = draft.profile;
  async function check(run: Run, failure: string) {
    try {
      const data = await call<{ run: Run }>("read_run", { id: run.id });
      if (!data.run || data.run.id !== run.id)
        throw new Error("Run status could not be verified.");
      if (data.run.status !== "prepared") {
        update({ busy: false, uncertain: false, error: "" });
        started(data.run);
      } else
        update({
          busy: false,
          uncertain: false,
          error:
            `${failure} The run is still prepared. You can Start again.`.trim(),
        });
    } catch (e) {
      update({
        busy: false,
        uncertain: true,
        error:
          `${failure} Start status is uncertain. Check status before trying again. ${String(e)}`.trim(),
      });
    }
  }
  async function start() {
    if (draft.busy || draft.uncertain || !profile || !draft.message.trim())
      return;
    const message = draft.message;
    update({ busy: true, error: "" });
    let prepared =
      draft.preparedMessage === message ? draft.prepared : undefined;
    if (!prepared) {
      try {
        prepared = await call<Run>("prepare_launch", {
          itemId: entry.item.id,
          profileId: profile.id,
          prompt: message,
          model: profile.model,
          effort: profile.effort,
          skillIds: profile.skills ?? [],
          base: "",
        });
        update({ prepared, preparedMessage: message });
        const authority = (p: Profile) =>
          JSON.stringify([
            p.id,
            p.name,
            p.mode ?? "default",
            p.sandbox,
            p.approval,
            p.publishing,
          ]);
        if (authority(prepared.profile) !== authority(profile)) {
          update({
            profile: prepared.profile,
            busy: false,
            error:
              "Launch settings changed. Review the updated permissions and mode, then Start agent.",
          });
          return;
        }
      } catch (e) {
        update({ busy: false, error: String(e) });
        return;
      }
    }
    try {
      const run = await call<Run>("start_run", { id: prepared.id });
      update({ busy: false, uncertain: false, error: "" });
      started(run);
    } catch (e) {
      update({ uncertain: true });
      await check(prepared, String(e));
    }
  }
  return (
    <section aria-label="New agent conversation">
      <label>
        Agent pipeline
        <select
          aria-label="Agent pipeline"
          value={profile?.id ?? ""}
          disabled={draft.busy || draft.uncertain}
          onChange={(event) => {
            const next = profiles.find((p) => p.id === event.target.value);
            update({
              profile: next,
              prepared: undefined,
              preparedMessage: undefined,
              error: "",
              message:
                draft.message === expandPrompt(profile, entry)
                  ? expandPrompt(next, entry)
                  : draft.message,
            });
          }}
        >
          <option value="">Select a pipeline</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      {!profile ? (
        <p role="alert">
          {profiles.length
            ? "Select a pipeline before starting."
            : "No launch profile configured. Open Code Factory settings to add one."}
        </p>
      ) : (
        <>
          <p className="launch-authority">
            {profile.name} · {profile.mode ?? "default"} · {profile.sandbox} ·{" "}
            {profile.approval} ·{" "}
            {profile.publishing === "ask"
              ? "Ask before publishing"
              : "Publishing authorized"}
          </p>
          {draft.error && <p role="alert">{draft.error}</p>}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void start();
            }}
          >
            <label>
              Message agent
              <textarea
                rows={4}
                value={draft.message}
                disabled={draft.busy || draft.uncertain}
                onChange={(e) => update({ message: e.target.value })}
              />
            </label>
            <button
              className="primary"
              disabled={draft.busy || draft.uncertain || !draft.message.trim()}
            >
              Start agent
            </button>
            {draft.busy && <span role="status">Starting…</span>}
          </form>
          {draft.uncertain && draft.prepared && (
            <button
              disabled={draft.busy}
              onClick={() => {
                update({ busy: true });
                void check(draft.prepared!, "");
              }}
            >
              Check start status
            </button>
          )}
        </>
      )}
    </section>
  );
}

function ItemDetail({
  id,
  reviewVersion,
  entry,
  action,
  onLaunch,
  target,
  setTarget,
  conversation,
  workStatus,
  history,
}: {
  id: string;
  reviewVersion: string;
  workStatus: React.ReactNode;
  history: React.ReactNode;
  target: string;
  setTarget: React.Dispatch<React.SetStateAction<string>>;
  conversation: React.ReactNode;
  entry?: Entry;
  action: Action;
  onLaunch: (e: Entry) => void;
}) {
  const [detail, setDetail] = useState<Detail>();
  const [loadError, setLoadError] = useState<{
    message: string;
    retrying: boolean;
  }>();
  const titleRef = useRef<HTMLHeadingElement>(null);
  useLayoutEffect(() => {
    if (
      detail?.item.id === id &&
      document.activeElement?.getAttribute("aria-label") === "Agent"
    )
      titleRef.current?.focus({ preventScroll: true });
  }, [id, detail?.item.id]);
  const [priority, setPriority] = useState(
    entry?.user.override?.toString() ?? "",
  );
  const [priorityBusy, setPriorityBusy] = useState(false);
  const [priorityError, setPriorityError] = useState("");
  useEffect(() => {
    setPriority(entry?.user.override?.toString() ?? "");
  }, [entry?.user.override]);
  const [detailVersion, setDetailVersion] = useState(0);
  const [linksOpen, setLinksOpen] = useState(Boolean(target));
  const selectedId = useRef(id);
  selectedId.current = id;
  async function mutate(name: string, args: Record<string, unknown>) {
    const changedId = id;
    const succeeded = await action(name, args);
    if (!succeeded || selectedId.current !== changedId) return false;
    setDetailVersion((version) => version + 1);
    return true;
  }
  const itemVersion = useUIVersion(["item", "user", "observation"], id);
  const linkVersion = useUIVersion(["relationship", "linked-pr", "ticket"]);
  const repositoryVersion = useUIVersion(
    ["repository"],
    entry?.item.repository,
  );
  const linkedRevision = JSON.stringify(
    entry?.linkedPullRequests?.map((pr) => [
      pr.id,
      pr.head,
      pr.state,
      pr.reviewDecision,
      pr.ci,
      pr.error,
      pr.syncedAt,
    ]),
  );
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    let delay = 1000;
    async function load() {
      try {
        const data = await call<Detail>("read_item", { id, saved: true });
        if (alive) {
          setDetail(data);
          setLoadError(undefined);
        }
      } catch (error) {
        if (!alive) return;
        const retrying = isConnectionFailure(error);
        setLoadError({
          message: retrying
            ? "Connection interrupted. Retrying item…"
            : String(error),
          retrying,
        });
        if (retrying) {
          timer = setTimeout(load, delay);
          delay = Math.min(delay * 2, 30000);
        }
      }
    }
    void load();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [
    id,
    entry?.item.revision,
    linkedRevision,
    detailVersion,
    itemVersion,
    linkVersion,
    repositoryVersion,
  ]);
  useEffect(() => {
    if (
      detail?.item.id === id &&
      !detail.refreshError &&
      (!detail.observation || detail.observation.refresh === "fresh")
    )
      void action("item_action", {
        id,
        action: "read",
        revision: detail.item.revision,
      });
  }, [
    detail?.item.id,
    detail?.item.revision,
    detail?.refreshError,
    detail?.observation?.refresh,
    id,
    action,
  ]);
  if (!detail || detail.item.id !== id || !entry)
    return (
      <>
        <p
          role={
            loadError?.retrying ? "status" : loadError ? "alert" : undefined
          }
        >
          {loadError?.message ?? "Loading item…"}
        </p>
        {loadError && !loadError.retrying && (
          <button onClick={() => setDetailVersion((v) => v + 1)}>
            Retry loading item
          </button>
        )}
      </>
    );
  const i = detail.item;
  const summaryParagraph =
    i.body
      .split(/\n\s*\n/)
      .map((paragraph) =>
        paragraph.replace(/^ {0,3}#{1,6}(?:[ \t]+.*)?$/gm, "").trim(),
      )
      .find(Boolean) ?? "";
  const summary = summaryParagraph.slice(0, 600);
  return (
    <>
      {detail.observation?.refresh === "pending" && (
        <p role="status">Refreshing saved details…</p>
      )}
      {(detail.refreshError || detail.observation?.refresh === "cooldown") && (
        <p role={detail.refreshError ? "alert" : "status"}>
          {detail.refreshError ||
            "Refresh deferred; showing saved information."}{" "}
          {detail.observation?.refresh === "cooldown" &&
            detail.observation.retryAt && (
              <>
                Retry after{" "}
                {new Date(detail.observation.retryAt).toLocaleString()}.{" "}
              </>
            )}
          <button onClick={() => setDetailVersion((v) => v + 1)}>
            Retry detail refresh
          </button>
        </p>
      )}
      {loadError && (
        <p role={loadError.retrying ? "status" : "alert"}>
          {loadError.message}
        </p>
      )}
      <h1 ref={titleRef} tabIndex={-1}>
        {i.title}
      </h1>
      <p className="item-meta">
        {i.id} · {i.state}
        {i.kind === "pr" ? " · PR" : ""} · {workStatus}
      </p>
      <section aria-label="Item summary">
        {i.body && (
          <p className="body">
            {summary}
            {summaryParagraph.length > 600 ? "…" : ""}
          </p>
        )}
      </section>
      {(i.pr ?? entry.item.pr) && (
        <section aria-label="PR monitoring">
          <PullRequestInfo pr={(i.pr ?? entry.item.pr)!} />
        </section>
      )}
      {!!(detail.linkedPullRequests ?? entry.linkedPullRequests)?.length && (
        <section aria-label="Linked pull requests">
          <h2>Linked pull requests</h2>
          {(detail.linkedPullRequests ?? entry.linkedPullRequests)!.map(
            (pr) => (
              <PullRequestInfo key={pr.id} pr={pr} linked />
            ),
          )}
        </section>
      )}
      {i.kind === "pr" && (
        <PostedReviews
          itemId={i.id}
          head={i.head}
          version={reviewVersion}
          repository={i.repository}
        />
      )}
      {conversation}
      <details className="item-details">
        <summary>Item details</summary>
        {history}
        {i.body !== summary && (
          <details>
            <summary>Full description</summary>
            <p className="body">{i.body}</p>
          </details>
        )}
        <div className="item-tools">
          <label className="priority-control">
            Priority
            <select
              aria-label="Priority override"
              value={priority}
              disabled={priorityBusy}
              onChange={async (e) => {
                const value = e.target.value;
                const previous = entry.user.override?.toString() ?? "";
                setPriority(value);
                setPriorityBusy(true);
                setPriorityError("");
                const saved = await action(
                  "item_action",
                  {
                    id,
                    action: "priority",
                    priority: value === "" ? null : Number(value),
                  },
                  setPriorityError,
                );
                if (!saved) setPriority(previous);
                setPriorityBusy(false);
              }}
            >
              <option value="">
                Auto ·{" "}
                {entry.priority === null ? "Pending" : `P${entry.priority}`}
              </option>
              {[0, 1, 2, 3].map((p) => (
                <option key={p} value={p}>
                  P{p}
                </option>
              ))}
            </select>
          </label>
          <button onClick={() => onLaunch(entry)}>New run</button>
        </div>
        {priorityBusy && <span role="status">Saving…</span>}
        {priorityError && <p role="alert">{priorityError}</p>}
        {entry.user.override === null && entry.assessment.error && (
          <p role="alert">{entry.assessment.error}</p>
        )}
        <details>
          <summary>Actions</summary>
          <div className="buttons">
            <button
              onClick={() =>
                void mutate("item_action", { id, action: "unread" })
              }
            >
              Mark unread
            </button>
            <button
              onClick={() =>
                void mutate("item_action", { id, action: "acknowledge" })
              }
            >
              Acknowledge new
            </button>
            <button
              onClick={() =>
                void mutate("item_action", { id, action: "acted" })
              }
            >
              Record action
            </button>
          </div>
        </details>
        <details>
          <summary>Priority details</summary>
          <p>
            {entry.prioritySource} ·{" "}
            {entry.assessment.rationale ||
              entry.assessment.error ||
              entry.assessment.status}
          </p>
        </details>
        <details
          open={linksOpen}
          onToggle={(e) => setLinksOpen(e.currentTarget.open)}
        >
          <summary>Relationships</summary>
          {detail.relationships.map((l) => (
            <p key={l.id}>
              {l.target}{" "}
              <small>
                {l.manual ? "Local link" : `Reference: ${l.evidence}`}
              </small>
              {l.manual && (
                <button
                  onClick={() =>
                    void mutate("set_relationship", {
                      source: id,
                      target: l.target,
                      remove: true,
                    })
                  }
                >
                  Remove
                </button>
              )}
            </p>
          ))}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const submitted = target;
              void mutate("set_relationship", { source: id, target }).then(
                (succeeded) => {
                  if (succeeded)
                    setTarget((current) =>
                      current === submitted ? "" : current,
                    );
                },
              );
            }}
          >
            <input
              aria-label="Link target"
              placeholder="owner/repo#123 or JIRA-123"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              required
            />
            <button>Add local link</button>
          </form>
        </details>
        {detail.tickets.map((t) => (
          <p key={t.id}>
            {t.id}: {t.summary || t.error} · {t.status}
          </p>
        ))}
        {i.head && (
          <p>
            Head: <code>{i.head}</code> · {i.branch}
          </p>
        )}
        {Boolean(i.checks) && (
          <details>
            <summary>Checks</summary>
            <pre>{JSON.stringify(i.checks, null, 2)}</pre>
          </details>
        )}
        {i.activity.some((a) => a.kind !== "reference") && (
          <details>
            <summary>Activity</summary>
            {i.activity
              .filter((a) => a.kind !== "reference")
              .map((a, n) => (
                <article key={`${a.id}:${n}`}>
                  <small>
                    {a.kind} · {a.author}
                  </small>
                  <p className="body">{a.body}</p>
                </article>
              ))}
          </details>
        )}
        {detail.user.actions.length > 0 && (
          <details>
            <summary>Your action history</summary>
            {detail.user.actions.map((a) => (
              <p key={a.id}>
                {a.kind} · {new Date(a.at).toLocaleString()}
              </p>
            ))}
          </details>
        )}
      </details>
    </>
  );
}
function Launch({
  entry,
  snap,
  close,
  started,
  initialRun,
  changed,
}: {
  entry?: Entry;
  initialRun?: Run;
  changed: () => Promise<void>;
  snap: Snapshot;
  close: () => void;
  started: (run: Run) => void;
}) {
  const [profileId, setProfileId] = useState(
    initialRun?.profile.id ??
      (entry?.item.kind === "pr"
        ? snap.repositories.find((r) => r.id === entry.item.repository)
            ?.reviewProfileId ||
          snap.config.reviewProfileId ||
          "review"
        : snap.repositories.find((r) => r.id === entry?.item.repository)
            ?.implementationProfileId ||
          snap.config.implementationProfileId ||
          "implement"),
  );
  const p =
    snap.profiles.find((p) => p.id === profileId) ?? initialRun?.profile;
  const expand = (p: Profile) =>
    p.prompt
      .replaceAll("{{title}}", entry?.item.title ?? "")
      .replaceAll("{{body}}", entry?.item.body ?? "")
      .replaceAll("{{url}}", entry?.item.url ?? "");
  const [prompt, setPrompt] = useState(
    initialRun?.prompt ?? (p ? expand(p) : ""),
  );
  const [model, setModel] = useState(p?.model ?? "");
  const [effort, setEffort] = useState(p?.effort ?? "");
  const [skillIds, setSkills] = useState<string[]>(
    initialRun?.skills.map((s) => s.id) ?? p?.skills ?? [],
  );
  const [base, setBase] = useState(initialRun?.base ?? "");
  const [prepared, setPrepared] = useState<Run | undefined>(initialRun);
  const [editing, setEditing] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const initialized = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useModalFocus(close);
  async function prepare() {
    setBusy(true);
    setError("");
    try {
      const run = await call<Run>("prepare_launch", {
        itemId: entry?.item.id ?? initialRun?.itemId,
        profileId,
        prompt,
        model,
        effort,
        skillIds,
        base,
      });
      setPrepared(run);
      setEditing(false);
      void changed().catch(() => {});
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    if (!initialized.current) {
      initialized.current = true;
      if (!initialRun) void prepare();
    }
  }, []);
  async function checkStart(id: string, failure = "") {
    setBusy(true);
    try {
      const { run } = await call<{ run: Run; attention: Attention[] }>(
        "read_run",
        { id },
      );
      if (!run || run.id !== id)
        throw new Error("Run status could not be verified.");
      if (run.status !== "prepared") {
        started(run);
        return;
      }
      setUncertain(false);
      setError(
        `${failure} The run is still prepared. You can Start again.`.trim(),
      );
    } catch (e) {
      setUncertain(true);
      setError(
        `${failure} Start status is uncertain. Check status before trying again. ${String(e)}`.trim(),
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="overlay">
      <section
        ref={dialogRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label="Launch agent"
        className="modal"
      >
        <button className="close" onClick={close} aria-label="Close launch">
          ×
        </button>
        <h2>
          {editing
            ? "Edit agent launch"
            : prepared
              ? "Review agent launch"
              : "Launch agent"}
        </h2>
        {error && <p role="alert">{error}</p>}
        <p>
          {entry?.item.id ?? initialRun?.itemId} · {entry?.item.title}
        </p>
        {!prepared && !editing ? (
          <p role="status">
            {busy ? "Preparing review…" : "Unable to prepare launch."}
            {!busy && (
              <>
                <button onClick={() => void prepare()}>
                  Retry preparation
                </button>
                <button onClick={() => setEditing(true)}>Edit</button>
              </>
            )}
          </p>
        ) : editing ? (
          <>
            <label>
              Launch profile
              <select
                value={profileId}
                onChange={(e) => {
                  const next = snap.profiles.find(
                    (p) => p.id === e.target.value,
                  )!;
                  setProfileId(next.id);
                  setPrompt(expand(next));
                  setModel(next.model);
                  setEffort(next.effort);
                  setSkills(next.skills ?? []);
                }}
              >
                {snap.profiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Task prompt
              <textarea
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                rows={8}
              />
            </label>
            <details>
              <summary>Advanced</summary>
              <div className="fields">
                <label>
                  Model
                  <input
                    value={model}
                    onChange={(e) => setModel(e.target.value)}
                    placeholder="Use local default"
                  />
                </label>
                <label>
                  Reasoning effort
                  <select
                    value={effort}
                    onChange={(e) => setEffort(e.target.value)}
                  >
                    {["", "low", "medium", "high", "xhigh", "max", "ultra"].map(
                      (v) => (
                        <option key={v} value={v}>
                          {v || "Local default"}
                        </option>
                      ),
                    )}
                  </select>
                </label>
              </div>
              <label>
                Base reference
                <input
                  value={base}
                  onChange={(e) => setBase(e.target.value)}
                  placeholder={
                    entry?.item.kind === "pr" ? "PR head" : "Repository default"
                  }
                />
              </label>
              <fieldset>
                <legend>Skills</legend>
                {snap.skills.map((s) => (
                  <label className="check" key={s.id}>
                    <input
                      type="checkbox"
                      checked={skillIds.includes(s.id)}
                      onChange={(e) =>
                        setSkills(
                          e.target.checked
                            ? [...skillIds, s.id]
                            : skillIds.filter((v) => v !== s.id),
                        )
                      }
                    />
                    {s.name}
                  </label>
                ))}
              </fieldset>
            </details>
            <p>
              Mode: {p?.mode ?? "default"} · Permissions: {p?.sandbox} ·{" "}
              {p?.approval}
            </p>
            <p>
              Publishing:{" "}
              {p?.publishing === "ask"
                ? "Requires your decision"
                : "Authorized by profile"}
            </p>
            <button
              className="primary"
              disabled={busy}
              onClick={() => void prepare()}
            >
              Review launch
            </button>
          </>
        ) : (
          prepared && (
            <>
              <dl>
                <dt>Profile</dt>
                <dd>{prepared.profile.name}</dd>
                <dt>Agent mode</dt>
                <dd>{prepared.profile.mode ?? "default"}</dd>
                <dt>Workspace</dt>
                <dd>{prepared.worktree}</dd>
                <dt>Branch / base</dt>
                <dd>
                  {prepared.branch} / {prepared.base}
                </dd>
                <dt>Permissions</dt>
                <dd>
                  {prepared.profile.sandbox} · {prepared.profile.approval}
                </dd>
                <dt>Publishing</dt>
                <dd>
                  {prepared.profile.publishing === "ask"
                    ? "Ask before publishing"
                    : "Authorized"}
                </dd>
                <dt>Skills</dt>
                <dd>
                  {prepared.skills.map((s) => s.name).join(", ") ||
                    "None selected"}
                </dd>
              </dl>
              <pre>{prepared.prompt}</pre>
              <div className="buttons">
                <button
                  disabled={busy || uncertain}
                  onClick={() => {
                    setEditing(true);
                    setError("");
                  }}
                >
                  Edit
                </button>
                <button
                  className="primary"
                  disabled={busy || uncertain}
                  onClick={async () => {
                    setBusy(true);
                    setError("");
                    try {
                      const r = await call<Run>("start_run", {
                        id: prepared.id,
                      });
                      started(r);
                    } catch (e) {
                      setUncertain(true);
                      await checkStart(prepared.id, String(e));
                    }
                  }}
                >
                  Start agent
                </button>
                {uncertain && (
                  <button
                    disabled={busy}
                    onClick={() => void checkStart(prepared.id)}
                  >
                    Check start status
                  </button>
                )}
              </div>
            </>
          )
        )}
      </section>
    </div>
  );
}
// Older journals split a provider's JSON params into contiguous chunks without
// an end marker. Frame once, then decode once, including across fetched pages.
function transcriptEvents(events: JournalEvent[]) {
  const decoded: JournalEvent[] = [];
  let pending: JournalEvent[] = [];
  let depth = 0,
    quoted = false,
    escaped = false;
  const flush = () => {
    decoded.push(...pending);
    pending = [];
    depth = 0;
    quoted = escaped = false;
  };
  for (const event of events) {
    const method = event.data.method ?? "";
    if (!method.endsWith("/chunk")) {
      flush();
      decoded.push(event);
      continue;
    }
    if (
      pending.length &&
      (pending[0].data.method !== method || pending[0].entity !== event.entity)
    )
      flush();
    pending.push(event);
    for (const char of String(event.data.params?.delta ?? "")) {
      if (quoted) {
        if (escaped) escaped = false;
        else if (char === "\\") escaped = true;
        else if (char === '"') quoted = false;
      } else if (char === '"') quoted = true;
      else if (char === "{" || char === "[") depth++;
      else if (char === "}" || char === "]") depth--;
    }
    if (depth === 0 && !quoted) {
      try {
        const params: unknown = JSON.parse(
          pending.map((part) => part.data.params?.delta ?? "").join(""),
        );
        if (!params || typeof params !== "object" || Array.isArray(params))
          throw new Error("Invalid provider params");
        decoded.push({
          ...pending[0],
          data: {
            method: method.slice(0, -6),
            params: params as Record<string, unknown>,
          },
        });
        pending = [];
      } catch {
        flush();
      }
    } else if (depth < 0) flush();
  }
  flush();
  return decoded;
}
function transcriptBlocks(events: JournalEvent[]) {
  const blocks: {
    key: number;
    id: string;
    label: string;
    text: string;
    details?: string;
  }[] = [];
  for (const e of transcriptEvents(events)) {
    const p = e.data.params ?? {};
    const m = e.data.method ?? "";
    const item = p.item as
      | {
          id?: string;
          type?: string;
          text?: string;
          command?: string;
          aggregatedOutput?: string;
          changes?: { path?: string; diff?: string }[];
          content?: { text?: string }[];
        }
      | undefined;
    let label = "";
    let text = "";
    let details: string | undefined;
    if (m === "codefactory/messageDelivery") {
      label = "Message delivery";
      text =
        p.state === "queued"
          ? "Message saved. Awaiting agent delivery."
          : p.state === "delivering"
            ? "Message saved. Delivering to agent…"
            : p.state === "delivered"
              ? "Message delivered to agent."
              : String(
                  p.error ??
                    "Agent delivery is uncertain. Check this session before sending again.",
                );
    } else if (m.includes("agentMessage/delta")) {
      label = "Agent";
      text = String(p.delta ?? "");
    } else if (m.includes("commandExecution/outputDelta")) {
      label = "Command output";
      text = String(p.delta ?? "");
    } else if (m.includes("reasoning/summaryTextDelta")) {
      label = "Summary";
      text = String(p.delta ?? "");
    } else if (m === "turn/plan/updated") {
      label = "Plan";
      text = ((p.plan as { step: string; status: string }[]) ?? [])
        .map((v) => `${v.status}: ${v.step}`)
        .join("\n");
    } else if (m.includes("diff/updated")) {
      label = "Changes";
      text = String(p.diff ?? p.delta ?? "");
    } else if (item?.type === "agentMessage") {
      label = "Agent";
      text = item.text ?? "";
    } else if (item?.type === "userMessage") {
      label =
        p.codeFactorySource === "question-recovery"
          ? "Question recovery"
          : "You";
      text = (item.content ?? []).map((v) => v.text ?? "").join("\n");
    } else if (item?.type === "commandExecution") {
      label = "Command";
      text = [item.command, item.aggregatedOutput].filter(Boolean).join("\n");
    } else if (item?.type === "fileChange") {
      label = "File changes";
      text = (item.changes ?? [])
        .map((v) => `${v.path}\n${v.diff ?? ""}`)
        .join("\n");
    } else if (m.endsWith("/chunk")) {
      label = "Output";
      text = String(p.delta ?? "");
    } else if (m === "error") {
      label = "Run error";
      const error = p.error ?? p;
      text =
        typeof error === "string"
          ? error
          : typeof error === "object" &&
              error !== null &&
              "message" in error &&
              typeof error.message === "string"
            ? error.message
            : "The agent reported an error.";
      if (typeof error !== "string") details = JSON.stringify(error, null, 2);
    }
    if (!label || !text) continue;
    const id = String(p.itemId ?? item?.id ?? "");
    const existing = id
      ? blocks.find((b) => b.id === id && b.label === label)
      : undefined;
    if (existing) {
      if (m.endsWith("/delta") || m.endsWith("Delta")) existing.text += text;
      else existing.text = text;
      if (details) existing.details = details;
    } else blocks.push({ key: e.seq, id, label, text, details });
  }
  return blocks;
}

function Agent({
  run,
  attention,
  action,
  openAttention,
  onError,
  title,
  message,
  setMessage,
  embedded = false,
  consoleView,
  setConsoleView,
  recovery,
}: {
  embedded?: boolean;
  consoleView: boolean;
  setConsoleView: (value: boolean) => void;
  recovery?: QuestionRecovery;
  run: Run;
  title: string;
  message: string;
  setMessage: React.Dispatch<React.SetStateAction<string>>;
  attention: Attention[];
  action: Action;
  openAttention: (id: string) => void;
  onError: (e: string) => void;
}) {
  const [events, setEvents] = useState<JournalEvent[]>([]);
  const [historyError, setHistoryError] = useState<{
    message: string;
    retrying: boolean;
  }>();
  const [cursor, setCursor] = useState(0);
  const [hasMore, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const pageCursor = useRef(0);
  const [historyVersion, setHistoryVersion] = useState(0);
  const runVersion = useUIVersion(["run", "transcript"], run.id);
  const resyncVersion = useUIResyncVersion();
  const previousResync = useRef(resyncVersion);
  const [browsingHistory, setBrowsingHistory] = useState(false);
  const transcriptVersion = browsingHistory ? 0 : runVersion;
  const deferred = run.status === "deferred";
  const canMessage =
    !!run.threadId &&
    ["idle", "running", "waiting", "interrupted", "failed"].includes(
      run.status,
    );
  const requestHistory = useRef<() => void>(() => {});
  const outputSeen = useRef(transcriptVersion);
  useEffect(() => {
    if (previousResync.current !== resyncVersion) {
      previousResync.current = resyncVersion;
      pageCursor.current = 0;
      setEvents([]);
      setCursor(0);
      setMore(false);
    }
    let stopped = false;
    let inFlight = false;
    let pending = false;
    let retrying = false;
    let timer: ReturnType<typeof setTimeout>;
    let delay = 1000;
    async function load() {
      pending = true;
      if (stopped || document.hidden || inFlight || retrying) return;
      inFlight = true;
      try {
        while (!stopped && pending && !document.hidden) {
          pending = false;
          const p = await call<EventPage>("read_transcript", {
            id: run.id,
            after: pageCursor.current,
            limit: 100,
          });
          if (stopped) return;
          setEvents((old) => [...old, ...p.events].slice(-1000));
          pageCursor.current = p.cursor;
          setCursor(p.cursor);
          setMore(p.hasMore);
          setHistoryError(undefined);
          delay = 1000;
          if (p.hasMore && !browsingHistory) pending = true;
        }
      } catch (error) {
        if (!stopped) {
          setHistoryError({
            message: isConnectionFailure(error)
              ? "Connection interrupted. Retrying conversation…"
              : String(error),
            retrying: isConnectionFailure(error),
          });
          retrying = true;
          timer = setTimeout(() => {
            retrying = false;
            void load();
          }, delay);
          delay = Math.min(delay * 2, 30000);
        }
      } finally {
        inFlight = false;
      }
    }
    const visible = () => {
      if (!document.hidden) void load();
    };
    requestHistory.current = () => void load();
    outputSeen.current = transcriptVersion;
    document.addEventListener("visibilitychange", visible);
    void load();
    return () => {
      stopped = true;
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", visible);
      requestHistory.current = () => {};
    };
  }, [run.id, historyVersion, resyncVersion, browsingHistory]);
  useEffect(() => {
    if (outputSeen.current !== transcriptVersion) {
      outputSeen.current = transcriptVersion;
      requestHistory.current();
    }
  }, [transcriptVersion]);
  const blocks = transcriptBlocks(events);
  const conversation = blocks.filter((b) =>
    ["Agent", "You", "Run error", "Message delivery"].includes(b.label),
  );
  const activity = blocks.flatMap((b) =>
    b.details
      ? [{ ...b, label: "Error details", text: b.details }]
      : ["Agent", "You", "Run error", "Message delivery"].includes(b.label)
        ? []
        : [b],
  );
  return (
    <>
      {!embedded && (
        <>
          <h1>{title}</h1>
          <WorkStatus run={run} blocked={attention.some((a) => a.blocking)} />
        </>
      )}
      {deferred ? (
        <section aria-label="GitHub retry">
          <p role="status">Waiting for GitHub</p>
          {run.error && <p>{run.error}</p>}
          {run.launchRetryAt && (
            <p>
              Next retry:{" "}
              <time dateTime={run.launchRetryAt}>
                {new Date(run.launchRetryAt).toLocaleString()}
              </time>
            </p>
          )}
          <button onClick={() => void action("interrupt_run", { id: run.id })}>
            Cancel retry
          </button>
        </section>
      ) : run.error ? (
        <p role="alert">{run.error}</p>
      ) : null}
      {embedded && (
        <p aria-label="Session status">
          {run.profile.name} · {deferred ? "Waiting for GitHub" : run.status}
        </p>
      )}
      <div className="buttons">
        <button onClick={() => setConsoleView(!consoleView)}>
          {consoleView ? "View conversation" : "View session console"}
        </button>
        {consoleView && run.turnId && (
          <button onClick={() => void action("interrupt_run", { id: run.id })}>
            Interrupt session
          </button>
        )}
      </div>
      {recovery &&
        [
          "checking",
          "clarifying",
          "requesting-form",
          "awaiting-answer",
          "answered",
        ].includes(recovery.stage) && (
          <section aria-label="Question recovery">
            <p role="status">
              {recovery.stage === "checking"
                ? "Checking whether the agent needs input…"
                : recovery.stage === "clarifying"
                  ? `Clarifying whether input is needed · attempt ${recovery.attempts} of 2`
                  : recovery.stage === "awaiting-answer"
                    ? "Question ready for your answer."
                    : recovery.stage === "answered"
                      ? "Returning to the selected task mode…"
                      : `Preparing a question · attempt ${recovery.attempts} of 2`}
            </p>
            <button
              onClick={() => {
                setConsoleView(true);
                void action("interrupt_run", { id: run.id });
              }}
            >
              Take over session
            </button>
          </section>
        )}
      {attention.map((a) => (
        <button
          className="notice"
          key={a.id}
          onClick={() => openAttention(a.id)}
        >
          {a.blocking
            ? "Waiting for your answer"
            : "Question · agent continues"}{" "}
          — Answer
        </button>
      ))}
      {historyError && (
        <p role={historyError.retrying ? "status" : "alert"}>
          {historyError.message}
        </p>
      )}
      <div
        className={`transcript ${consoleView ? "session-console" : ""}`}
        aria-label={consoleView ? "Session console" : "Conversation"}
        aria-live="polite"
      >
        {(consoleView ? blocks : conversation).map((b) => (
          <article key={b.key}>
            <small>{b.label === "Run error" ? "Agent error" : b.label}</small>
            {consoleView ? (
              <pre>{b.text}</pre>
            ) : (
              <p className="body">{b.text}</p>
            )}
          </article>
        ))}
        {!conversation.length && (
          <p>
            {["running", "waiting", "starting", "queued"].includes(run.status)
              ? "Waiting for an agent update…"
              : "No agent messages yet."}
          </p>
        )}
      </div>
      {hasMore &&
        (browsingHistory ? (
          <button onClick={() => setHistoryVersion((v) => v + 1)}>
            Load next history page
          </button>
        ) : (
          <p>Loading more history after event {cursor}…</p>
        ))}
      {browsingHistory && (
        <button onClick={() => setBrowsingHistory(false)}>
          Follow latest history
        </button>
      )}
      {events.length >= 1000 && (
        <button
          onClick={() => {
            setBrowsingHistory(true);
            pageCursor.current = 0;
            setEvents([]);
            setCursor(0);
            setHistoryVersion((v) => v + 1);
          }}
        >
          View history from the beginning
        </button>
      )}
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          if (!canMessage || busy || !message.trim()) return;
          setBusy(true);
          const submitted = message;
          try {
            const sent = await action("send_message", {
              id: run.id,
              text: message,
              expectedTurn: ["running", "waiting"].includes(run.status)
                ? run.turnId
                : "",
            });
            if (!sent) return;
            setMessage((current) => (current === submitted ? "" : current));
          } catch (e) {
            onError(String(e));
          } finally {
            setBusy(false);
          }
        }}
      >
        <label>
          Message agent
          <textarea
            value={message}
            disabled={!run.threadId || deferred}
            onChange={(e) => setMessage(e.target.value)}
            rows={3}
          />
        </label>
        <button
          className="primary"
          disabled={
            busy ||
            !message.trim() ||
            !canMessage ||
            (!!recovery &&
              [
                "checking",
                "clarifying",
                "requesting-form",
                "awaiting-answer",
                "answered",
              ].includes(recovery.stage))
          }
        >
          Send message
        </button>
      </form>
      <details className="agent-details">
        <summary>Details</summary>
        <p>{run.profile.name}</p>
        {run.turnId && ["running", "waiting"].includes(run.status) && (
          <button onClick={() => void action("interrupt_run", { id: run.id })}>
            Interrupt
          </button>
        )}
        {!consoleView && activity.length > 0 && (
          <section aria-label="Technical activity">
            {activity.map((b) => (
              <article key={b.key}>
                <small>{b.label}</small>
                <pre>{b.text}</pre>
              </article>
            ))}
          </section>
        )}
        <p>
          Workspace: <code>{run.worktree}</code>
        </p>
        {["idle", "interrupted", "failed", "terminated"].includes(
          run.status,
        ) && (
          <>
            <p>
              Cleanup retires the retained session and removes a clean worktree.
              Conversation history stays available; modified files prevent
              removal.
            </p>
            <button onClick={() => void action("cleanup_run", { id: run.id })}>
              Clean up worktree
            </button>
          </>
        )}
      </details>
    </>
  );
}
function AttentionDialog({
  attention: a,
  context,
  close,
  answer,
  chat,
  dismiss,
}: {
  attention: Attention;
  context: string;
  close: () => void;
  answer: (value: unknown) => Promise<void>;
  chat: () => void;
  dismiss?: () => Promise<void>;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const dialogRef = useModalFocus(close);
  async function submit(v: unknown) {
    setBusy(true);
    try {
      await answer(v);
    } catch (e) {
      setError(String(e));
      setBusy(false);
    }
  }
  const questions = a.params.questions;
  return (
    <div className="overlay">
      <section
        ref={dialogRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label="Agent needs attention"
        className="modal"
      >
        <button className="close" aria-label="Close attention" onClick={close}>
          ×
        </button>
        <h2>Agent needs attention</h2>
        <p>{context}</p>
        <p>
          {a.origin === "text"
            ? "The agent stopped without a usable question form. Review its reply, answer here, or choose No reply needed."
            : a.blocking
              ? "The agent is waiting for your response."
              : "The agent continues while you answer."}
        </p>
        {error && <p role="alert">{error}</p>}
        {questions ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const answers = Object.fromEntries(
                questions.map((q) => [q.id, { answers: [values[q.id] ?? ""] }]),
              );
              void submit({ answers });
            }}
          >
            {questions.map((q) => (
              <fieldset key={q.id}>
                <legend>{q.question}</legend>
                {q.options?.map((o) => (
                  <label className="check" key={o.label}>
                    <input
                      type="radio"
                      name={q.id}
                      value={o.label}
                      checked={values[q.id] === o.label}
                      onChange={() => setValues({ ...values, [q.id]: o.label })}
                    />
                    <span>
                      {o.label}
                      <small>{o.description}</small>
                    </span>
                  </label>
                ))}
                {(!q.options?.length || q.isOther) && (
                  <label>
                    Answer
                    <input
                      type={q.isSecret ? "password" : "text"}
                      aria-label={q.question}
                      value={
                        q.options?.some((o) => o.label === values[q.id])
                          ? ""
                          : (values[q.id] ?? "")
                      }
                      onChange={(e) =>
                        setValues({ ...values, [q.id]: e.target.value })
                      }
                    />
                  </label>
                )}
              </fieldset>
            ))}
            <button
              className="primary"
              disabled={busy || questions.some((q) => !values[q.id])}
            >
              Answer
            </button>
          </form>
        ) : (
          <>
            <ApprovalDetails params={a.params} />
            <details>
              <summary>Technical details</summary>
              <pre>{JSON.stringify(a.params, null, 2)}</pre>
            </details>
            <div className="buttons">
              {a.method.includes("permissions") ? (
                <>
                  <button
                    disabled={busy}
                    onClick={() =>
                      void submit({ permissions: {}, scope: "turn" })
                    }
                  >
                    Deny
                  </button>
                  <button
                    disabled={busy}
                    onClick={() =>
                      void submit({
                        permissions: a.params.permissions,
                        scope: "turn",
                      })
                    }
                  >
                    Allow once
                  </button>
                </>
              ) : a.method.includes("elicitation") ? (
                <Elicitation attention={a} busy={busy} submit={submit} />
              ) : (
                (
                  a.params.availableDecisions ?? ["accept", "decline", "cancel"]
                ).map((d, n) => (
                  <button
                    disabled={busy}
                    key={n}
                    onClick={() => void submit({ decision: d })}
                  >
                    {decisionLabel(d)}
                  </button>
                ))
              )}
            </div>
          </>
        )}
        <button onClick={chat}>
          {a.origin === "text" ? "Open session console" : "Open agent chat"}
        </button>
        {dismiss && (
          <button
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await dismiss();
              } catch (e) {
                setError(String(e));
                setBusy(false);
              }
            }}
          >
            No reply needed
          </button>
        )}
      </section>
    </div>
  );
}
function decisionLabel(decision: unknown): string {
  const labels: Record<string, string> = {
    accept: "Allow once",
    acceptForSession: "Allow for this session",
    decline: "Deny",
    cancel: "Cancel",
  };
  if (typeof decision === "string") return labels[decision] ?? decision;
  if (decision && typeof decision === "object") {
    if ("acceptWithExecpolicyAmendment" in decision)
      return "Allow and update command policy";
    if ("applyNetworkPolicyAmendment" in decision)
      return "Apply network access decision";
  }
  return "Apply requested decision";
}
function ApprovalDetails({ params }: { params: Attention["params"] }) {
  const readable = (value: unknown): React.ReactNode => {
    if (Array.isArray(value))
      return value.map((v, i) => <div key={i}>{readable(v)}</div>);
    if (value && typeof value === "object")
      return (
        <dl>
          {Object.entries(value).map(([key, v]) => (
            <React.Fragment key={key}>
              <dt>
                {key.replace(/([a-z])([A-Z])/g, "$1 $2").replaceAll("_", " ")}
              </dt>
              <dd>{readable(v)}</dd>
            </React.Fragment>
          ))}
        </dl>
      );
    return String(value);
  };
  return (
    <dl>
      {[
        ["Command", params.command],
        ["Reason", params.reason],
        ["Working directory", params.cwd],
        [
          "Requested access",
          params.permissions ??
            params.additionalPermissions ??
            params.networkApprovalContext ??
            params.grantRoot,
        ],
        ["File changes", params.changes],
      ]
        .filter(([, value]) => value != null)
        .map(([label, value]) => (
          <React.Fragment key={String(label)}>
            <dt>{String(label)}</dt>
            <dd>{readable(value)}</dd>
          </React.Fragment>
        ))}
    </dl>
  );
}
createRoot(document.getElementById("root")!).render(<Factory />);

interface FormField {
  type?: string;
  title?: string;
  description?: string;
  enum?: string[];
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  format?: string;
  items?: FormField;
}
function Elicitation({
  attention: a,
  busy,
  submit,
}: {
  attention: Attention;
  busy: boolean;
  submit: (v: unknown) => Promise<void>;
}) {
  const [content, setContent] = useState<Record<string, unknown>>({});
  const schema = a.params.requestedSchema as
    | { properties?: Record<string, FormField>; required?: string[] }
    | undefined;
  const mode = String(a.params.mode ?? "form");
  return (
    <div>
      <p>{String(a.params.message ?? "")}</p>
      {mode === "url" ? (
        <>
          <button onClick={() => void openLink(String(a.params.url))}>
            Open authorization page
          </button>
          <button
            disabled={busy}
            onClick={() => void submit({ action: "accept" })}
          >
            Continue after authorization
          </button>
        </>
      ) : schema?.properties ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void submit({ action: "accept", content });
          }}
        >
          {Object.entries(schema.properties).map(([key, f]) => (
            <label key={key}>
              {f.title ?? key}
              {f.description && <small>{f.description}</small>}
              {f.type === "boolean" ? (
                <input
                  type="checkbox"
                  checked={content[key] === true}
                  onChange={(e) =>
                    setContent({ ...content, [key]: e.target.checked })
                  }
                />
              ) : f.enum ? (
                <select
                  required={schema.required?.includes(key)}
                  value={String(content[key] ?? "")}
                  onChange={(e) =>
                    setContent({ ...content, [key]: e.target.value })
                  }
                >
                  <option value="">Choose…</option>
                  {f.enum.map((v) => (
                    <option key={v}>{v}</option>
                  ))}
                </select>
              ) : f.type === "array" && f.items?.enum ? (
                <select
                  multiple
                  value={(content[key] ?? []) as string[]}
                  onChange={(e) =>
                    setContent({
                      ...content,
                      [key]: [...e.target.selectedOptions].map((o) => o.value),
                    })
                  }
                >
                  {f.items.enum.map((v) => (
                    <option key={v}>{v}</option>
                  ))}
                </select>
              ) : (
                <input
                  type={
                    f.type === "integer" || f.type === "number"
                      ? "number"
                      : f.format === "email"
                        ? "email"
                        : "text"
                  }
                  required={schema.required?.includes(key)}
                  min={f.minimum}
                  max={f.maximum}
                  step={f.type === "integer" ? 1 : "any"}
                  minLength={f.minLength}
                  maxLength={f.maxLength}
                  value={String(content[key] ?? "")}
                  onChange={(e) =>
                    setContent({
                      ...content,
                      [key]:
                        f.type === "integer" || f.type === "number"
                          ? Number(e.target.value)
                          : e.target.value,
                    })
                  }
                />
              )}
            </label>
          ))}
          <button disabled={busy}>Submit form</button>
        </form>
      ) : (
        <p>This verification request requires native host support.</p>
      )}
      <button
        disabled={busy}
        onClick={() => void submit({ action: "decline" })}
      >
        Decline
      </button>
      <button disabled={busy} onClick={() => void submit({ action: "cancel" })}>
        Cancel
      </button>
    </div>
  );
}
