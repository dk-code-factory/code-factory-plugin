import React, { useState, useRef } from "react";
import { call } from "./bridge";
import { PluginAdmin, RepositoryAdmin, SystemReviewSettings } from "./admin";
import type { Config, Profile, Snapshot, Skill } from "./types";
type Action = (
  name: string,
  args?: Record<string, unknown>,
  failed?: (message: string) => void,
) => Promise<boolean>;
function SaveForm({
  save,
  children,
  disabled = false,
}: {
  save: () => Promise<void>;
  children: React.ReactNode;
  disabled?: boolean;
}) {
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        if (busy || disabled) return;
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
      <fieldset className="form-fields" disabled={busy || disabled}>
        {children}
      </fieldset>
      {status && <p role="status">{status}</p>}
      {error && <p role="alert">{error}</p>}
    </form>
  );
}
export default function Settings({
  snap,
  action,
}: {
  snap: Snapshot;
  action: Action;
}) {
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
  const [selectedSkill, setSelectedSkill] = useState("");
  const [skillLoading, setSkillLoading] = useState(false);
  const [skillError, setSkillError] = useState("");
  const skillRequest = useRef(0);
  const [skillContent, setSkillContent] = useState(
    "---\nname: my-skill\ndescription: Explain when this skill applies.\n---\n\n",
  );
  async function loadSkill(id: string) {
    const generation = ++skillRequest.current;
    setSelectedSkill(id);
    setSkillError("");
    if (!id) {
      setSkillName("");
      setSkillContent(
        "---\nname: my-skill\ndescription: Explain when this skill applies.\n---\n\n",
      );
      setSkillLoading(false);
      return;
    }
    setSkillLoading(true);
    try {
      const { skill } = await call<{ skill: Skill }>("read_ui_state", {
        views: ["skill"],
        id,
      });
      if (generation !== skillRequest.current) return;
      setSkillName(skill.name);
      setSkillContent(skill.content);
    } catch (error) {
      if (generation === skillRequest.current)
        setSkillError("Could not load skill: " + String(error));
    } finally {
      if (generation === skillRequest.current) setSkillLoading(false);
    }
  }
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
            skills={snap.skills}
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
          value={selectedSkill}
          onChange={(e) => void loadSkill(e.target.value)}
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
        {selectedSkill && (
          <button
            disabled={skillLoading}
            onClick={() => void loadSkill(selectedSkill)}
          >
            Reload selected skill
          </button>
        )}
        {skillLoading && <p role="status">Loading managed skill…</p>}
        {skillError && <p role="alert">{skillError}</p>}
        <SaveForm
          disabled={skillLoading || Boolean(skillError)}
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
              aria-label="Skill instructions"
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
