"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { useSnowballAuth } from "../auth-boundary";
import { DevicePairing } from "./device-pairing";

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonObject | JsonValue[];
type JsonObject = { [key: string]: JsonValue };

type SettingDescriptor = {
  path: string;
  group: string;
  label: string;
  description: string;
  control: "text" | "textarea" | "number" | "toggle" | "select" | "list";
  minimum?: number;
  maximum?: number;
  options?: Array<{ label: string; value: string }>;
};

type SettingsResponse = {
  settings: JsonObject;
  schema: SettingDescriptor[];
};

type CommandResult = {
  matched: boolean;
  action?: string;
  target?: string;
  projectName?: string;
  voiceName?: string;
  prompt?: string;
  reason?: string;
};

type CandidateCatalog = {
  version: number;
  source: string;
  authenticated: boolean;
  voiceState: "available" | "requires_voice";
  voices: string[];
  projects: string[];
};

function valueAt(root: JsonObject, path: string): JsonValue | undefined {
  let current: JsonValue = root;
  for (const key of path.split(".")) {
    if (!current || Array.isArray(current) || typeof current !== "object") return undefined;
    current = current[key];
  }
  return current;
}

function withValue(root: JsonObject, path: string, value: JsonValue): JsonObject {
  const next = structuredClone(root);
  const keys = path.split(".");
  let current: JsonObject = next;
  keys.slice(0, -1).forEach((key) => {
    const child = current[key];
    if (!child || Array.isArray(child) || typeof child !== "object") current[key] = {};
    current = current[key] as JsonObject;
  });
  current[keys.at(-1) || path] = value;
  return next;
}

function displayValue(value: JsonValue | undefined) {
  if (Array.isArray(value)) return value.join(", ");
  if (typeof value === "string" || typeof value === "number") return value;
  return "";
}

export function AdminConsole() {
  const { request, logout } = useSnowballAuth();
  const [saved, setSaved] = useState<JsonObject | null>(null);
  const [draft, setDraft] = useState<JsonObject | null>(null);
  const [schema, setSchema] = useState<SettingDescriptor[]>([]);
  const [query, setQuery] = useState("");
  const [message, setMessage] = useState("Loading settings…");
  const [saving, setSaving] = useState(false);
  const [command, setCommand] = useState("ChatGPT Codex Project Snowball");
  const [commandResult, setCommandResult] = useState<CommandResult | null>(null);
  const [candidateCatalog, setCandidateCatalog] = useState<CandidateCatalog | null>(null);
  const [candidateMessage, setCandidateMessage] = useState("Loading visible ChatGPT candidates…");

  useEffect(() => {
    let cancelled = false;
    void request("/api/settings", { cache: "no-store" })
      .then(async (response) => {
        if (!response.ok) throw new Error("Could not load settings.");
        return response.json() as Promise<SettingsResponse>;
      })
      .then((data) => {
        if (cancelled) return;
        setSaved(data.settings);
        setDraft(structuredClone(data.settings));
        setSchema(data.schema);
        setMessage("");
      })
      .catch((error: unknown) => {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "Could not load settings.");
      });
    return () => { cancelled = true; };
  }, [request]);

  useEffect(() => {
    let cancelled = false;
    void request("/api/candidates", { cache: "no-store" })
      .then(async (response) => {
        const data = (await response.json().catch(() => ({}))) as { catalog?: CandidateCatalog; error?: string };
        if (!response.ok || !data.catalog) throw new Error(data.error || "Could not load visible candidates.");
        if (!cancelled) {
          setCandidateCatalog(data.catalog);
          setCandidateMessage("");
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) setCandidateMessage(error instanceof Error ? error.message : "Could not load visible candidates.");
      });
    return () => { cancelled = true; };
  }, [request]);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return schema;
    return schema.filter((field) => `${field.group} ${field.label} ${field.description} ${field.path}`.toLocaleLowerCase().includes(normalized));
  }, [query, schema]);

  const groups = useMemo(() => {
    const result = new Map<string, SettingDescriptor[]>();
    filtered.forEach((field) => result.set(field.group, [...(result.get(field.group) || []), field]));
    return [...result.entries()];
  }, [filtered]);

  const dirty = Boolean(saved && draft && JSON.stringify(saved) !== JSON.stringify(draft));

  function update(field: SettingDescriptor, raw: string | boolean) {
    if (!draft) return;
    let value: JsonValue = raw;
    if (field.control === "number") value = Number(raw);
    if (field.control === "list" && typeof raw === "string") value = raw.split(",").map((item) => item.trim()).filter(Boolean);
    setDraft(withValue(draft, field.path, value));
  }

  async function save() {
    if (!draft) return;
    setSaving(true);
    setMessage("");
    try {
      const response = await request("/api/settings", {
        method: "PUT",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(draft),
      });
      const data = (await response.json().catch(() => ({}))) as Partial<SettingsResponse> & { error?: string };
      if (!response.ok || !data.settings) throw new Error(data.error || "Could not save settings.");
      setSaved(data.settings);
      setDraft(structuredClone(data.settings));
      if (data.schema) setSchema(data.schema);
      setMessage("Settings saved.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not save settings.");
    } finally {
      setSaving(false);
    }
  }

  async function testCommand(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const response = await request("/api/commands/interpret", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ transcript: command }),
    });
    const result = (await response.json().catch(() => ({ reason: "Command test failed." }))) as CommandResult;
    setCommandResult(result);
  }

  return (
    <main className="admin-shell">
      <header className="admin-topbar">
        <Link className="wordmark" href="/" aria-label="Snowball-Voice-Gate home">
          <span className="snow-mark" aria-hidden="true"><i /><i /><i /></span>
          <span>Snowball-Voice-Gate</span>
        </Link>
        <nav aria-label="Admin navigation">
          <Link href="/">Voice console</Link>
          <button type="button" onClick={() => void logout()}>Sign out</button>
        </nav>
      </header>

      <section className="admin-hero">
        <div>
          <p className="eyebrow">LOCAL ADMIN</p>
          <h1>Gateway settings</h1>
          <p>Search and change voice commands, project prompts, turn timing, and future client discovery from one place.</p>
        </div>
        <div className="security-badge"><span /> Protected local session</div>
      </section>

      <section className="admin-summary" aria-label="Security summary">
        <article><strong>Password gate</strong><span>Voice API and Browser Console</span></article>
        <article><strong>Request protection</strong><span>Strict cookie + CSRF token</span></article>
        <article><strong>Private state</strong><span>Stored in the protected data volume</span></article>
      </section>

      <DevicePairing />

      <section className="candidate-catalog" aria-labelledby="candidate-title">
        <div>
          <p className="card-kicker">AUTHENTICATED CANDIDATES</p>
          <h2 id="candidate-title">Visible ChatGPT names</h2>
          <p>These are the only voice and project names the Gateway will accept from the board. The board receives a bounded signed snapshot; it never ships production names in firmware.</p>
        </div>
        {candidateMessage && <p className="admin-message" role="status">{candidateMessage}</p>}
        {candidateCatalog && (
          <div className="candidate-columns">
            <div><strong>Projects</strong><span>{candidateCatalog.projects.length ? candidateCatalog.projects.join(", ") : "None visible"}</span></div>
            <div><strong>Voices</strong><span>{candidateCatalog.voiceState === "requires_voice" ? "Start Voice to inspect the picker" : candidateCatalog.voices.join(", ") || "None visible"}</span></div>
          </div>
        )}
      </section>

      <section className="command-tester" aria-labelledby="command-title">
        <div>
          <p className="card-kicker">WAKE COMMAND TESTER</p>
          <h2 id="command-title">Check a phrase before using it.</h2>
          <p>The gateway parser gives explicit ChatGPT Project precedence unless “Codex Project” is spoken.</p>
        </div>
        <form onSubmit={testCommand}>
          <label htmlFor="command-input">Transcript</label>
          <div><input id="command-input" value={command} onChange={(event) => setCommand(event.target.value)} /><button type="submit">Interpret</button></div>
        </form>
        {commandResult && (
          <output className={commandResult.matched ? "matched" : "unmatched"}>
            {commandResult.action || "not matched"}
            {commandResult.target && ` · ${commandResult.target}`}
            {commandResult.projectName && ` · ${commandResult.projectName}`}
            {commandResult.voiceName && ` · voice ${commandResult.voiceName}`}
            {commandResult.reason && ` · ${commandResult.reason}`}
          </output>
        )}
      </section>

      <section className="settings-toolbar">
        <label>
          <span>Search settings</span>
          <input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Wake word, Korean prompt, silence…" />
        </label>
        <div>
          <button type="button" className="secondary-button" disabled={!dirty || saving} onClick={() => saved && setDraft(structuredClone(saved))}>Discard</button>
          <button type="button" className="save-button" disabled={!dirty || saving} onClick={() => void save()}>{saving ? "Saving…" : "Save changes"}</button>
        </div>
      </section>

      {message && <p className="admin-message" role="status">{message}</p>}
      {draft && groups.map(([group, fields]) => (
        <section className="settings-group" key={group} aria-labelledby={`group-${group.replaceAll(" ", "-")}`}>
          <div className="settings-group-title">
            <h2 id={`group-${group.replaceAll(" ", "-")}`}>{group}</h2>
            <span>{fields.length} settings</span>
          </div>
          <div className="settings-fields">
            {fields.map((field) => {
              const id = `setting-${field.path.replaceAll(".", "-")}`;
              const current = valueAt(draft, field.path);
              return (
                <label className={`setting-field ${field.control === "toggle" ? "toggle-field" : ""}`} key={field.path} htmlFor={id}>
                  <span className="setting-copy"><strong>{field.label}</strong><small>{field.description}</small></span>
                  {field.control === "toggle" ? (
                    <input id={id} type="checkbox" checked={current === true} onChange={(event) => update(field, event.target.checked)} />
                  ) : field.control === "textarea" ? (
                    <textarea id={id} value={String(displayValue(current))} onChange={(event) => update(field, event.target.value)} rows={3} />
                  ) : field.control === "select" ? (
                    <select id={id} value={String(displayValue(current))} onChange={(event) => update(field, event.target.value)}>
                      {field.options?.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                    </select>
                  ) : (
                    <input id={id} type={field.control === "number" ? "number" : "text"} min={field.minimum} max={field.maximum} value={String(displayValue(current))} onChange={(event) => update(field, event.target.value)} />
                  )}
                </label>
              );
            })}
          </div>
        </section>
      ))}
      {draft && groups.length === 0 && <p className="empty-settings">No settings match “{query}”.</p>}
    </main>
  );
}
