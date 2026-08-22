"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import Link from "next/link";

type AuthStatus = {
  authenticated: boolean;
  setupRequired: boolean;
  csrfToken?: string;
  expiresAt?: string;
};

type SnowballAuth = {
  csrfToken: string;
  expiresAt?: string;
  request: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
  logout: () => Promise<void>;
};

const AuthContext = createContext<SnowballAuth | null>(null);

export function useSnowballAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error("Snowball authentication is unavailable.");
  return value;
}

export function AuthBoundary({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [setupCode, setSetupCode] = useState("");
  const [message, setMessage] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const response = await fetch("/api/auth/status", { cache: "no-store" });
      if (!response.ok) throw new Error("Authentication service unavailable");
      setStatus((await response.json()) as AuthStatus);
    } catch {
      setStatus({ authenticated: false, setupRequired: false });
      setMessage("Snowball could not reach the local authentication service.");
    }
  }, []);

  useEffect(() => {
    const initialize = window.setTimeout(() => void refresh(), 0);
    return () => window.clearTimeout(initialize);
  }, [refresh]);

  const request = useCallback(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const method = (init.method || "GET").toUpperCase();
    const headers = new Headers(init.headers);
    if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
      headers.set("X-CSRF-Token", status?.csrfToken || "");
    }
    const response = await fetch(input, { ...init, headers });
    if (response.status === 401) void refresh();
    return response;
  }, [refresh, status?.csrfToken]);

  const logout = useCallback(async () => {
    await request("/api/auth/logout", { method: "POST" });
    setStatus({ authenticated: false, setupRequired: false });
    setPassword("");
  }, [request]);

  const auth = useMemo<SnowballAuth | null>(() => {
    if (!status?.authenticated || !status.csrfToken) return null;
    return { csrfToken: status.csrfToken, expiresAt: status.expiresAt, request, logout };
  }, [logout, request, status]);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setMessage("");
    if (status?.setupRequired && password !== confirmation) {
      setMessage("The passwords do not match.");
      return;
    }
    setSubmitting(true);
    try {
      const endpoint = status?.setupRequired ? "/api/auth/bootstrap" : "/api/auth/login";
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(status?.setupRequired ? { setupCode, password } : { password }),
      });
      const detail = (await response.json().catch(() => ({}))) as AuthStatus & { error?: string };
      if (!response.ok) throw new Error(detail.error || "Authentication failed.");
      setStatus(detail);
      setPassword("");
      setConfirmation("");
      setSetupCode("");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Authentication failed.");
    } finally {
      setSubmitting(false);
    }
  }

  if (auth) return <AuthContext.Provider value={auth}>{children}</AuthContext.Provider>;

  return (
    <main className="auth-shell">
      <section className="auth-panel" aria-labelledby="auth-title">
        <Link className="wordmark auth-wordmark" href="/" aria-label="Snowball home">
          <span className="snow-mark" aria-hidden="true"><i /><i /><i /></span>
          <span>SNOWBALL</span>
        </Link>
        {!status ? (
          <p className="auth-loading" role="status">Checking the local gateway…</p>
        ) : (
          <>
            <p className="eyebrow">{status.setupRequired ? "INITIAL SETUP" : "LOCAL ADMIN"}</p>
            <h1 id="auth-title">{status.setupRequired ? "Secure your Snowball." : "Welcome back."}</h1>
            <p className="auth-copy">
              {status.setupRequired
                ? "Create the administrator password that protects voice controls, settings, and the live browser console."
                : "Enter the Snowball administrator password. Your ChatGPT sign-in remains separate inside Browser Console."}
            </p>
            {status.setupRequired && (
              <aside className="setup-note">
                Find the one-time setup code in the container log: <code>docker logs snowball-voice</code>
              </aside>
            )}
            <form className="auth-form" onSubmit={submit}>
              {status.setupRequired && (
                <label>
                  <span>Initial setup code</span>
                  <input value={setupCode} onChange={(event) => setSetupCode(event.target.value)} autoComplete="one-time-code" required />
                </label>
              )}
              <label>
                <span>Administrator password</span>
                <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete={status.setupRequired ? "new-password" : "current-password"} minLength={status.setupRequired ? 12 : undefined} required />
              </label>
              {status.setupRequired && (
                <label>
                  <span>Confirm password</span>
                  <input type="password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} autoComplete="new-password" minLength={12} required />
                </label>
              )}
              {message && <p className="auth-error" role="alert">{message}</p>}
              <button className="auth-submit" type="submit" disabled={submitting}>
                {submitting ? "Please wait…" : status.setupRequired ? "Complete setup" : "Sign in"}
              </button>
            </form>
          </>
        )}
      </section>
    </main>
  );
}
