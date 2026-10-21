import React, { useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { call, connect, openLink } from "./bridge";
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
} from "./types";
import "./style.css";

function Factory() {
  const [snap, setSnap] = useState<Snapshot>();
  const [tab, setTab] = useState("Inbox");
  const [error, setError] = useState("");
  const [connection, setConnection] = useState("Connecting to Work…");
  const [selected, setSelected] = useState("");
  const [runId, setRunId] = useState("");
  const [launch, setLaunch] = useState<Entry>();
  const [attentionId, setAttentionId] = useState("");
  const [filter, setFilter] = useState("open");
  const [search, setSearch] = useState("");
  const cursor = useRef(0);
  const mounted = useRef(true);
  const refresh = useCallback(async () => {
    const s = await call<Snapshot>("read_snapshot");
    if (mounted.current && s.cursor >= cursor.current) {
      setSnap(s);
      cursor.current = s.cursor;
      setConnection("Connected");
    }
  }, []);
  const action = useCallback(
    async (name: string, args: Record<string, unknown> = {}) => {
      let succeeded = true;
      try {
        await call(name, args);
        setError("");
      } catch (e) {
        setError(String(e));
        succeeded = false;
      }
      // Failed mutations can still change durable state, such as retiring a
      // session before dirty cleanup is refused. Keep that state visible.
      try {
        await refresh();
      } catch (e) {
        if (succeeded) setError(String(e));
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
        backoff = 1000;
      } catch (e) {
        if (!stopped) {
          setConnection("Disconnected · reconnecting");
          setError(String(e));
          backoff = Math.min(backoff * 2, 30000);
        }
      }
      inFlight = false;
      if (!stopped) timer = setTimeout(poll, document.hidden ? 15000 : backoff);
    }
    const snapRef = { current: false };
    void connect()
      .then(async () => {
        await poll();
      })
      .catch((e) => setError(String(e)));
    const timeout = setTimeout(() => {
      if (!snapRef.current)
        setError(
          "Setup blocker: open Code Factory inside a Work chat with MCP Apps enabled and a connected private tunnel.",
        );
    }, 10000);
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
      clearTimeout(timeout);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [refresh]);
  const pending = snap?.attention.filter((a) => a.status === "pending") ?? [];
  const currentAttention = pending.find((a) => a.id === attentionId);
  return (
    <main>
      <header>
        <div className="brand">
          <span className="mark">CF</span>
          <div>
            <h1>Code Factory</h1>
            <small>Your repositories. Your agents.</small>
          </div>
        </div>
        <span
          className="connection"
          role="status"
          aria-label="Connection status"
          aria-live="polite"
          aria-atomic="true"
        >
          ● {connection}
        </span>
      </header>
      <nav>
        {["Inbox", "Agents", "Settings"].map((t) => (
          <button
            key={t}
            aria-current={tab === t ? "page" : undefined}
            onClick={() => setTab(t)}
          >
            {t}
            {t === "Agents" && pending.length > 0 && (
              <b className="badge">{pending.length}</b>
            )}
          </button>
        ))}
        <button
          className="attention"
          onClick={() => {
            setTab("Agents");
            if (pending[0]) setAttentionId(pending[0].id);
          }}
        >
          Needs attention <b>{pending.length}</b>
        </button>
      </nav>
      {error && (
        <div role="alert" className="error">
          {error}
          <button onClick={() => setError("")} aria-label="Dismiss error">
            ×
          </button>
        </div>
      )}
      {!snap ? (
        <section className="empty">
          Connecting to your local service…
          <p>
            Work computer access and a private MCP tunnel are required during
            setup.
          </p>
        </section>
      ) : (
        <>
          {tab === "Inbox" && (
            <>
              <div className="toolbar">
                <div>
                  <h2>Repository inbox</h2>
                  <small>
                    Synced every 30 seconds · opening an item marks its
                    displayed revision read
                  </small>
                </div>
                <input
                  aria-label="Search inbox"
                  placeholder="Search issues and pull requests"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                />
                <select
                  aria-label="Inbox filter"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                >
                  {["open", "all", "new", "unread", "acted-on"].map((f) => (
                    <option key={f}>{f}</option>
                  ))}
                </select>
              </div>
              <div className="syncs">
                {snap.repositories.map((r) => (
                  <span key={r.id}>
                    {r.id} ·{" "}
                    {r.error ||
                      (!r.lastSync.startsWith("0001")
                        ? new Date(r.lastSync).toLocaleTimeString()
                        : "Waiting for first sync")}
                  </span>
                ))}
              </div>
              <div className="split">
                <section className="item-list">
                  {snap.inbox
                    .filter(
                      (v) =>
                        (filter === "all" ||
                          (filter === "open" && v.item.state === "open") ||
                          (filter === "new" && v.user.new) ||
                          (filter === "unread" && v.unread) ||
                          (filter === "acted-on" &&
                            v.user.actions.length > 0)) &&
                        `${v.item.title} ${v.item.id}`
                          .toLowerCase()
                          .includes(search.toLowerCase()),
                    )
                    .map((v) => (
                      <button
                        className={`item ${selected === v.item.id ? "selected" : ""}`}
                        key={v.item.id}
                        onClick={() => setSelected(v.item.id)}
                      >
                        <div className="item-meta">
                          <span>
                            {v.item.kind === "pr" ? "↗ PR" : "○ Issue"} ·{" "}
                            {v.item.id}
                          </span>
                          <b className={`priority p${v.priority}`}>
                            {v.priority === null ? "Pending" : `P${v.priority}`}
                          </b>
                        </div>
                        <h3>
                          {v.unread && <span className="dot" />}
                          {v.item.title}
                        </h3>
                        <div className="item-meta">
                          <span>
                            {v.item.author} · {v.item.state}
                            {v.item.draft ? " · draft" : ""}
                          </span>
                          <span>
                            {v.user.new && <b className="badge">New</b>}
                            {v.user.actions.length > 0 ? " Acted on" : ""}
                          </span>
                        </div>
                      </button>
                    ))}
                  {!snap.inbox.length && (
                    <div className="empty">
                      Your inbox starts here.
                      <p>
                        Add a GitHub repository in Settings. Existing items
                        arrive unread; later creations get a New badge.
                      </p>
                      <button onClick={() => setTab("Settings")}>
                        Add repository
                      </button>
                    </div>
                  )}
                </section>
                <section className="detail">
                  {selected ? (
                    <ItemDetail
                      id={selected}
                      entry={snap.inbox.find((e) => e.item.id === selected)}
                      action={action}
                      onLaunch={setLaunch}
                      onError={setError}
                    />
                  ) : (
                    <div className="empty">
                      Select an issue or pull request to triage it.
                    </div>
                  )}
                </section>
              </div>
            </>
          )}
          {tab === "Agents" && (
            <div className="split">
              <section className="item-list">
                <h2>Local agents</h2>
                {snap.runs
                  .filter((r) => r.status !== "prepared")
                  .map((r) => (
                    <button
                      className={`item ${runId === r.id ? "selected" : ""}`}
                      key={r.id}
                      onClick={() => setRunId(r.id)}
                    >
                      <h3>
                        {snap.inbox.find((e) => e.item.id === r.itemId)?.item
                          .title ?? r.itemId}
                      </h3>
                      <span>
                        {r.profile.name} · {r.status}
                      </span>
                      {pending.some((a) => a.runId === r.id) && (
                        <b className="badge">Needs attention</b>
                      )}
                    </button>
                  ))}
                {snap.runs.filter((r) => r.status !== "prepared").length ===
                  0 && (
                  <p className="empty">Launch an agent from an issue or PR.</p>
                )}
              </section>
              <section className="detail">
                {snap.runs.find((r) => r.id === runId) ? (
                  <Agent
                    key={runId}
                    run={snap.runs.find((r) => r.id === runId)!}
                    attention={pending.filter((a) => a.runId === runId)}
                    action={action}
                    openAttention={setAttentionId}
                    onError={setError}
                  />
                ) : (
                  <div className="empty">
                    Reopen an agent to see progress and send a follow-up.
                  </div>
                )}
              </section>
            </div>
          )}
          {tab === "Settings" && (
            <Settings snap={snap} action={action} onError={setError} />
          )}
          {launch && (
            <Launch
              entry={launch}
              snap={snap}
              close={() => setLaunch(undefined)}
              started={(id) => {
                setLaunch(undefined);
                setRunId(id);
                setTab("Agents");
                void refresh();
              }}
            />
          )}
          {currentAttention && (
            <AttentionDialog
              attention={currentAttention}
              close={() => setAttentionId("")}
              answer={async (answer) => {
                await call("answer_request", {
                  id: currentAttention.id,
                  answer,
                });
                await refresh();
                setAttentionId("");
              }}
              chat={() => {
                setRunId(currentAttention.runId);
                setTab("Agents");
                setAttentionId("");
              }}
            />
          )}
        </>
      )}
      <footer>
        Runs stay active when this view closes. Your computer must remain
        online.
      </footer>
    </main>
  );
}
type Action = (
  name: string,
  args?: Record<string, unknown>,
) => Promise<boolean>;
function ItemDetail({
  id,
  entry,
  action,
  onLaunch,
  onError,
}: {
  id: string;
  entry?: Entry;
  action: Action;
  onLaunch: (e: Entry) => void;
  onError: (e: string) => void;
}) {
  const [detail, setDetail] = useState<Detail>();
  const [target, setTarget] = useState("");
  const [priority, setPriority] = useState("");
  const [detailVersion, setDetailVersion] = useState(0);
  const selectedId = useRef(id);
  selectedId.current = id;
  async function mutate(name: string, args: Record<string, unknown>) {
    const changedId = id;
    const succeeded = await action(name, args);
    if (!succeeded || selectedId.current !== changedId) return false;
    setDetailVersion((version) => version + 1);
    return true;
  }
  useEffect(() => {
    let alive = true;
    void call<Detail>("read_item", { id })
      .then((d) => {
        if (alive) setDetail(d);
      })
      .catch((e) => {
        if (alive) onError(String(e));
      });
    return () => {
      alive = false;
    };
  }, [id, entry?.item.revision, detailVersion, onError]);
  useEffect(() => {
    if (detail?.item.id === id)
      void action("item_action", {
        id,
        action: "read",
        revision: detail.item.revision,
      });
  }, [detail?.item.id, detail?.item.revision, id, action]);
  if (!detail || detail.item.id !== id || !entry) return <p>Loading item…</p>;
  const i = detail.item;
  return (
    <>
      <div className="item-meta">
        {i.id} · {i.state} · {i.kind}
      </div>
      <h2>{i.title}</h2>
      <div className="buttons">
        <button className="primary" onClick={() => onLaunch(entry)}>
          Launch agent
        </button>
        <button
          onClick={() => void mutate("item_action", { id, action: "unread" })}
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
          onClick={() => void mutate("item_action", { id, action: "acted" })}
        >
          Record action
        </button>
      </div>
      <p className="body">{i.body || "No description."}</p>
      <h3>
        Priority {entry.priority === null ? "pending" : `P${entry.priority}`}
      </h3>
      <p>
        {entry.prioritySource} ·{" "}
        {entry.assessment.rationale ||
          entry.assessment.error ||
          entry.assessment.status}
      </p>
      <div className="buttons">
        <select
          aria-label="Priority override"
          value={priority}
          onChange={(e) => setPriority(e.target.value)}
        >
          <option value="">Use labels or inference</option>
          {[0, 1, 2, 3].map((p) => (
            <option key={p} value={p}>
              P{p}
            </option>
          ))}
        </select>
        <button
          onClick={() =>
            void mutate("item_action", {
              id,
              action: "priority",
              priority: priority === "" ? null : Number(priority),
            })
          }
        >
          Save priority
        </button>
      </div>
      <h3>Relationships</h3>
      {detail.relationships.map((l) => (
        <p key={l.id}>
          {l.target}{" "}
          <small>{l.manual ? "Local link" : `Reference: ${l.evidence}`}</small>
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
                setTarget((current) => (current === submitted ? "" : current));
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
      {i.checks && (
        <details>
          <summary>Checks</summary>
          <pre>{JSON.stringify(i.checks, null, 2)}</pre>
        </details>
      )}
      <h3>Activity</h3>
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
      <h3>Your action history</h3>
      {detail.user.actions.map((a) => (
        <p key={a.id}>
          {a.kind} · {new Date(a.at).toLocaleString()}
        </p>
      ))}
    </>
  );
}
function Launch({
  entry,
  snap,
  close,
  started,
}: {
  entry: Entry;
  snap: Snapshot;
  close: () => void;
  started: (id: string) => void;
}) {
  const [profileId, setProfileId] = useState(snap.profiles[0]?.id ?? "");
  const p = snap.profiles.find((p) => p.id === profileId)!;
  const expand = (p: Profile) =>
    p.prompt
      .replaceAll("{{title}}", entry.item.title)
      .replaceAll("{{body}}", entry.item.body)
      .replaceAll("{{url}}", entry.item.url);
  const [prompt, setPrompt] = useState(p ? expand(p) : "");
  const [model, setModel] = useState(p?.model ?? "");
  const [effort, setEffort] = useState(p?.effort ?? "");
  const [skillIds, setSkills] = useState<string[]>(p?.skills ?? []);
  const [base, setBase] = useState("");
  const [prepared, setPrepared] = useState<Run>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useModalFocus(close);
  async function prepare() {
    setBusy(true);
    setError("");
    try {
      setPrepared(
        await call<Run>("prepare_launch", {
          itemId: entry.item.id,
          profileId,
          prompt,
          model,
          effort,
          skillIds,
          base,
        }),
      );
    } catch (e) {
      setError(String(e));
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
        <h2>{prepared ? "Review agent launch" : "Launch agent"}</h2>
        {error && <p role="alert">{error}</p>}
        <p>
          {entry.item.id} · {entry.item.title}
        </p>
        {!prepared ? (
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
                  entry.item.kind === "pr" ? "PR head" : "Repository default"
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
            <p>
              Workspace: a dedicated worktree · {p.sandbox} · approvals:{" "}
              {p.approval}
            </p>
            <p>
              Publishing:{" "}
              {p.publishing === "ask"
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
          <>
            <dl>
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
                onClick={() => {
                  setPrepared(undefined);
                  setError("");
                }}
              >
                Edit launch
              </button>
              <button
                className="primary"
                disabled={busy}
                onClick={async () => {
                  setBusy(true);
                  setError("");
                  try {
                    const r = await call<Run>("start_run", { id: prepared.id });
                    started(r.id);
                  } catch (e) {
                    setError(String(e));
                    setBusy(false);
                  }
                }}
              >
                Start agent
              </button>
            </div>
          </>
        )}
      </section>
    </div>
  );
}
function transcriptBlocks(events: JournalEvent[]) {
  const blocks: { key: number; id: string; label: string; text: string }[] = [];
  for (const e of events) {
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
    if (m.includes("agentMessage/delta")) {
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
      label = "You";
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
      text = JSON.stringify(p.error ?? p);
    }
    if (!label || !text) continue;
    const id = String(p.itemId ?? item?.id ?? "");
    const existing = id
      ? blocks.find((b) => b.id === id && b.label === label)
      : undefined;
    if (existing) {
      if (m.endsWith("/delta") || m.endsWith("Delta")) existing.text += text;
      else existing.text = text;
    } else blocks.push({ key: e.seq, id, label, text });
  }
  return blocks;
}

function Agent({
  run,
  attention,
  action,
  openAttention,
  onError,
}: {
  run: Run;
  attention: Attention[];
  action: Action;
  openAttention: (id: string) => void;
  onError: (e: string) => void;
}) {
  const [events, setEvents] = useState<JournalEvent[]>([]);
  const [cursor, setCursor] = useState(0);
  const [hasMore, setMore] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const pageCursor = useRef(0);
  const [historyVersion, setHistoryVersion] = useState(0);
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
          delay = 1000;
        }
      } catch (e) {
        if (!stop) onError(String(e));
        delay = Math.min(delay * 2, 30000);
      }
      if (!stop) timer = setTimeout(load, document.hidden ? 15000 : delay);
    }
    void load();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [run.id, onError, historyVersion]);
  return (
    <>
      <span
        className="status"
        role="status"
        aria-label="Run status"
        aria-live="polite"
        aria-atomic="true"
      >
        {run.status}
      </span>
      <h2>{run.profile.name}</h2>
      <p>
        <code>{run.worktree}</code>
      </p>
      {run.error && <p role="alert">{run.error}</p>}
      <div className="buttons">
        {run.turnId && (
          <button onClick={() => void action("interrupt_run", { id: run.id })}>
            Interrupt
          </button>
        )}
        {["interrupted", "failed"].includes(run.status) && (
          <button
            onClick={() =>
              void action("start_run", { id: run.id, resume: true })
            }
          >
            Resume agent
          </button>
        )}
        {["idle", "interrupted", "failed"].includes(run.status) && (
          <button onClick={() => void action("cleanup_run", { id: run.id })}>
            Clean up worktree
          </button>
        )}
      </div>
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
      <h3>Conversation & progress</h3>
      <div className="transcript" aria-live="polite">
        {transcriptBlocks(events).map((b) => (
          <article key={b.key}>
            <small>{b.label}</small>
            <pre>{b.text}</pre>
          </article>
        ))}
        {!events.length && <p>Waiting for agent output…</p>}
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
          setBusy(true);
          try {
            await call("send_message", {
              id: run.id,
              text: message,
              expectedTurn: run.turnId,
            });
            setMessage("");
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
            onChange={(e) => setMessage(e.target.value)}
            rows={3}
          />
        </label>
        <button
          className="primary"
          disabled={
            busy ||
            !message.trim() ||
            !["idle", "running", "waiting"].includes(run.status)
          }
        >
          Send message
        </button>
      </form>
    </>
  );
}
function AttentionDialog({
  attention: a,
  close,
  answer,
  chat,
}: {
  attention: Attention;
  close: () => void;
  answer: (value: unknown) => Promise<void>;
  chat: () => void;
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
        <p>
          {a.blocking
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
            <pre>{JSON.stringify(a.params, null, 2)}</pre>
            <div className="buttons">
              {a.method.includes("permissions") ? (
                <>
                  <button
                    disabled={busy}
                    onClick={() =>
                      void submit({ permissions: {}, scope: "turn" })
                    }
                  >
                    Deny permissions
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
                    Approve requested permissions for this turn
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
                    {typeof d === "string" ? d : JSON.stringify(d)}
                  </button>
                ))
              )}
            </div>
          </>
        )}
        <button onClick={chat}>Open agent chat</button>
      </section>
    </div>
  );
}
function Settings({
  snap,
  action,
  onError,
}: {
  snap: Snapshot;
  action: Action;
  onError: (e: string) => void;
}) {
  const [config, setConfig] = useState<Config>(snap.config);
  const [labels, setLabels] = useState(
    JSON.stringify(snap.config.priorityLabels, null, 2),
  );
  const [repo, setRepo] = useState("");
  const [checkout, setCheckout] = useState("");
  const [base, setBase] = useState("");
  const [token, setToken] = useState("");
  const [profile, setProfile] = useState<Profile>(snap.profiles[0]);
  const [skillName, setSkillName] = useState("");
  const [skillContent, setSkillContent] = useState(
    "---\nname: my-skill\ndescription: Explain when this skill applies.\n---\n\n",
  );
  const [diagnostics, setDiagnostics] =
    useState<import("./types").Diagnostics>();
  return (
    <div className="settings">
      <section>
        <h2>Repositories</h2>
        {snap.repositories.map((r) => (
          <p key={r.id}>
            {r.id} · {r.checkout || "Managed clone"} · {r.error || "Connected"}
          </p>
        ))}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void action("save_repository", { id: repo, checkout, base });
          }}
        >
          <label>
            GitHub repository
            <input
              placeholder="owner/repository"
              required
              value={repo}
              onChange={(e) => setRepo(e.target.value)}
            />
          </label>
          <label>
            Existing checkout (optional)
            <input
              placeholder="/home/you/projects/repository"
              value={checkout}
              onChange={(e) => setCheckout(e.target.value)}
            />
          </label>
          <label>
            Default base (optional)
            <input
              placeholder="HEAD"
              value={base}
              onChange={(e) => setBase(e.target.value)}
            />
          </label>
          <button>Save repository</button>
        </form>
        <button
          onClick={() =>
            void action("discover_skills", { cwd: checkout || "/" })
          }
        >
          Discover local skills
        </button>
      </section>
      <section>
        <h2>Priorities & notifications</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            try {
              void action("save_settings", {
                ...config,
                priorityLabels: JSON.parse(labels),
              });
            } catch (e) {
              onError(String(e));
            }
          }}
        >
          <label>
            GitHub priority label mapping
            <textarea
              value={labels}
              onChange={(e) => setLabels(e.target.value)}
              rows={5}
            />
          </label>
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
            Jira Cloud URL
            <input
              placeholder="https://your-team.atlassian.net"
              value={config.jiraUrl}
              onChange={(e) =>
                setConfig({ ...config, jiraUrl: e.target.value })
              }
            />
          </label>
          <label>
            Jira account email
            <input
              type="email"
              value={config.jiraEmail}
              onChange={(e) =>
                setConfig({ ...config, jiraEmail: e.target.value })
              }
            />
          </label>
          <button>Save settings</button>
        </form>
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              await call("set_jira_credential", { token });
              setToken("");
            } catch (e) {
              onError(String(e));
            }
          }}
        >
          <label>
            Jira API token
            <input
              type="password"
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              required
            />
          </label>
          <button>Save credential securely</button>
        </form>
        <small>
          Read-only access. Tokens are stored locally and never returned in tool
          results.
        </small>
      </section>
      <section>
        <h2>Launch profiles</h2>
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
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void action("save_profile", { ...profile });
          }}
        >
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
              </label>
            ))}
          </fieldset>
          <button>Save profile</button>
        </form>
      </section>
      <section>
        <h2>Managed skills</h2>
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
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void action("save_skill", {
              name: skillName,
              content: skillContent,
            });
          }}
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
        </form>
      </section>
      <section>
        <h2>Connection diagnostics</h2>
        <button
          onClick={() =>
            void call<import("./types").Diagnostics>("diagnostics")
              .then(setDiagnostics)
              .catch((e) => onError(String(e)))
          }
        >
          Check connection
        </button>
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
      </section>
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
