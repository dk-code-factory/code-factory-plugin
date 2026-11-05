import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createRoot } from "react-dom/client";
import { call, openLink } from "./bridge";
import "./wrapper.css";

type Item = {
  id: string;
  repository: string;
  number: number;
  kind: string;
  title: string;
  body?: string;
  state: string;
  url: string;
  revision: string;
  labels: string[];
  head: string;
  author?: string;
};
type Entry = {
  item: Item;
  detailVersion?: string;
  priority: number | null;
  prioritySource: string;
  unread: boolean;
  user: { new: boolean; override?: number | null };
  assessment?: { status: string; error?: string; rationale?: string };
  queues?: string[];
  work?: { complete: boolean };
  linkedPullRequests?: { id: string; title: string; state: string }[];
};
type Session = {
  id: string;
  itemId?: string;
  kind: string;
  execution: string;
  reported?: string;
  summary?: string;
  pendingCount: number;
  deliveryState?: string;
  threadPending?: boolean;
  hold: boolean;
  error?: string;
  agent: { profile?: string; definition?: string };
  due: string;
  threadId?: string;
};
type Question = {
  id: string;
  sessionId: string;
  method: string;
  status: string;
  blocking: boolean;
  params: Record<string, unknown>;
};
type Proposal = { id: string; state: string; diff: string };
type State = {
  version: number;
  inbox: Entry[];
  selectedEntry?: Entry;
  inboxTotal: number;
  sessions: Session[];
  questions: Question[];
  proposals?: Proposal[];
  settings?: Record<string, unknown>;
};
type Transcript = {
  events: {
    seq: number;
    data: {
      fragmentId?: string;
      fragmentIndex?: number;
      fragmentCount?: number;
      fragment?: string;
      method?: string;
      params?: {
        delta?: string;
        itemId?: string;
        turnId?: string;
        item?: { id?: string; type?: string; text?: string };
      };
      text?: string;
    };
  }[];
  cursor: number;
  hasMore: boolean;
};
const blank: State = {
  version: 0,
  inbox: [],
  inboxTotal: 0,
  sessions: [],
  questions: [],
  proposals: [],
  settings: {},
};
type Query = { search: string; filter: string; offset: number; limit: number };
const defaultQuery: Query = {
  search: "",
  filter: "open",
  offset: 0,
  limit: 50,
};
const views: Record<string, string> = {
  Working: "working",
  Issues: "issues",
  "My PRs": "mine",
  "Review PRs": "review",
  Messages: "messages",
  Configuration: "configuration",
  Conversation: "conversation",
  Events: "events",
};
const inboxTabs = ["Working", "Issues", "My PRs", "Review PRs"];

function Factory() {
  const boot = (
    window as unknown as {
      factoryBoot?: { tab: string; search: string; filter: string };
    }
  ).factoryBoot;
  const [state, setState] = useState<State>(blank);
  const [tab, setTab] = useState(
    document.documentElement.dataset.codeFactoryView === "settings"
      ? "Configuration"
      : boot?.tab || "Issues",
  );
  const [selected, setSelected] = useState("");
  const [selectedTitle, setSelectedTitle] = useState("");
  const [detail, setDetail] = useState<{
    item: Item;
    detailVersion: string;
    relationships: {
      source: string;
      target: string;
      evidence: string;
      manual?: boolean;
    }[];
  }>();
  const [queries, setQueries] = useState<Record<string, Query>>({
    [boot?.tab || "Issues"]: {
      ...defaultQuery,
      search: boot?.search || "",
      filter: ["open", "all", "new", "unread"].includes(boot?.filter || "")
        ? boot!.filter
        : "open",
    },
  });
  const query = queries[tab] || defaultQuery;
  const { search, filter, offset, limit } = query;
  const updateQuery = (next: Partial<Query>) =>
    setQueries((old) => ({ ...old, [tab]: { ...query, ...next } }));
  const [origin, setOrigin] = useState<{
    tab: string;
    query: Query;
    scroll: number;
    id: string;
  }>();
  const restore = useRef<{ scroll: number; id: string } | undefined>(undefined);
  const rowRefs = useRef(new Map<string, HTMLButtonElement>());
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [connected, setConnected] = useState(false);
  const [operations, setOperations] = useState<Set<string>>(new Set());
  const operationsRef = useRef(new Set<string>());
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [native, setNative] = useState<unknown>();
  const [cwd, setCwd] = useState("");
  const [links, setLinks] = useState<Record<string, string>>({});
  const [relationshipsOpen, setRelationshipsOpen] = useState<
    Record<string, boolean>
  >({});
  const link = links[selected] || "";
  const setLink = (value: string) =>
    setLinks((old) => ({ ...old, [selected]: value }));
  const [transcript, setTranscript] = useState<Transcript>({
    events: [],
    cursor: 0,
    hasMore: false,
  });
  const messages = useMemo(
    () => transcriptMessages(transcript.events),
    [transcript.events],
  );
  const current =
    tab === "Configuration"
      ? "configuration"
      : tab === "Conversation"
        ? selected
        : "";
  const currentRef = useRef(current);
  currentRef.current = current;
  const session = state.sessions.find((s) => s.id === current);
  const busy = operations.has(current || tab);
  const activeQuestions = state.questions.filter((q) => q.status === "pending");
  const request = {
    view: views[tab],
    search,
    filter,
    offset,
    limit,
    itemId: current,
  };
  const requestKey = JSON.stringify(request);
  const requestRef = useRef(requestKey);
  requestRef.current = requestKey;
  const projectionRef = useRef<{ key: string; version?: number }>({
    key: requestKey,
  });
  if (projectionRef.current.key !== requestKey)
    projectionRef.current = { key: requestKey };
  const projection = projectionRef.current;
  const [stateKey, setStateKey] = useState("");
  const detailVersion =
    stateKey === requestKey && state.selectedEntry?.item.id === selected
      ? state.selectedEntry.detailVersion
      : undefined;
  const detailReady =
    tab === "Configuration" ||
    (tab === "Conversation" &&
      !!detailVersion &&
      detail?.item.id === selected &&
      detail.detailVersion === detailVersion);
  const refresh = useCallback(async () => {
    const args = JSON.parse(requestKey);
    if (projection.version !== undefined)
      args.sinceVersion = projection.version;
    const value = await call<State | { unchanged: true; version: number }>(
      "factory_state",
      args,
    );
    if (
      requestRef.current === requestKey &&
      projectionRef.current === projection
    ) {
      if (
        projection.version !== undefined &&
        value.version < projection.version
      )
        return;
      projection.version = value.version;
      if (!("unchanged" in value)) {
        setState(value);
        setStateKey(requestKey);
      }
      setConnected(true);
    }
  }, [requestKey, projection]);
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        await refresh();
      } catch (e) {
        if (alive) {
          setConnected(false);
          setError(String(e));
        }
      } finally {
        if (alive) timer = setTimeout(poll, document.hidden ? 10000 : 1000);
      }
    };
    void poll();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [refresh]);
  useEffect(() => {
    setDetail(undefined);
    if (tab !== "Conversation" || !selected || !detailVersion) return;
    let alive = true;
    void call<typeof detail>("factory_item", { id: selected })
      .then((d) => {
        if (alive && d?.item.id === selected) {
          if (d.detailVersion !== detailVersion)
            void refreshRef.current().catch((e) => setError(String(e)));
          else setDetail(d);
        }
      })
      .catch((e) => {
        if (alive) setError(String(e));
      });
    return () => {
      alive = false;
    };
  }, [tab, selected, detailVersion]);
  useEffect(() => {
    if (!detailReady || !detail || currentRef.current !== detail.item.id)
      return;
    // This effect runs only after matching detail is committed to the view.
    void call("user_action", {
      id: detail.item.id,
      action: "read",
      revision: detail.item.revision,
    }).catch((e) => setError(String(e)));
  }, [detail, detailReady]);
  useEffect(() => {
    if (stateKey !== requestKey || !inboxTabs.includes(tab) || !restore.current)
      return;
    const target = restore.current;
    const frame = requestAnimationFrame(() => {
      rowRefs.current.get(target.id)?.focus({ preventScroll: true });
      window.scrollTo(0, target.scroll);
      restore.current = undefined;
    });
    return () => cancelAnimationFrame(frame);
  }, [stateKey, requestKey, tab]);
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    let cursor = 0;
    setTranscript({ events: [], cursor: 0, hasMore: false });
    if (!current) return;
    const poll = async () => {
      try {
        while (alive) {
          const previous = cursor;
          const page = await call<Transcript>("session_transcript", {
            id: current,
            after: cursor,
          });
          if (!alive) return;
          cursor = page.cursor;
          setTranscript((old) => ({
            ...page,
            events: page.events.length
              ? [...old.events, ...page.events].slice(-1000)
              : old.events,
          }));
          if (!page.hasMore) break;
          if (cursor <= previous)
            throw new Error("Transcript page did not advance its cursor");
        }
      } catch (e) {
        if (alive) setError(String(e));
      } finally {
        if (alive) timer = setTimeout(poll, document.hidden ? 10000 : 1000);
      }
    };
    void poll();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [current]);
  async function mutate(
    name: string,
    args: Record<string, unknown>,
    success = "Saved",
  ) {
    const scope =
      name === "answer_session_question"
        ? state.questions.find((q) => q.id === args.id)?.sessionId ||
          String(args.id)
        : name === "configuration_chat" ||
            name === "confirm_configuration" ||
            name === "inspect_configuration"
          ? "configuration"
          : name === "sync_repositories"
            ? "sync"
            : String(args.itemId || args.id || args.source || current);
    if (operationsRef.current.has(scope)) return;
    operationsRef.current.add(scope);
    setOperations(new Set(operationsRef.current));
    setError("");
    setNotice("");
    try {
      const result = await call(name, args);
      setNotice(success);
      try {
        await refreshRef.current();
      } catch (e) {
        setError(
          `The action was accepted, but refreshing failed: ${String(e)}`,
        );
      }
      return result;
    } catch (e) {
      setError(String(e));
    } finally {
      operationsRef.current.delete(scope);
      setOperations(new Set(operationsRef.current));
    }
  }
  async function send(e: React.FormEvent) {
    e.preventDefault();
    const text = drafts[current]?.trim();
    if (!text) return;
    const key = current;
    const result = await mutate(
      tab === "Configuration" ? "configuration_chat" : "session_chat",
      tab === "Configuration" ? { text } : { itemId: key, text },
      "Message delivered",
    );
    if (result)
      setDrafts((old) =>
        old[key]?.trim() === text ? { ...old, [key]: "" } : old,
      );
  }
  function choose(id: string) {
    if (tab !== "Conversation")
      setOrigin({ tab, query: { ...query }, scroll: window.scrollY, id });
    setSelectedTitle(
      state.inbox.find((entry) => entry.item.id === id)?.item.title || id,
    );
    setDetail(undefined);
    setError("");
    setNotice("");
    setSelected(id);
    setTab("Conversation");
  }
  function back() {
    const target = origin || {
      tab: "Issues",
      query: defaultQuery,
      scroll: 0,
      id: selected,
    };
    setQueries((old) => ({ ...old, [target.tab]: target.query }));
    restore.current = { scroll: target.scroll, id: target.id };
    setDetail(undefined);
    setTab(target.tab);
    setError("");
    setNotice("");
  }
  const visible = stateKey === requestKey ? state.inbox : [];
  return (
    <main>
      <header>
        <h1>Code Factory</h1>
        <span role="status" aria-label="Connection status">
          {connected ? "Connected" : "Connecting to Work…"}
        </span>
      </header>
      <nav aria-label="Main navigation">
        {[
          "Working",
          "Issues",
          "My PRs",
          "Review PRs",
          "Messages",
          "Configuration",
          "Events",
        ].map((name) => (
          <button
            key={name}
            aria-current={tab === name ? "page" : undefined}
            onClick={() => setTab(name)}
          >
            {name}
            {name === "Messages" && activeQuestions.length
              ? ` (${activeQuestions.length})`
              : ""}
          </button>
        ))}
      </nav>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {inboxTabs.includes(tab) && (
        <section aria-label={tab}>
          <h2>{tab}</h2>
          <div className="row">
            <input
              aria-label="Search inbox"
              placeholder="Search"
              value={search}
              onChange={(e) =>
                updateQuery({ search: e.target.value, offset: 0, limit: 50 })
              }
            />
            <select
              aria-label="Inbox filter"
              value={filter}
              onChange={(e) =>
                updateQuery({ filter: e.target.value, offset: 0, limit: 50 })
              }
            >
              {["open", "all", "unread", "new"].map((v) => (
                <option key={v}>{v}</option>
              ))}
            </select>
            <button
              disabled={operations.has("sync")}
              onClick={() =>
                void mutate(
                  "sync_repositories",
                  {},
                  "Synchronization completed",
                )
              }
            >
              Sync
            </button>
          </div>
          {stateKey !== requestKey ? (
            <p role="status">Loading items…</p>
          ) : (
            visible.length === 0 && <p>No matching items.</p>
          )}
          <ul className="items">
            {visible.slice(0, limit).map((entry) => {
              const s = state.sessions.find((v) => v.itemId === entry.item.id);
              return (
                <li className="inbox-row" key={entry.item.id}>
                  <button
                    className="item"
                    ref={(node) => {
                      if (node) rowRefs.current.set(entry.item.id, node);
                      else rowRefs.current.delete(entry.item.id);
                    }}
                    onClick={() => choose(entry.item.id)}
                  >
                    <strong>{entry.item.title}</strong>
                    <span>
                      {entry.item.id} ·{" "}
                      {entry.priority === null
                        ? "Unassessed"
                        : `P${entry.priority}`}{" "}
                      · {entry.item.state}
                      {entry.unread ? " · Unread" : ""}
                      {entry.user.new ? " · New" : ""}
                    </span>
                    {entry.assessment?.error && (
                      <span>Priority assessment: {entry.assessment.error}</span>
                    )}
                    {s && (
                      <span>
                        {s.execution} · {s.reported || "idle"} ·{" "}
                        {s.pendingCount} pending events
                      </span>
                    )}
                  </button>
                  {entry.linkedPullRequests?.map((p) => (
                    <p key={p.id}>
                      {p.title || p.id} · {p.state}
                    </p>
                  ))}
                </li>
              );
            })}
          </ul>
          {stateKey === requestKey && (
            <p aria-label="Inbox result count">
              {state.inboxTotal} matching items · showing{" "}
              {visible.length ? offset + 1 : 0}–{offset + visible.length}
            </p>
          )}
          {stateKey === requestKey &&
            state.inboxTotal > offset + limit &&
            limit < 200 && (
              <button
                onClick={() =>
                  updateQuery({ limit: Math.min(200, limit + 50) })
                }
              >
                Show more items
              </button>
            )}
          {offset > 0 && (
            <button
              onClick={() =>
                updateQuery({ offset: Math.max(0, offset - limit) })
              }
            >
              Previous page
            </button>
          )}
          {stateKey === requestKey &&
            state.inboxTotal > offset + limit &&
            limit === 200 && (
              <button onClick={() => updateQuery({ offset: offset + limit })}>
                Next page
              </button>
            )}
        </section>
      )}
      {tab === "Messages" && (
        <section aria-label="Questions">
          <h2>Questions and approvals</h2>
          {activeQuestions.length === 0 && <p>No pending questions.</p>}
          {activeQuestions.map((q) => (
            <article key={q.id}>
              <button
                onClick={() => {
                  if (q.sessionId === "configuration") setTab("Configuration");
                  else choose(q.sessionId);
                }}
              >
                Open {q.sessionId}
              </button>
              <QuestionForm
                question={q}
                busy={operations.has(q.sessionId)}
                answer={(answer) =>
                  mutate(
                    "answer_session_question",
                    { id: q.id, answer },
                    "Answer sent",
                  )
                }
              />
            </article>
          ))}
          {state.questions
            .filter((q) => ["stale", "uncertain"].includes(q.status))
            .map((q) => (
              <p key={q.id}>
                {q.sessionId}: {q.status} request. Resume its retained
                conversation to continue.
              </p>
            ))}
        </section>
      )}
      {(tab === "Conversation" || tab === "Configuration") && (
        <section aria-label={tab}>
          {tab === "Conversation" && <button onClick={back}>Back</button>}
          <h2>
            {tab === "Configuration"
              ? "Configuration chat"
              : detailReady && detail
                ? detail.item.title
                : selectedTitle || selected}
          </h2>
          {tab === "Conversation" && !detailReady && (
            <p role="status">
              Item context is unavailable until loading succeeds.
            </p>
          )}
          {tab === "Conversation" && detailReady && detail && (
            <>
              <p>
                {detail.item.id} · {detail.item.state} · {detail.item.head}
              </p>
              <p className="body">{detail.item.body}</p>
              <button
                disabled={busy}
                onClick={() =>
                  void mutate("user_action", {
                    id: selected,
                    action: "acknowledge",
                  })
                }
              >
                Acknowledge new
              </button>
              <button
                disabled={busy}
                onClick={() =>
                  void mutate("user_action", { id: selected, action: "unread" })
                }
              >
                Mark unread
              </button>
              <button
                disabled={busy}
                onClick={() =>
                  void mutate("user_action", { id: selected, action: "acted" })
                }
              >
                Record action
              </button>

              <button
                onClick={() =>
                  void openLink(detail.item.url).catch((e) =>
                    setError(String(e)),
                  )
                }
              >
                Open on GitHub
              </button>
              <label>
                Priority
                <select
                  aria-label="Priority"
                  value={
                    state.selectedEntry?.item.id === selected
                      ? (state.selectedEntry.user.override ?? "")
                      : ""
                  }
                  disabled={busy}
                  onChange={(e) =>
                    void mutate("user_action", {
                      id: selected,
                      action: "priority",
                      priority:
                        e.target.value === "" ? null : Number(e.target.value),
                    })
                  }
                >
                  <option value="">Automatic</option>
                  {[0, 1, 2, 3].map((p) => (
                    <option value={p} key={p}>
                      P{p}
                    </option>
                  ))}
                </select>
              </label>
              <details
                open={relationshipsOpen[selected] || false}
                onToggle={(e) => {
                  const open = e.currentTarget.open;
                  setRelationshipsOpen((old) => ({ ...old, [selected]: open }));
                }}
              >
                <summary>Relationships</summary>
                {detail.relationships
                  .filter((l) => l.source === selected || l.target === selected)
                  .map((l, i) => (
                    <p key={i}>
                      {l.source} → {l.target}
                      {l.manual && (
                        <button
                          disabled={busy}
                          onClick={() =>
                            void mutate("link_items", {
                              source: l.source,
                              target: l.target,
                              remove: true,
                            })
                          }
                        >
                          Remove relationship
                        </button>
                      )}
                    </p>
                  ))}
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    void mutate("link_items", {
                      source: selected,
                      target: link,
                      evidence: "Manual implementation relationship",
                    });
                  }}
                >
                  <label>
                    Linked item
                    <input
                      value={link}
                      onChange={(e) => setLink(e.target.value)}
                    />
                  </label>
                  <button disabled={busy || !link}>Link</button>
                </form>
              </details>
            </>
          )}
          <div className="session-status" aria-label="Session status">
            <p>Execution: {session?.execution || "Not started"}</p>
            <p>Reported status: {session?.reported || "idle"}</p>
            <p>Pending events: {session?.pendingCount || 0}</p>
            {session?.pendingCount ? (
              <p>Batch due: {new Date(session.due).toLocaleTimeString()}</p>
            ) : null}
            {session?.hold && <p>Queue held for explicit continuation</p>}
            {session?.summary && <p>{session.summary}</p>}
            {session?.error && <p role="alert">{session.error}</p>}
            {session && (
              <p>
                Agent: {session.agent.definition || "native default"}
                {session.agent.profile
                  ? ` · profile ${session.agent.profile}`
                  : ""}
              </p>
            )}
          </div>
          {session && (
            <div className="row">
              {["run", "resume", "interrupt", "cleanup"].map((action) => (
                <button
                  disabled={busy}
                  key={action}
                  onClick={() =>
                    void mutate(
                      "session_control",
                      { id: current, action },
                      `${action} accepted`,
                    )
                  }
                >
                  {action === "cleanup"
                    ? "Clean worktree"
                    : action[0].toUpperCase() + action.slice(1)}
                </button>
              ))}
              {(["claimed", "uncertain"].includes(
                session.deliveryState || "",
              ) ||
                (session.threadPending && session.hold)) && (
                <>
                  {["claimed", "uncertain"].includes(
                    session.deliveryState || "",
                  ) && (
                    <button
                      disabled={busy}
                      onClick={() =>
                        void mutate("resolve_session_delivery", {
                          id: current,
                          decision: "discard",
                        })
                      }
                    >
                      Discard uncertain delivery
                    </button>
                  )}
                  <button
                    disabled={busy}
                    onClick={() =>
                      void mutate("resolve_session_delivery", {
                        id: current,
                        decision: "retry",
                      })
                    }
                  >
                    Explicitly retry uncertain delivery
                  </button>
                </>
              )}
            </div>
          )}
          {activeQuestions
            .filter((q) => q.sessionId === current)
            .map((q) => (
              <QuestionForm
                key={q.id}
                question={q}
                busy={operations.has(q.sessionId)}
                answer={(answer) =>
                  mutate(
                    "answer_session_question",
                    { id: q.id, answer },
                    "Answer sent",
                  )
                }
              />
            ))}
          <div
            className="transcript"
            role="log"
            aria-label="Conversation transcript"
          >
            {messages.map((entry) => (
              <p key={entry.key}>{entry.text}</p>
            ))}
          </div>
          <form onSubmit={(e) => void send(e)}>
            <label>
              Message
              <textarea
                aria-label="Message"
                value={drafts[current] || ""}
                onChange={(e) =>
                  setDrafts((old) => ({ ...old, [current]: e.target.value }))
                }
              />
            </label>
            <button
              disabled={
                busy ||
                !detailReady ||
                !drafts[current]?.trim() ||
                activeQuestions.some(
                  (q) => q.sessionId === current && q.blocking,
                )
              }
            >
              Send
            </button>
          </form>
          {tab === "Configuration" && (
            <>
              <h3>Proposed changes</h3>
              {!state.proposals?.length && <p>No configuration proposals.</p>}
              {state.proposals?.map((p) => (
                <article key={p.id}>
                  <p>
                    Proposal {p.id} · {p.state}
                  </p>
                  <pre>{p.diff}</pre>
                  {p.state === "proposed" && (
                    <div className="row">
                      <button
                        disabled={busy}
                        onClick={() =>
                          void mutate(
                            "confirm_configuration",
                            { id: p.id, confirm: true },
                            "Configuration confirmed; application waits for safe turn boundaries",
                          )
                        }
                      >
                        Confirm these changes
                      </button>
                      <button
                        disabled={busy}
                        onClick={() =>
                          void mutate(
                            "confirm_configuration",
                            { id: p.id, confirm: false },
                            "Proposal rejected",
                          )
                        }
                      >
                        Reject
                      </button>
                    </div>
                  )}
                </article>
              ))}
              <details>
                <summary>Effective settings</summary>
                <pre>{JSON.stringify(state.settings, null, 2)}</pre>
              </details>
              <details>
                <summary>Native Codex configuration</summary>
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    void mutate("inspect_configuration", { cwd }).then(
                      setNative,
                    );
                  }}
                >
                  <label>
                    Repository checkout
                    <input
                      value={cwd}
                      onChange={(e) => setCwd(e.target.value)}
                    />
                  </label>
                  <button disabled={busy || !cwd}>
                    Inspect native settings
                  </button>
                </form>
                {native != null && <pre>{JSON.stringify(native, null, 2)}</pre>}
              </details>
            </>
          )}
        </section>
      )}
      {tab === "Events" && (
        <Archive onConfigure={() => setTab("Configuration")} />
      )}
    </main>
  );
}
type ArchiveKind = "events" | "deliveries" | "batches" | "rules";
type ArchiveRow = {
  id: string;
  kind: ArchiveKind;
  repository?: string;
  itemId?: string;
  sessionId?: string;
  at?: string;
  type?: string;
  action?: string;
  source?: string;
  state?: string;
  preview?: { text: string; originalBytes: number; truncated: boolean };
} & Record<string, unknown>;
type ArchivePage = {
  kind: ArchiveKind;
  offset: number;
  limit: number;
  total: number;
  rows: ArchiveRow[];
};
function Archive({ onConfigure }: { onConfigure: () => void }) {
  const [kind, setKind] = useState<ArchiveKind>("events");
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(50);
  const [refresh, setRefresh] = useState(0);
  const [accepted, setAccepted] = useState<{
    key: string;
    page: ArchivePage;
  }>();
  const [selected, setSelected] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const key = JSON.stringify([kind, offset, limit]);
  useEffect(() => {
    let current = true;
    setLoading(true);
    setError("");
    void call("factory_events", { kind, offset, limit }).then(
      (result) => {
        if (!current) return;
        const page = result as ArchivePage;
        setAccepted({ key, page });
        setSelected((id) => (page.rows.some((row) => row.id === id) ? id : ""));
        setLoading(false);
      },
      (failure) => {
        if (!current) return;
        setError(String(failure));
        setLoading(false);
      },
    );
    return () => {
      current = false;
    };
  }, [kind, offset, limit, key, refresh]);
  const page = accepted?.key === key ? accepted.page : undefined;
  const row = page?.rows.find((entry) => entry.id === selected);
  const metadata =
    row &&
    Object.fromEntries(
      Object.entries(row).filter(([name]) => name !== "preview"),
    );
  return (
    <section aria-label="Event routing">
      <h2>Events, rules and batches</h2>
      <div className="row">
        <label>
          Archive kind{" "}
          <select
            value={kind}
            onChange={(event) => {
              setKind(event.target.value as ArchiveKind);
              setOffset(0);
              setSelected("");
            }}
          >
            <option value="events">Events</option>
            <option value="deliveries">Webhook deliveries</option>
            <option value="batches">Batches</option>
            <option value="rules">Rules</option>
          </select>
        </label>
        <label>
          Archive page size{" "}
          <select
            value={limit}
            onChange={(event) => {
              setLimit(Number(event.target.value));
              setOffset(0);
              setSelected("");
            }}
          >
            {[50, 100, 200].map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </label>
        <button onClick={() => setRefresh((value) => value + 1)}>
          Refresh archive
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
      {loading && <p role="status">Loading archive page…</p>}
      {page && (
        <>
          <p aria-live="polite">
            {page.rows.length ? offset + 1 : 0}–
            {page.rows.length ? offset + page.rows.length : 0} of {page.total}{" "}
            {kind}
          </p>
          <div className="row">
            <button
              disabled={loading || offset === 0}
              onClick={() => {
                setOffset(Math.max(0, offset - limit));
                setSelected("");
              }}
            >
              Previous archive page
            </button>
            <button
              disabled={loading || offset + limit >= page.total}
              onClick={() => {
                setOffset(offset + limit);
                setSelected("");
              }}
            >
              Next archive page
            </button>
          </div>
          <ul className="items" aria-label="Archive records">
            {page.rows.map((entry) => (
              <li key={entry.id}>
                <button
                  className="item"
                  aria-pressed={selected === entry.id}
                  onClick={() => setSelected(entry.id)}
                >
                  {entry.id}
                  <span>
                    {[
                      entry.repository,
                      entry.itemId,
                      entry.type,
                      entry.action,
                      entry.state,
                      entry.at,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      {row && (
        <article aria-label="Selected archive record">
          <h3>{row.id}</h3>
          <pre>{JSON.stringify(metadata, null, 2)}</pre>
          {row.preview && (
            <>
              <h3>Preview</h3>
              <pre>{row.preview.text}</pre>
              {row.preview.truncated && (
                <p>Preview truncated from {row.preview.originalBytes} bytes.</p>
              )}
            </>
          )}
        </article>
      )}
      <p>
        Archive pages show compact metadata and previews. Open the item's
        conversation for current context.
      </p>
      {kind === "rules" && (
        <>
          <p>
            Rule lists show up to 16 entries each; the counts include all
            entries.
          </p>
          <button onClick={onConfigure}>
            Read full rules in Configuration
          </button>
        </>
      )}
    </section>
  );
}
function transcriptMessages(events: Transcript["events"]) {
  const rows: { key: number; text: string }[] = [];
  const indexes = new Map<string, number>();
  let fragments:
    | { id: string; count: number; parts: string[]; seq: number }
    | undefined;
  for (const event of events) {
    let { data } = event;
    let seq = event.seq;
    if (typeof data.fragmentId === "string") {
      if (
        data.fragmentIndex === 0 &&
        Number.isInteger(data.fragmentCount) &&
        data.fragmentCount! > 0 &&
        data.fragmentCount! <= 1000
      )
        fragments = {
          id: data.fragmentId,
          count: data.fragmentCount!,
          parts: [],
          seq,
        };
      if (
        !fragments ||
        fragments.id !== data.fragmentId ||
        fragments.count !== data.fragmentCount ||
        data.fragmentIndex !== fragments.parts.length ||
        typeof data.fragment !== "string"
      ) {
        fragments = undefined;
        continue;
      }
      fragments.parts.push(data.fragment);
      if (fragments.parts.length !== fragments.count) continue;
      const complete = fragments;
      fragments = undefined;
      try {
        const decoded: unknown = JSON.parse(complete.parts.join(""));
        if (!decoded || typeof decoded !== "object" || Array.isArray(decoded))
          continue;
        data = decoded as typeof data;
        seq = complete.seq;
      } catch {
        continue;
      }
    } else fragments = undefined;
    const p = data.params;
    if (data.text) {
      rows.push({ key: seq, text: data.text });
      continue;
    }
    const item = p?.item;
    const key = `${p?.turnId || ""}:${p?.itemId || item?.id || "agent"}`;
    if (typeof p?.delta === "string") {
      const index = indexes.get(key);
      if (index === undefined) {
        indexes.set(key, rows.length);
        rows.push({ key: seq, text: p.delta });
      } else rows[index].text += p.delta;
    } else if (item?.type === "agentMessage" && item.text) {
      const index = indexes.get(key);
      if (index === undefined) {
        indexes.set(key, rows.length);
        rows.push({ key: seq, text: item.text });
      } else rows[index].text = item.text;
    }
  }
  return rows;
}

function QuestionForm({
  question: q,
  busy,
  answer,
}: {
  question: Question;
  busy: boolean;
  answer: (value: Record<string, unknown>) => Promise<unknown>;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const elicitation = q.method === "mcpServer/elicitation/request";
  const urlMode = elicitation && q.params.mode === "url";
  const unsupported =
    elicitation &&
    !["form", "openai/form", "openaiForm", "url"].includes(
      String(q.params.mode),
    );
  const [advanced, setAdvanced] = useState(
    elicitation && !urlMode ? '{"action":"accept","content":{}}' : "{}",
  );
  const [error, setError] = useState("");
  const params = q.params;
  const questions = (params.questions || []) as (
    | string
    | {
        id: string;
        header?: string;
        question: string;
        isSecret?: boolean;
        options?: { label: string; description?: string }[];
      }
  )[];
  const native = q.method === "item/tool/requestUserInput";
  const approval =
    q.method.endsWith("/requestApproval") &&
    q.method !== "item/permissions/requestApproval";
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    try {
      let value: Record<string, unknown>;
      if (native)
        value = {
          answers: Object.fromEntries(
            questions.map((entry) => {
              const question = entry as Exclude<typeof entry, string>;
              return [question.id, { answers: [values[question.id]] }];
            }),
          ),
        };
      else if (q.method === "code_factory_report_status")
        value = { answers: questions.map((_, i) => values[String(i)]) };
      else value = JSON.parse(advanced) as Record<string, unknown>;
      await answer(value);
    } catch (e) {
      setError(String(e));
    }
  }
  return (
    <form
      className="question"
      aria-label="Human input request"
      onSubmit={(e) => void submit(e)}
    >
      <h3>
        {q.method === "code_factory_report_status"
          ? String(params.summary || "Agent needs your input")
          : "Agent request"}
      </h3>
      {error && <p role="alert">{error}</p>}
      {elicitation && (
        <p>
          {String(
            params.message || params.description || "External input requested",
          )}
        </p>
      )}
      {unsupported && (
        <p>
          This request requires native host support. Decline or cancel it here;
          no verification proof is fabricated.
        </p>
      )}
      {urlMode && (
        <div className="row">
          <button
            type="button"
            onClick={() =>
              void openLink(String(params.url)).catch((e) =>
                setError(String(e)),
              )
            }
          >
            Open authorization
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => void answer({ action: "accept" })}
          >
            Continue after authorization
          </button>
        </div>
      )}
      {questions.map((entry, i) => {
        const question =
          typeof entry === "string"
            ? {
                id: String(i),
                question: entry,
                options: undefined,
                isSecret: false,
              }
            : entry;
        return (
          <fieldset key={question.id}>
            <legend>{question.question}</legend>
            {question.options?.map((option) => (
              <label key={option.label}>
                <input
                  type="radio"
                  name={`${q.id}:${question.id}`}
                  value={option.label}
                  checked={values[question.id] === option.label}
                  onChange={() =>
                    setValues((old) => ({
                      ...old,
                      [question.id]: option.label,
                    }))
                  }
                />
                {option.label}
                {option.description ? ` — ${option.description}` : ""}
              </label>
            ))}
            <label>
              Your answer
              <input
                required
                aria-label={`Answer: ${question.question}`}
                type={question.isSecret ? "password" : "text"}
                value={values[question.id] || ""}
                onChange={(e) =>
                  setValues((old) => ({
                    ...old,
                    [question.id]: e.target.value,
                  }))
                }
              />
            </label>
          </fieldset>
        );
      })}
      {approval ? (
        <>
          <pre>{JSON.stringify(params, null, 2)}</pre>
          <div className="row">
            {(Array.isArray(params.availableDecisions) &&
            params.availableDecisions.length
              ? params.availableDecisions
              : ["accept", "acceptForSession", "decline", "cancel"]
            ).map((decision) => (
              <button
                type="button"
                disabled={busy}
                key={JSON.stringify(decision)}
                onClick={() => void answer({ decision })}
              >
                {typeof decision === "string"
                  ? decision
                  : JSON.stringify(decision)}
              </button>
            ))}
          </div>
        </>
      ) : (
        <>
          {!questions.length && !urlMode && !unsupported && (
            <>
              <pre>{JSON.stringify(params, null, 2)}</pre>
              <label>
                Response JSON
                <textarea
                  value={advanced}
                  onChange={(e) => setAdvanced(e.target.value)}
                />
              </label>
            </>
          )}
          {!urlMode && !unsupported && (
            <button disabled={busy}>Send answer</button>
          )}
          {elicitation && (
            <div className="row">
              <button
                type="button"
                disabled={busy}
                onClick={() => void answer({ action: "decline" })}
              >
                Decline request
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => void answer({ action: "cancel" })}
              >
                Cancel request
              </button>
            </div>
          )}
        </>
      )}
    </form>
  );
}
createRoot(document.getElementById("root")!).render(<Factory />);
