import React, { useCallback, useEffect, useState } from "react";
import { call } from "./bridge";
import type { Config, Profile, Repository, Snapshot } from "./types";

interface PluginRow {
  id: string;
  name: string;
  marketplace: string;
  installed: boolean;
  enabled: boolean;
  installedVersion?: string;
  availableVersion?: string;
  updateStatus: string;
  installPolicy?: string;
  authPolicy?: string;
  sourceType?: string;
}
interface Operation {
  id: string;
  action: string;
  target: string;
  status: string;
  error?: string;
}
interface AdminState {
  inventory: {
    checkedAt: string;
    error?: string;
    plugins: PluginRow[];
    marketplaces: { name: string; sourceType?: string }[];
  };
  operations: Operation[];
  total: number;
  offset: number;
}
export function PluginAdmin({ skills }: { skills: Snapshot["skills"] }) {
  const [state, setState] = useState<AdminState>();
  const [query, setQuery] = useState("");
  const [available, setAvailable] = useState(false);
  const [offset, setOffset] = useState(0);
  const [source, setSource] = useState("");
  const [ref, setRef] = useState("");
  const [selector, setSelector] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [request, setRequest] = useState<Operation>();
  const load = useCallback(
    () =>
      call<AdminState>("read_plugin_admin", {
        query,
        available,
        offset,
        limit: 40,
      }),
    [query, available, offset],
  );
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      let busy = false;
      try {
        const next = await load();
        if (!stop) {
          setState(next);
          busy = next.operations.some((op) =>
            ["queued", "running"].includes(op.status),
          );
          if (request && next.operations.some((op) => op.id === request.id))
            setRequest(undefined);
        }
      } catch (e) {
        if (!stop) setError(`Could not read plugin status: ${String(e)}`);
      }
      if (!stop) timer = setTimeout(refresh, busy ? 1000 : 10000);
    }
    void refresh();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [load, request]);
  const busy =
    submitting ||
    !!request ||
    state?.operations.some((op) => ["queued", "running"].includes(op.status));
  async function manage(action: string, target = "", ref = "") {
    if (busy) return;
    setSubmitting(true);
    setError("");
    try {
      const operation = await call<Operation>("manage_codex_plugins", {
        action,
        target,
        ref,
      });
      setRequest(operation);
    } catch (e) {
      setError(
        `${String(e)} Check operation status before trying again. No automatic replay was performed.`,
      );
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <section aria-label="Codex plugin administration">
      <p>
        Manage plugins for the pinned Codex provider. Installing a plugin can
        add skills, tools and hooks. Active tasks keep their current skill
        snapshots; maintenance waits until agent work is inactive.
      </p>
      <div className="buttons">
        <button disabled={busy} onClick={() => void manage("refresh")}>
          Refresh plugin inventory
        </button>
        <button disabled={busy} onClick={() => void manage("check-updates")}>
          Check for plugin updates
        </button>
        <button disabled={busy} onClick={() => void manage("discover-skills")}>
          Discover pipeline skills
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
      {state?.inventory.error && <p role="alert">{state.inventory.error}</p>}
      <p role="status">
        {busy
          ? "Plugin maintenance in progress…"
          : state?.inventory.checkedAt &&
              !state.inventory.checkedAt.startsWith("0001")
            ? `Inventory checked ${new Date(state.inventory.checkedAt).toLocaleString()}`
            : "Plugin inventory has not been checked yet."}
      </p>
      <label>
        Search Codex plugins
        <input
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setOffset(0);
          }}
        />
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={available}
          onChange={(e) => {
            setAvailable(e.target.checked);
            setOffset(0);
          }}
        />
        Include available plugins
      </label>
      <p>{state?.total ?? 0} matching plugins</p>
      <div className="plugin-list">
        {state?.inventory.plugins.map((p) => (
          <article key={p.id} className="plugin-row">
            <h3>{p.name}</h3>
            <p>
              <code>{p.id}</code>
            </p>
            <p>
              {p.installed
                ? p.enabled
                  ? "Installed · enabled"
                  : "Installed · disabled"
                : "Not installed"}
            </p>
            <p>
              Installed version: {p.installedVersion || "—"} · Available
              version: {p.availableVersion || "Unknown"}
            </p>
            <p>
              {p.updateStatus === "available"
                ? "Update available"
                : p.updateStatus === "current"
                  ? "Matches checked marketplace version"
                  : p.installed
                    ? "Update status unavailable from this marketplace; Update installs its current release."
                    : "Available to install"}
            </p>
            <small>
              Source: {p.sourceType || "Unknown"} · Install policy:{" "}
              {p.installPolicy || "Unknown"} · Authentication policy:{" "}
              {p.authPolicy || "Unknown"}. Authentication readiness is checked
              by Codex when the plugin is used.
            </small>
            <div className="buttons">
              {p.installed ? (
                <>
                  <button
                    disabled={busy}
                    onClick={() => void manage("update", p.id)}
                  >
                    Update {p.name}
                  </button>
                  <button
                    disabled={busy}
                    onClick={() => void manage("remove", p.id)}
                  >
                    Remove {p.name}
                  </button>
                </>
              ) : (
                <button
                  disabled={busy}
                  onClick={() => void manage("install", p.id)}
                >
                  Install {p.name}
                </button>
              )}
            </div>
          </article>
        ))}
      </div>
      <div className="buttons">
        <button
          disabled={offset === 0}
          onClick={() => setOffset(Math.max(0, offset - 40))}
        >
          Previous plugins
        </button>
        <button
          disabled={!state || offset + 40 >= state.total}
          onClick={() => setOffset(offset + 40)}
        >
          Next plugins
        </button>
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void manage("install", selector);
        }}
      >
        <label>
          Plugin selector
          <input
            required
            value={selector}
            onChange={(e) => setSelector(e.target.value)}
            placeholder="plugin-name@marketplace-name"
          />
        </label>
        <button disabled={busy}>Install selected plugin</button>
      </form>
      <h3>Marketplaces</h3>
      {state?.inventory.marketplaces.map((m) => (
        <p key={m.name}>
          {m.name} · {m.sourceType || "Discovered"}{" "}
          <button
            disabled={busy}
            onClick={() => void manage("remove-marketplace", m.name)}
          >
            Remove marketplace {m.name}
          </button>
        </p>
      ))}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void manage("add-marketplace", source, ref);
        }}
      >
        <label>
          Marketplace source
          <input
            required
            value={source}
            onChange={(e) => setSource(e.target.value)}
            placeholder="owner/repository, HTTPS/SSH Git URL, or absolute local path"
          />
        </label>
        <label>
          Marketplace Git ref (optional)
          <input value={ref} onChange={(e) => setRef(e.target.value)} />
        </label>
        <button disabled={busy}>Add marketplace</button>
      </form>
      <h3>Pipeline skills</h3>
      <p>
        {skills.length} discovered or managed skills. Select any of these under
        Launch profiles → Default skills for review or implementation. New
        launches snapshot the chosen skill and its supporting files.
      </p>
      <ul>
        {skills.map((s) => (
          <li key={s.id}>
            {s.name} · {s.managed ? "Managed" : "Discovered"}
          </li>
        ))}
      </ul>
      <h3>Recent operations</h3>
      <ul>
        {[...(request ? [request] : []), ...(state?.operations ?? [])]
          .filter((op, i, all) => all.findIndex((x) => x.id === op.id) === i)
          .map((op) => (
            <li key={op.id}>
              <strong>
                {op.action} · {op.status}
              </strong>{" "}
              {op.target}
              {op.error && <p role="alert">{op.error}</p>}
            </li>
          ))}
      </ul>
    </section>
  );
}

function PipelineSelect({
  label,
  value,
  profiles,
  change,
  inherit,
}: {
  label: string;
  value: string;
  profiles: Profile[];
  change: (value: string) => void;
  inherit: string;
}) {
  return (
    <label>
      {label}
      <select
        aria-label={label}
        value={value}
        onChange={(e) => change(e.target.value)}
      >
        <option value="">{inherit}</option>
        {profiles.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name} · {p.sandbox} · {p.publishing}
          </option>
        ))}
      </select>
    </label>
  );
}
export function RepositoryAdmin({
  snap,
  save,
}: {
  snap: Snapshot;
  save: (name: string, args: Record<string, unknown>) => Promise<void>;
}) {
  const empty = (): Repository => ({
    id: "",
    checkout: "",
    base: "",
    lastSync: "",
    error: "",
    autoReview: null,
    reviewPrompt: "",
    reviewProfileId: "",
    implementationProfileId: "",
    paused: false,
  });
  const [repo, setRepo] = useState<Repository>(empty);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [lastAction, setLastAction] = useState("save_repository");
  const defaultAuto = snap.config.autoReview ?? false;
  async function perform(action: string, args: Record<string, unknown>) {
    if (busy) return;
    setBusy(true);
    setError("");
    setLastAction(action);
    setStatus(action === "save_repository" ? "Saving…" : "Polling…");
    try {
      await save(action, args);
      setStatus(
        action === "sync_repositories"
          ? "Repository poll completed."
          : "Saved.",
      );
    } catch (e) {
      setStatus("");
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Repository monitoring">
      <p>
        Review notifications are checked on the system interval and repositories reconcile every 15 minutes using your GitHub
        sign-in. Review PRs includes requests for your user or teams. Adding a
        repository requires that account to have access.
      </p>
      {snap.viewer?.teamError && <p role="alert">{snap.viewer.teamError}</p>}
      {snap.repositories.map((r) => (
        <article key={r.id} className="repository-row">
          <strong>{r.id}</strong>
          <p>{r.checkout || "Managed clone"}</p>
          <p>
            {r.paused
              ? "Monitoring paused"
              : r.lastSync && !r.lastSync.startsWith("0001")
                ? `Last successful poll ${new Date(r.lastSync).toLocaleString()}`
                : "Waiting for first poll"}{" "}
            ·{" "}
            {(r.autoReview ?? defaultAuto) ? "Automatic review" : "Queue only"}
          </p>
          {r.error && <p role="alert">{r.error}</p>}
          {r.reviewError && <p role="alert">{r.reviewError}</p>}
          <button
            onClick={() => {
              setRepo({ ...r, autoReview: r.autoReview ?? null });
              setError("");
              setStatus("");
            }}
          >
            Edit repository {r.id}
          </button>
        </article>
      ))}
      <button onClick={() => setRepo(empty())}>Add another repository</button>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void perform("save_repository", {
            id: repo.id,
            checkout: repo.checkout,
            base: repo.base,
            paused: repo.paused ?? false,
            autoReview: repo.autoReview ?? null,
            reviewProfileId: repo.reviewProfileId ?? "",
            implementationProfileId: repo.implementationProfileId ?? "",
            reviewPrompt: repo.reviewPrompt ?? "",
          });
        }}
      >
        <fieldset className="form-fields" disabled={busy}>
          <label>
            GitHub repository
            <input
              required
              placeholder="owner/repository"
              value={repo.id}
              onChange={(e) => setRepo({ ...repo, id: e.target.value })}
              autoFocus={snap.repositories.length === 0}
            />
          </label>
          <label>
            Existing checkout (optional)
            <input
              placeholder="/home/you/projects/repository"
              value={repo.checkout}
              onChange={(e) => setRepo({ ...repo, checkout: e.target.value })}
            />
          </label>
          <label>
            Default base (optional)
            <input
              placeholder="HEAD"
              value={repo.base}
              onChange={(e) => setRepo({ ...repo, base: e.target.value })}
            />
          </label>
          <label>
            Repository review requests
            <select
              value={
                repo.autoReview == null ? "inherit" : String(repo.autoReview)
              }
              onChange={(e) =>
                setRepo({
                  ...repo,
                  autoReview:
                    e.target.value === "inherit"
                      ? null
                      : e.target.value === "true",
                })
              }
            >
              <option value="inherit">
                Inherit system default (
                {defaultAuto ? "Automatic" : "Queue only"})
              </option>
              <option value="false">Queue only</option>
              <option value="true">Automatically start review</option>
            </select>
          </label>
          <PipelineSelect
            label="Repository review pipeline"
            value={repo.reviewProfileId ?? ""}
            profiles={snap.profiles}
            change={(reviewProfileId) => setRepo({ ...repo, reviewProfileId })}
            inherit="Inherit system review pipeline"
          />
          <PipelineSelect
            label="Repository implementation pipeline"
            value={repo.implementationProfileId ?? ""}
            profiles={snap.profiles}
            change={(implementationProfileId) =>
              setRepo({ ...repo, implementationProfileId })
            }
            inherit="Inherit system implementation pipeline"
          />
          <label>
            Additional repository review prompt
            <textarea
              rows={4}
              value={repo.reviewPrompt ?? ""}
              onChange={(e) =>
                setRepo({ ...repo, reviewPrompt: e.target.value })
              }
              placeholder="Extra review instructions appended to the generic review prompt"
            />
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={repo.paused ?? false}
              onChange={(e) => setRepo({ ...repo, paused: e.target.checked })}
            />
            Pause repository monitoring
          </label>
          <p>
            Automatic reviews use the selected read-only pipeline and require
            approval before publishing. Polling does not automatically implement
            changes or post a review. A request is claimed once per PR head and
            request episode.
          </p>
          <button>Save repository</button>
        </fieldset>
        {lastAction === "save_repository" && status && (
          <p role="status">{status}</p>
        )}
        {lastAction === "save_repository" && error && (
          <p role="alert">{error}</p>
        )}
      </form>
      <button
        disabled={busy}
        onClick={() => void perform("sync_repositories", {})}
      >
        Poll repositories now
      </button>
      {lastAction !== "save_repository" && status && (
        <p role="status">{status}</p>
      )}
      {lastAction !== "save_repository" && error && <p role="alert">{error}</p>}
    </section>
  );
}
export function SystemReviewSettings({
  config,
  profiles,
  change,
  usage,
}: {
  usage?:import("./types").GitHubUsage;
  config: Config;
  profiles: Profile[];
  change: (c: Config) => void;
}) {
  return (
    <>
      <fieldset aria-label="GitHub API usage">
       <legend>GitHub API usage</legend>
       <label>Request budget per hour<input type="number" min={60} max={5000} value={config.githubRequestsPerHour??600} onChange={e=>change({...config,githubRequestsPerHour:Number(e.target.value)})}/></label>
       <label>Keep GitHub requests in reserve<input type="number" min={0} max={5000} value={config.githubReserve??100} onChange={e=>change({...config,githubReserve:Number(e.target.value)})}/></label>
       <label>Review notification poll interval<select aria-label="Review notification poll interval" value={config.githubPollSeconds??120} onChange={e=>change({...config,githubPollSeconds:Number(e.target.value)})}>{![60,120,300,900,3600].includes(config.githubPollSeconds??120)&&<option value={config.githubPollSeconds}>{config.githubPollSeconds} seconds</option>}<option value={60}>Every minute</option><option value={120}>Every 2 minutes</option><option value={300}>Every 5 minutes</option><option value={900}>Every 15 minutes</option><option value={3600}>Every hour</option></select></label>
       <p>Notifications are read without marking them read. Repositories reconcile every 15 minutes; full histories and checks refresh for selected or tracked work. Manual polls share the same budget. This budget covers Code Factory reads; plugins, skills and agent commands use GitHub independently.</p>
       {usage&&<p role="status">{usage.requestsLastHour} / {usage.budget} requests in the last hour. Reserve: {usage.reserve}. REST remaining: {usage.restRemaining??"Unknown"}; GraphQL remaining: {usage.graphqlRemaining??"Unknown"}.{usage.nextRequest&&!usage.nextRequest.startsWith("0001")&&new Date(usage.nextRequest).getTime()>Date.now()?` Deferred requests retry after ${new Date(usage.nextRequest).toLocaleString()}.`:""}</p>}
       {usage?.notificationError&&<p role="alert">Review notifications: {usage.notificationError}. Repository reconciliation remains available.</p>}
      </fieldset>
      <label>
        System default for review requests
        <select
          value={String(config.autoReview ?? false)}
          onChange={(e) =>
            change({ ...config, autoReview: e.target.value === "true" })
          }
        >
          <option value="false">Queue only</option>
          <option value="true">Automatically start review</option>
        </select>
      </label>
      <PipelineSelect
        label="System review pipeline"
        value={config.reviewProfileId ?? ""}
        profiles={profiles}
        change={(reviewProfileId) => change({ ...config, reviewProfileId })}
        inherit="PR review"
      />
      <PipelineSelect
        label="System implementation pipeline"
        value={config.implementationProfileId ?? ""}
        profiles={profiles}
        change={(implementationProfileId) =>
          change({ ...config, implementationProfileId })
        }
        inherit="Implementation"
      />
      <label>
        Plugin update check interval
        <select
          value={config.pluginCheckMinutes ?? 0}
          onChange={(e) =>
            change({ ...config, pluginCheckMinutes: Number(e.target.value) })
          }
        >
          <option value={0}>Manual checks</option>
          <option value={15}>Every 15 minutes</option>
          <option value={60}>Every hour</option>
          <option value={1440}>Every day</option>
        </select>
      </label>
      <p>
        Repositories can override these defaults. Update checks refresh
        marketplace metadata and report changed versions; installing an update
        remains explicit.
      </p>
    </>
  );
}
