"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useSnowballAuth } from "./auth-boundary";

type GatewayStatus = {
  gateway: "starting" | "ready" | "degraded";
  voiceLive?: boolean;
  browser: {
    state: string;
    reason?: string;
    url?: string;
    title?: string;
    voiceButtonPresent?: boolean;
    voiceActive?: boolean;
    authenticated?: boolean;
  };
  webrtc: {
    connected: boolean;
    peer?: string;
  };
  push: {
    subscribers: number;
  };
  network: {
    lanIp: string;
    httpsPort: number;
    icePort: number;
  };
};

type SessionState = "idle" | "connecting" | "active" | "stopping" | "error";

const browserConsoleUrl = "/console/vnc.html?autoconnect=true&resize=scale&view_clip=false&path=console/websockify";

function waitForIceGathering(pc: RTCPeerConnection) {
  if (pc.iceGatheringState === "complete") return Promise.resolve();
  return new Promise<void>((resolve) => {
    const listener = () => {
      if (pc.iceGatheringState === "complete") {
        pc.removeEventListener("icegatheringstatechange", listener);
        resolve();
      }
    };
    pc.addEventListener("icegatheringstatechange", listener);
  });
}

function base64UrlToBytes(value: string) {
  const padding = "=".repeat((4 - (value.length % 4)) % 4);
  const binary = atob((value + padding).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

export function VoiceConsole() {
  const { request, logout } = useSnowballAuth();
  const [status, setStatus] = useState<GatewayStatus | null>(null);
  const [session, setSession] = useState<SessionState>("idle");
  const [notice, setNotice] = useState("Ready when you are.");
  const [pushState, setPushState] = useState<NotificationPermission | "unsupported">(
    "default",
  );
  const [secureContext, setSecureContext] = useState(true);
  const peerRef = useRef<RTCPeerConnection | null>(null);
  const mediaRef = useRef<MediaStream | null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement | null>(null);
  const intentionalStopRef = useRef(false);
  const voiceWasActiveRef = useRef(false);
  const operationRef = useRef(0);

  const refreshStatus = useCallback(async () => {
    try {
      const response = await request("/api/status", { cache: "no-store" });
      if (!response.ok) throw new Error("Gateway unavailable");
      setStatus((await response.json()) as GatewayStatus);
    } catch {
      setStatus(null);
    }
  }, [request]);

  useEffect(() => {
    const initialize = window.setTimeout(() => {
      void refreshStatus();
      setSecureContext(window.isSecureContext);
      if ("Notification" in window) setPushState(Notification.permission);
      else setPushState("unsupported");
    }, 0);
    const timer = window.setInterval(
      refreshStatus,
      session === "active" || session === "connecting" ? 1000 : 5000,
    );
    return () => {
      window.clearTimeout(initialize);
      window.clearInterval(timer);
    };
  }, [refreshStatus, session]);

  const releaseLocalAudio = useCallback(() => {
    peerRef.current?.close();
    peerRef.current = null;
    mediaRef.current?.getTracks().forEach((track) => track.stop());
    mediaRef.current = null;
    if (remoteAudioRef.current) remoteAudioRef.current.srcObject = null;
  }, []);

  const waitForBrowserVoiceState = useCallback(async (active: boolean, timeoutMs: number) => {
    const deadline = Date.now() + timeoutMs;
    let latest: GatewayStatus | null = null;
    while (Date.now() < deadline) {
      try {
        const response = await request("/api/status", { cache: "no-store" });
        if (response.ok) {
          latest = (await response.json()) as GatewayStatus;
          setStatus(latest);
          const live = Boolean(latest.voiceLive);
          const browserIdle = latest.browser.voiceActive === false;
          if (active ? live : !live && browserIdle) return latest;
        }
      } catch {
        // Keep polling: the browser controller may be briefly busy completing
        // the Voice stop transition.
      }
      await new Promise((resolve) => window.setTimeout(resolve, 250));
    }
    return latest;
  }, [request]);

  const stopVoice = useCallback(async () => {
    operationRef.current += 1;
    intentionalStopRef.current = true;
    voiceWasActiveRef.current = false;
    setSession("stopping");
    setNotice("Ending the conversation…");
    releaseLocalAudio();
    try {
      await request("/api/voice/stop", {
        method: "POST",
        signal: AbortSignal.timeout(6000),
      });
    } catch {
      // The status check below is authoritative even if the stop request timed
      // out while the browser was completing its own Voice transition.
    }
    const stopped = await waitForBrowserVoiceState(false, 7000);
    if (stopped?.voiceLive === false && stopped.browser.voiceActive === false) {
      setSession("idle");
      setNotice("Conversation ended.");
      return;
    }
    // Do not claim that Voice ended while the browser still reports it live.
    // Leaving the session active makes the next press a safe retry instead of
    // requiring a second click merely to refresh stale UI state.
    intentionalStopRef.current = false;
    voiceWasActiveRef.current = true;
    setSession("active");
    setNotice("ChatGPT Voice is still live. Snowball could not confirm the stop.");
  }, [releaseLocalAudio, request, waitForBrowserVoiceState]);

  useEffect(() => {
    if (!status || intentionalStopRef.current) return;

    const reconcile = window.setTimeout(() => {
      if (status.voiceLive && peerRef.current?.connectionState === "connected") {
        voiceWasActiveRef.current = true;
        setSession("active");
        setNotice("Live with Snowball.");
        return;
      }

      if (voiceWasActiveRef.current) {
        intentionalStopRef.current = true;
        voiceWasActiveRef.current = false;
        releaseLocalAudio();
        setSession("idle");
        setNotice("Conversation ended in ChatGPT.");
      }
    }, 0);
    return () => window.clearTimeout(reconcile);
  }, [releaseLocalAudio, status]);

  useEffect(() => {
    return () => {
      intentionalStopRef.current = true;
      releaseLocalAudio();
    };
  }, [releaseLocalAudio]);

  async function startVoice() {
    if (session === "active" || session === "connecting") {
      await stopVoice();
      return;
    }

    intentionalStopRef.current = false;
    voiceWasActiveRef.current = false;
    const operation = ++operationRef.current;

    const superseded = () => operation !== operationRef.current;

    if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
      setSession("error");
      setNotice("Microphone access needs trusted HTTPS. Install the local certificate first.");
      return;
    }

    setSession("connecting");
    setNotice("Confirming the ChatGPT session…");
    try {
      const statusResponse = await request("/api/status", { cache: "no-store" });
      if (!statusResponse.ok) throw new Error("Gateway unavailable");
      const currentStatus = (await statusResponse.json()) as GatewayStatus;
      if (superseded()) return;
      setStatus(currentStatus);
      if (!currentStatus.browser.authenticated || currentStatus.browser.state !== "ready") {
        setSession("error");
        setNotice("Sign in through Browser Console first. Signing in never starts Voice.");
        return;
      }
    } catch {
      if (superseded()) return;
      setSession("error");
      setNotice("Could not confirm the ChatGPT sign-in state.");
      return;
    }

    setNotice("Opening a private audio path…");

    try {
      const media = await navigator.mediaDevices.getUserMedia({
        video: false,
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
        },
      });
      if (superseded()) {
        media.getTracks().forEach((track) => track.stop());
        return;
      }
      mediaRef.current = media;

      const peer = new RTCPeerConnection({ iceServers: [] });
      peerRef.current = peer;
      for (const track of media.getAudioTracks()) peer.addTrack(track, media);

      peer.addEventListener("track", (event) => {
        if (!remoteAudioRef.current) return;
        remoteAudioRef.current.srcObject = event.streams[0];
        void remoteAudioRef.current.play();
      });
      peer.addEventListener("connectionstatechange", () => {
        if (peer.connectionState === "connected") {
          setNotice("Audio connected. Starting ChatGPT Voice…");
        } else if (["failed", "disconnected", "closed"].includes(peer.connectionState)) {
          if (intentionalStopRef.current) return;
          setSession("error");
          setNotice("The local audio link was interrupted.");
        }
      });

      const offer = await peer.createOffer({ offerToReceiveAudio: true });
      await peer.setLocalDescription(offer);
      await waitForIceGathering(peer);

      const response = await request("/api/webrtc/offer", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(peer.localDescription),
      });
      if (!response.ok) throw new Error(await response.text());
      const answer = (await response.json()) as RTCSessionDescriptionInit;
      if (superseded()) return;
      await peer.setRemoteDescription(answer);

      const voiceResponse = await request("/api/voice/start", { method: "POST" });
      if (superseded()) {
        await request("/api/voice/stop", { method: "POST" }).catch(() => undefined);
        void refreshStatus();
        return;
      }
      if (!voiceResponse.ok) {
        const detail = (await voiceResponse.json().catch(() => ({}))) as { error?: string };
        throw new Error(detail.error ?? "Audio is connected; ChatGPT needs attention in Browser Console.");
      }
      const browserStatus = (await voiceResponse.json()) as GatewayStatus["browser"];
      if (superseded()) return;
      const liveStatus = await waitForBrowserVoiceState(true, 5000);
      voiceWasActiveRef.current = Boolean(liveStatus?.voiceLive);
      if (liveStatus?.voiceLive) {
        setSession("active");
        setNotice("Live with Snowball.");
      } else {
        throw new Error(browserStatus.voiceActive
          ? "ChatGPT Voice is active, but Snowball has not confirmed the live audio path."
          : "ChatGPT Voice ended before the session became active.");
      }
      void refreshStatus();
    } catch (error) {
      if (superseded()) return;
      releaseLocalAudio();
      void request("/api/voice/stop", { method: "POST" }).catch(() => undefined);
      setSession("error");
      setNotice(error instanceof Error ? error.message : "Could not start voice.");
    }
  }

  async function enablePush() {
    if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
      setPushState("unsupported");
      setNotice("Install this site to the iOS Home Screen before enabling alerts.");
      return;
    }

    try {
      const registration = await navigator.serviceWorker.register("/sw.js");
      const permission = await Notification.requestPermission();
      setPushState(permission);
      if (permission !== "granted") return;

      const keyResponse = await request("/api/push/key");
      const { publicKey } = (await keyResponse.json()) as { publicKey: string };
      const subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: base64UrlToBytes(publicKey),
      });
      const response = await request("/api/push/subscribe", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(subscription),
      });
      if (!response.ok) throw new Error("Could not save this device.");
      setNotice("Recovery alerts are enabled on this device.");
      void refreshStatus();
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Push setup failed.");
    }
  }

  async function testPush() {
    const response = await request("/api/push/test", { method: "POST" });
    setNotice(response.ok ? "Test alert sent." : "No subscribed device is available yet.");
  }

  const browserNeedsHelp = status?.browser.state === "needs_human" || status?.browser.state === "needs_login";
  const gatewayReady = status?.gateway === "ready";

  return (
    <main className="console-shell">
      <div className="ambient ambient-one" />
      <div className="ambient ambient-two" />

      <header className="topbar">
        <Link className="wordmark" href="/" aria-label="Snowball-Voice-Gate home">
          <span className="snow-mark" aria-hidden="true"><i /><i /><i /></span>
          <span>Snowball-Voice-Gate</span>
        </Link>
        <div className={`gateway-pill ${gatewayReady ? "online" : "offline"}`}>
          <span /> {gatewayReady ? "LOCAL GATEWAY ONLINE" : "GATEWAY STARTING"}
        </div>
      </header>

      <section className="voice-stage" aria-labelledby="voice-heading">
        <p className="eyebrow">PRIVATE VOICE LINK</p>
        <h1 id="voice-heading">Talk to your ChatGPT.</h1>
        <p className="subhead">One tap opens a direct audio path through Snowball. Nothing listens until you begin.</p>

        <button
          className={`voice-orb ${session}`}
          type="button"
          onClick={() => void startVoice()}
          disabled={session === "stopping"}
          aria-label={session === "active" || session === "connecting" ? "End voice conversation" : "Start voice conversation"}
        >
          <span className="orb-glow" />
          <span className="wave wave-a" />
          <span className="wave wave-b" />
          <span className="wave wave-c" />
          <span className="orb-core" />
        </button>

        <div className={`session-label ${session}`}>
          <span className="session-dot" />
          {session === "idle" && "Tap to begin"}
          {session === "connecting" && "Connecting"}
          {session === "active" && "Voice is live"}
          {session === "stopping" && "Ending"}
          {session === "error" && "Needs attention"}
        </div>
        <p className="notice" role="status">{notice}</p>
        {/* Remote conversational audio is live speech, not prerecorded media with captions. */}
        {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
        <audio ref={remoteAudioRef} autoPlay />
      </section>

      {browserNeedsHelp && (
        <section className="attention-card" aria-live="assertive">
          <div>
            <p className="card-kicker">HUMAN CHECK REQUIRED</p>
            <h2>{status?.browser.state === "needs_login" ? "ChatGPT needs you to sign in." : "The browser needs a quick look."}</h2>
            <p>{status?.browser.reason || "Automation paused without changing the current page."}</p>
          </div>
          <a className="primary-action" href={browserConsoleUrl} target="_blank" rel="noreferrer">
            Open live browser
          </a>
        </section>
      )}

      <section className="control-grid" aria-label="Gateway controls">
        <article className="control-card browser-card">
          <div className="card-icon browser-icon"><span /></div>
          <p className="card-kicker">BROWSER</p>
          <h2>ChatGPT session</h2>
          <p className="card-copy">Sign in, approve a challenge, or inspect the exact Chromium session Snowball uses. Opening the console never starts Voice.</p>
          <div className="card-meta">
            <span className={`mini-status ${status?.browser.authenticated ? "good" : "warn"}`} />
            {status?.browser.authenticated ? "signed in" : status?.browser.state?.replaceAll("_", " ") || "starting"}
          </div>
          <p className="touch-hint">Fits automatically · tap to click · two-finger swipe to scroll</p>
          <a className="text-action" href={browserConsoleUrl} target="_blank" rel="noreferrer">
            Open Browser Console <span>↗</span>
          </a>
        </article>

        <article className="control-card alert-card">
          <div className="card-icon bell-icon"><span /></div>
          <p className="card-kicker">RECOVERY ALERTS</p>
          <h2>Call me when needed</h2>
          <p className="card-copy">Snowball will pause and notify this device when login, CAPTCHA, or permissions need you.</p>
          <div className="card-meta">
            <span className={`mini-status ${pushState === "granted" ? "good" : "neutral"}`} />
            {pushState === "granted" ? `${status?.push.subscribers ?? 1} device subscribed` : "notifications off"}
          </div>
          <div className="split-actions">
            <button className="text-action" type="button" onClick={() => void enablePush()}>Enable alerts</button>
            {pushState === "granted" && <button className="quiet-action" type="button" onClick={() => void testPush()}>Test</button>}
          </div>
        </article>

        <article className="control-card network-card">
          <div className="card-icon network-icon"><span /><i /></div>
          <p className="card-kicker">LOCAL ONLY</p>
          <h2>Inside your home</h2>
          <p className="card-copy">The console and audio socket listen only on Snowball’s LAN address.</p>
          <dl className="network-facts">
            <div><dt>HTTPS</dt><dd>{status ? `${status.network.lanIp}:${status.network.httpsPort}` : "192.168.1.1:8443"}</dd></div>
            <div><dt>VOICE</dt><dd>{status ? `UDP ${status.network.icePort}` : "UDP 49000"}</dd></div>
          </dl>
          {!secureContext && <a className="text-action" href="/ca.crt">Install local certificate <span>↓</span></a>}
        </article>
      </section>

      <footer>
        <span>Snowball · first light</span>
        <div className="footer-actions">
          <Link href="/admin">Admin</Link>
          <button type="button" onClick={() => void refreshStatus()}>Refresh status</button>
          <button type="button" onClick={() => void logout()}>Sign out</button>
        </div>
      </footer>
    </main>
  );
}
