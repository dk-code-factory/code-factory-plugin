import React, {
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
import { ReportedIssues } from "./reports";
import { PluginAdmin, RepositoryAdmin, SystemReviewSettings } from "./admin";
import { PostedReviews } from "./reviews";

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
  const [tab, setTab] = useState(settingsView ? "Settings" : "Inbox");
  const [runBack, setRunBack] = useState("Messages");
  const pageRef = useRef<HTMLElement>(null);
  const positions = useRef(
    new Map<string, { scroll: number; focus?: string }>(),
  );
  const [error, setError] = useState("");
  const [pollError, setPollError] = useState("");
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
  >({});
  const filter = queries[tab]?.filter ?? "open";
  const search = queries[tab]?.search ?? "";
  const setFilter = (filter: string) =>
    setQueries((old) => ({
      ...old,
      [tab]: { search: old[tab]?.search ?? "", filter },
    }));
  const setSearch = (search: string) =>
    setQueries((old) => ({
      ...old,
      [tab]: {
        filter: old[tab]?.filter ?? "open",
        search,
      },
    }));
  const [chatDrafts, setChatDrafts] = useState<Record<string, ChatDraft>>({});
  const activeView = useRef({ tab, selected });
  activeView.current = { tab, selected };
  const cursor = useRef(0);
  const mounted = useRef(true);
  const refresh = useCallback(async () => {
    const s = await call<Snapshot>(
      settingsView ? "open_settings" : "read_snapshot",
    );
    if (mounted.current && s.cursor >= cursor.current) {
      setSnap(s);
      cursor.current = s.cursor;
      setConnection("Connected");
      setPollError("");
    }
  }, [settingsView]);
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
      // Failed mutations can still change durable state, such as retiring a
      // session before dirty cleanup is refused. Keep that state visible.
      try {
        await refresh();
      } catch (e) {
        setPollError(
          isConnectionFailure(e)
            ? "Connection interrupted. Retrying automatically."
            : String(e),
        );
      }
      return succeeded;
    },
    [refresh],
  );
  useEffect(() => {
    mounted.current = true;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    let backoff = 1000;
    let inFlight = false;
    let failures = 0;
    async function poll() {
      if (stopped || inFlight) return;
      inFlight = true;
      try {
        if (!snapRef.current) {
          await refresh();
          snapRef.current = true;
        } else {
          const p = await call<EventPage>("read_changes", {
            after: cursor.current,
            limit: 200,
          });
          if (p.events.length) {
            await refresh();
          } else {
            cursor.current = Math.max(cursor.current, p.cursor);
          }
          setConnection("Connected");
        }
        setPollError("");
        failures = 0;
        backoff = 1000;
      } catch (e) {
        if (!stopped) {
          failures++;
          setConnection("Reconnecting…");
          if (!isConnectionFailure(e) || failures >= 3)
            setPollError(
              isConnectionFailure(e)
                ? "Connection interrupted. Retrying automatically."
                : String(e),
            );
          backoff = Math.min(backoff * 2, 30000);
        }
      }
      inFlight = false;
      if (!stopped) timer = setTimeout(poll, document.hidden ? 15000 : backoff);
    }
    const snapRef = { current: false };
    // The poll loop owns initialization and retries it after a failed handshake.
    void poll();
    const visible = () => {
      if (!document.hidden) {
        clearTimeout(timer);
        void poll();
      }
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      stopped = true;
      mounted.current = false;
      clearTimeout(timer);
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
  const visibleItems = (
    snap?.inbox.filter((v) => {
      const own =
        viewer?.login &&
        v.item.author.toLowerCase() === viewer.login.toLowerCase();
      const requested =
        viewer?.login &&
        (v.item.reviewRequests?.some(
          (login) => login.toLowerCase() === viewer.login.toLowerCase(),
        ) ||
          v.item.reviewTeams?.some((team) =>
            viewer.teams?.some(
              (mine) => mine.toLowerCase() === team.toLowerCase(),
            ),
          ));
      if (
        v.queues
          ? !v.queues.includes(
              (
                {
                  Working: "working",
                  Inbox: "issues",
                  Mine: "mine",
                  Review: "review",
                } as Record<string, string>
              )[tab],
            )
          : tab === "Working"
            ? !snap?.runs.some(
                (r) => r.itemId === v.item.id && r.status !== "prepared",
              ) || v.item.state !== "open"
            : tab === "Inbox"
              ? v.item.kind !== "issue"
              : tab === "Mine"
                ? v.item.kind !== "pr" || !own
                : v.item.kind !== "pr" || !requested
      )
        return false;
      return (
        (filter === "all" ||
          (filter === "pending" &&
            v.item.state === "open" &&
            !v.hasPullRequest) ||
          (filter === "open" && v.item.state === "open") ||
          (filter === "new" && v.user.new) ||
          (filter === "unread" && v.unread) ||
          (filter === "acted-on" && v.user.actions.length > 0)) &&
        `${v.item.title} ${v.item.id}`
          .toLowerCase()
          .includes(search.toLowerCase())
      );
    }) ?? []
  ).sort(
    (a, b) =>
      (a.priority ?? 4) - (b.priority ?? 4) ||
      Date.parse(b.item.updated) - Date.parse(a.item.updated) ||
      a.item.id.localeCompare(b.item.id),
  );
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
    const saved = positions.current.get(pageKey);
    const target = saved?.focus
      ? [...document.querySelectorAll<HTMLElement>("[data-focus-key]")].find(
          (el) => el.dataset.focusKey === saved.focus,
        )
      : undefined;
    const heading = pageRef.current?.querySelector<HTMLElement>("h1, h2");
    if (!target && heading) heading.tabIndex = -1;
    const formFocused =
      tab === "Settings" &&
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
    if (!formFocused) (target ?? fallback)?.focus({ preventScroll: true });
    window.scrollTo(0, saved?.scroll ?? 0);
  }, [pageKey]);
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
  const agentEntry = snap?.inbox.find(
    (e) => e.item.id === (currentRun?.itemId ?? selected),
  );
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
      {error && (
        <div role="alert" className="error">
          {error}
          <button onClick={() => setError("")} aria-label="Dismiss error">
            ×
          </button>
        </div>
      )}
      {!snap ? (
        <p className="empty">Loading…</p>
      ) : (
        <>
          {tab === "Reported issues" && <ReportedIssues cursor={snap.cursor} />}
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
                  GitHub identity unavailable. Check the connection in Settings.
                </p>
              )}
              {tab === "Review" && viewer?.teamError && (
                <p role="alert">{viewer.teamError}</p>
              )}
              <section className="item-list" aria-label="Items">
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
                                setError(`Could not open GitHub: ${String(e)}`),
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
                            snap.runs.find((r) => r.id === a.runId)?.itemId ===
                              v.item.id,
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
                {snap.inbox.length > 0 && visibleItems.length === 0 && (
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
                {!snap.inbox.length && (
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
                  reviewVersion={(snap.reviewVersions?.[agentEntry.item.id] ?? "") + JSON.stringify(snap.runs.filter(r => r.itemId === agentEntry.item.id).map(r => [r.id,r.status,r.turnId]))}
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
                            {new Date(r.created).toLocaleString()} · {r.status}
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
                          setConsoleRuns((old) => ({ ...old, [runId]: value }))
                        }
                        recovery={snap.questionRecoveries?.find(
                          (r) =>
                            r.runId === runId &&
                            ["checking", "clarifying", "requesting-form", "awaiting-answer", "answered"].includes(r.stage),
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
                      ["checking", "clarifying", "requesting-form", "awaiting-answer", "answered"].includes(r.stage),
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
                launch ?? snap.inbox.find((e) => e.item.id === draftRun?.itemId)
              }
              initialRun={draftRun}
              changed={refresh}
              snap={snap}
              close={() => {
                setLaunch(undefined);
                setDraftRun(undefined);
              }}
              started={(run) => {
                setLaunch(undefined);
                setDraftRun(undefined);
                setSnap((old) =>
                  old
                    ? {
                        ...old,
                        runs: [...old.runs.filter((r) => r.id !== run.id), run],
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
  const linkedRevision=JSON.stringify(entry?.linkedPullRequests?.map(pr=>[pr.id,pr.head,pr.state,pr.reviewDecision,pr.ci,pr.error,pr.syncedAt]));
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    let delay = 1000;
    async function load() {
      try {
        const data = await call<Detail>("read_item", { id });
        if (alive) {
          setDetail(data);
          setLoadError(undefined);
          if(data.observation?.refresh === "pending") timer=setTimeout(load,1000);
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
  }, [id, entry?.item.revision, linkedRevision, detailVersion]);
  useEffect(() => {
    if (
      detail?.item.id === id && !detail.refreshError &&
      (!detail.observation || detail.observation.refresh === "fresh")
    )
      void action("item_action", {
        id,
        action: "read",
        revision: detail.item.revision,
      });
  }, [detail?.item.id, detail?.item.revision, detail?.refreshError, detail?.observation?.refresh, id, action]);
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
  const summaryParagraph = i.body
    .split(/\n\s*\n/)
    .map((paragraph) => paragraph.replace(/^ {0,3}#{1,6}(?:[ \t]+.*)?$/gm, "").trim())
    .find(Boolean) ?? "";
  const summary = summaryParagraph.slice(0, 600);
  return (
    <>
      {detail.observation?.refresh === "pending" && <p role="status">Refreshing saved details…</p>}
      {(detail.refreshError || detail.observation?.refresh === "cooldown") && (
        <p role={detail.refreshError ? "alert" : "status"}>
          {detail.refreshError || "Refresh deferred; showing saved information."}{" "}
          {detail.observation?.refresh === "cooldown" && detail.observation.retryAt &&
            <>Retry after {new Date(detail.observation.retryAt).toLocaleString()}. </>}
          <button onClick={() => setDetailVersion(v => v + 1)}>Retry detail refresh</button>
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
          {(detail.linkedPullRequests ?? entry.linkedPullRequests)!.map((pr) => (
            <PullRequestInfo key={pr.id} pr={pr} linked />
          ))}
        </section>
      )}
      {i.kind === "pr" && <PostedReviews itemId={i.id} head={i.head} version={reviewVersion} />}
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
  let depth = 0, quoted = false, escaped = false;
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
    if (pending.length &&
        (pending[0].data.method !== method || pending[0].entity !== event.entity)) flush();
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
        const params: unknown = JSON.parse(pending.map((part) => part.data.params?.delta ?? "").join(""));
        if (!params || typeof params !== "object" || Array.isArray(params)) throw new Error("Invalid provider params");
        decoded.push({ ...pending[0], data: { method: method.slice(0, -6), params: params as Record<string, unknown> } });
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
              : String(p.error ?? "Agent delivery is uncertain. Check this session before sending again.");
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
  const deferred = run.status === "deferred";
  const canMessage =
    !!run.threadId &&
    ["idle", "running", "waiting", "interrupted", "failed"].includes(run.status);
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;
    let delay = 1000;
    async function load() {
      try {
        const p = await call<EventPage>("read_transcript", {
          id: run.id,
          after: pageCursor.current,
          limit: 100,
        });
        if (!stop) {
          setEvents((old) => [...old, ...p.events].slice(-1000));
          pageCursor.current = p.cursor;
          setCursor(p.cursor);
          setMore(p.hasMore);
          setHistoryError(undefined);
          delay = 1000;
        }
      } catch (e) {
        if (!stop)
          setHistoryError({
            message: isConnectionFailure(e)
              ? "Connection interrupted. Retrying conversation…"
              : String(e),
            retrying: isConnectionFailure(e),
          });
        delay = Math.min(delay * 2, 30000);
      }
      if (!stop) timer = setTimeout(load, document.hidden ? 15000 : delay);
    }
    void load();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [run.id, historyVersion]);
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
        ["checking", "clarifying", "requesting-form", "awaiting-answer", "answered"].includes(recovery.stage) && (
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
      {hasMore && <p>Loading more history after event {cursor}…</p>}
      {events.length >= 1000 && (
        <button
          onClick={() => {
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
            !canMessage || !!recovery && ["checking", "clarifying", "requesting-form", "awaiting-answer", "answered"].includes(recovery.stage)
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
        {["idle", "interrupted", "failed", "terminated"].includes(run.status) && (
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
function SaveForm({
  save,
  children,
}: {
  save: () => Promise<void>;
  children: React.ReactNode;
}) {
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        if (busy) return;
        setBusy(true);
        setStatus("Saving…");
        setError("");
        try {
          await save();
          setStatus("Saved.");
        } catch (e) {
          setStatus("");
          setError(String(e));
        } finally {
          setBusy(false);
        }
      }}
    >
      <fieldset className="form-fields" disabled={busy}>
        {children}
      </fieldset>
      {status && <p role="status">{status}</p>}
      {error && <p role="alert">{error}</p>}
    </form>
  );
}
function Settings({ snap, action }: { snap: Snapshot; action: Action }) {
  async function save(name: string, args: Record<string, unknown>) {
    let error = "Could not save.";
    if (
      !(await action(name, args, (message) => {
        error = message;
      }))
    )
      throw new Error(error.replace(/^Error: /, ""));
  }
  const [config, setConfig] = useState<Config>(snap.config);
  const [labels, setLabels] = useState(
    Object.entries(snap.config.priorityLabels).map(([label, priority]) => ({
      label,
      priority,
    })),
  );
  const [profile, setProfile] = useState<Profile>(snap.profiles[0]);
  const [skillDirectory, setSkillDirectory] = useState("/");
  const [skillName, setSkillName] = useState("");
  const [skillContent, setSkillContent] = useState(
    "---\nname: my-skill\ndescription: Explain when this skill applies.\n---\n\n",
  );
  const [diagnostics, setDiagnostics] =
    useState<import("./types").Diagnostics>();
  const [diagnosticState, setDiagnosticState] = useState("");
  const [diagnosticError, setDiagnosticError] = useState("");
  return (
    <div className="settings">
      <details className="settings-section" open>
        <summary>Repositories</summary>
        <RepositoryAdmin snap={snap} save={save} />
        <SaveForm save={() => save("discover_skills", { cwd: skillDirectory })}>
          <label>
            Skill discovery directory
            <input
              value={skillDirectory}
              onChange={(e) => setSkillDirectory(e.target.value)}
              required
            />
          </label>
          <button>Discover local skills</button>
        </SaveForm>
      </details>
      <details className="settings-section">
        <summary>Plugins & skills</summary>
        <PluginAdmin skills={snap.skills} />
      </details>
      <details className="settings-section">
        <summary>System defaults</summary>
        <SaveForm save={() => save("save_settings", { ...config })}>
          <SystemReviewSettings
            usage={snap.githubUsage}
            config={config}
            profiles={snap.profiles}
            change={setConfig}
          />
          <button>Save system defaults</button>
        </SaveForm>
      </details>
      <details className="settings-section">
        <summary>Priorities & notifications</summary>
        <SaveForm
          save={async () => {
            const names = labels.map((row) => row.label.trim());
            if (
              names.some((name) => !name) ||
              new Set(names).size !== names.length
            )
              throw new Error(
                "Use a unique, nonempty label for each priority row.",
              );
            const priorityLabels = Object.fromEntries(
              labels.map((row) => [row.label.trim(), row.priority]),
            );
            await save("save_settings", { ...config, priorityLabels });
            setConfig((current) => ({ ...current, priorityLabels }));
          }}
        >
          <fieldset>
            <legend>GitHub priority labels</legend>
            {labels.map((row, index) => (
              <div className="priority-row" key={index}>
                <label>
                  Label
                  <input
                    aria-label={`Priority label ${index + 1}`}
                    value={row.label}
                    onChange={(e) =>
                      setLabels(
                        labels.map((v, n) =>
                          n === index ? { ...v, label: e.target.value } : v,
                        ),
                      )
                    }
                    required
                  />
                </label>
                <label>
                  Priority
                  <select
                    aria-label={`Mapped priority ${index + 1}`}
                    value={row.priority}
                    onChange={(e) =>
                      setLabels(
                        labels.map((v, n) =>
                          n === index
                            ? { ...v, priority: Number(e.target.value) }
                            : v,
                        ),
                      )
                    }
                  >
                    {[0, 1, 2, 3].map((v) => (
                      <option value={v} key={v}>
                        P{v}
                      </option>
                    ))}
                  </select>
                </label>
                <button
                  type="button"
                  aria-label={`Remove priority label ${index + 1}`}
                  onClick={() =>
                    setLabels(labels.filter((_, n) => n !== index))
                  }
                >
                  Remove
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => setLabels([...labels, { label: "", priority: 2 }])}
            >
              Add priority label
            </button>
          </fieldset>
          <label>
            Assessment prompt
            <textarea
              value={config.assessmentPrompt}
              onChange={(e) =>
                setConfig({ ...config, assessmentPrompt: e.target.value })
              }
              rows={5}
            />
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={config.notifications}
              onChange={(e) =>
                setConfig({ ...config, notifications: e.target.checked })
              }
            />
            Background notifications
          </label>
          <label>
            Question helper model (optional)
            <input
              value={config.questionHelperModel ?? ""}
              onChange={(event) =>
                setConfig({
                  ...config,
                  questionHelperModel: event.target.value,
                })
              }
              placeholder="Use the session model"
            />
          </label>
          <button>Save settings</button>
        </SaveForm>
      </details>
      <details className="settings-section">
        <summary>Launch profiles</summary>
        <select
          aria-label="Edit profile"
          value={profile.id}
          onChange={(e) =>
            setProfile(snap.profiles.find((p) => p.id === e.target.value)!)
          }
        >
          {snap.profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <button
          onClick={() =>
            setProfile({
              id: crypto.randomUUID(),
              name: "New profile",
              prompt: "Investigate {{title}}.\n{{body}}",
              skills: [],
              model: "",
              effort: "",
              sandbox: "read-only",
              approval: "on-request",
              publishing: "ask",
            })
          }
        >
          New profile
        </button>
        <SaveForm save={() => save("save_profile", { ...profile })}>
          <label>
            Name
            <input
              value={profile.name}
              onChange={(e) => setProfile({ ...profile, name: e.target.value })}
              required
            />
          </label>
          <label>
            Prompt template
            <textarea
              value={profile.prompt}
              onChange={(e) =>
                setProfile({ ...profile, prompt: e.target.value })
              }
              rows={5}
            />
          </label>
          <label>
            Model
            <input
              value={profile.model}
              onChange={(e) =>
                setProfile({ ...profile, model: e.target.value })
              }
            />
          </label>
          <label>
            Effort
            <input
              value={profile.effort}
              onChange={(e) =>
                setProfile({ ...profile, effort: e.target.value })
              }
            />
          </label>
          <label>
            Agent mode
            <select
              value={profile.mode || "default"}
              onChange={(e) =>
                setProfile({
                  ...profile,
                  mode: e.target.value as "default" | "plan",
                })
              }
            >
              <option value="default">Default</option>
              <option value="plan">Plan · interactive questions</option>
            </select>
          </label>
          <label>
            Sandbox
            <select
              value={profile.sandbox}
              onChange={(e) =>
                setProfile({ ...profile, sandbox: e.target.value })
              }
            >
              {["read-only", "workspace-write", "danger-full-access"].map(
                (x) => (
                  <option key={x}>{x}</option>
                ),
              )}
            </select>
          </label>
          <label>
            Approval policy
            <select
              value={profile.approval}
              onChange={(e) =>
                setProfile({ ...profile, approval: e.target.value })
              }
            >
              {["on-request", "untrusted", "never"].map((x) => (
                <option key={x}>{x}</option>
              ))}
            </select>
          </label>
          <label>
            Publishing authority
            <select
              value={profile.publishing}
              onChange={(e) =>
                setProfile({ ...profile, publishing: e.target.value })
              }
            >
              <option value="ask">Ask before publishing</option>
              <option value="authorized">Publishing authorized by task</option>
            </select>
          </label>
          <fieldset>
            <legend>Default skills</legend>
            {snap.skills.map((s) => (
              <label className="check" key={s.id}>
                <input
                  type="checkbox"
                  disabled={s.disabled}
                  checked={(profile.skills ?? []).includes(s.id)}
                  onChange={(e) =>
                    setProfile({
                      ...profile,
                      skills: e.target.checked
                        ? [...(profile.skills ?? []), s.id]
                        : (profile.skills ?? []).filter((id) => id !== s.id),
                    })
                  }
                />
                {s.name}
                {s.disabled ? " · disabled or unavailable" : ""}
              </label>
            ))}
          </fieldset>
          <button>Save profile</button>
        </SaveForm>
      </details>
      <details className="settings-section">
        <summary>Managed skills</summary>
        <select
          aria-label="Edit managed skill"
          onChange={(e) => {
            const s = snap.skills.find((s) => s.id === e.target.value);
            if (s) {
              setSkillName(s.name);
              setSkillContent(s.content);
            }
          }}
        >
          <option value="">Create a skill</option>
          {snap.skills
            .filter((s) => s.managed)
            .map((s) => (
              <option value={s.id} key={s.id}>
                {s.name}
              </option>
            ))}
        </select>
        <SaveForm
          save={() =>
            save("save_skill", { name: skillName, content: skillContent })
          }
        >
          <label>
            Skill name
            <input
              value={skillName}
              onChange={(e) => setSkillName(e.target.value)}
              required
              placeholder="my-skill"
            />
          </label>
          <label>
            Skill instructions
            <textarea
              value={skillContent}
              onChange={(e) => setSkillContent(e.target.value)}
              rows={10}
            />
          </label>
          <button>Save skill</button>
        </SaveForm>
      </details>
      <details className="settings-section">
        <summary>Connection diagnostics</summary>
        <button
          disabled={diagnosticState === "Checking…"}
          onClick={async () => {
            setDiagnosticState("Checking…");
            setDiagnosticError("");
            try {
              setDiagnostics(
                await call<import("./types").Diagnostics>("diagnostics"),
              );
              setDiagnosticState("Checked.");
            } catch (e) {
              setDiagnosticState("");
              setDiagnosticError(String(e));
            }
          }}
        >
          Check connection
        </button>
        {diagnosticState && <p role="status">{diagnosticState}</p>}
        {diagnosticError && <p role="alert">{diagnosticError}</p>}
        {diagnostics !== undefined && (
          <div>
            <p>
              Version {diagnostics.version} · {diagnostics.at}
            </p>
            <ul>
              {diagnostics.checks.map((c) => (
                <li key={c.id}>
                  <strong>{c.state}</strong> · {c.id}: {c.detail}
                </li>
              ))}
            </ul>
          </div>
        )}
        <p>
          GitHub and Codex reuse local sign-in. The setup skill can guide
          authorization using Work computer access.
        </p>
      </details>
    </div>
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
