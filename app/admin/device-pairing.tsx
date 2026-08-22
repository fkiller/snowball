"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useSnowballAuth } from "../auth-boundary";

type SerialPortLike = {
  readable: ReadableStream<Uint8Array> | null;
  writable: WritableStream<Uint8Array> | null;
  open(options: { baudRate: number; bufferSize?: number }): Promise<void>;
  close(): Promise<void>;
};

type SerialNavigator = Navigator & {
  serial?: {
    requestPort(options?: { filters?: Array<{ usbVendorId: number; usbProductId?: number }> }): Promise<SerialPortLike>;
  };
};

type SerialResponse = {
  version: number;
  id?: string;
  ok?: boolean;
  error?: string;
  event?: string;
  command?: string;
  stage?: string;
  detail?: string;
  result?: unknown;
};

type DeviceHello = {
  model: string;
  firmwareVersion: string;
  protocolVersion: number;
  hardwareId: string;
  publicKey: string;
  publicKeyFingerprint: string;
  developmentWake: string;
};

type DeviceStatus = {
  configured: boolean;
  wifiConnected: boolean;
  gatewayReachable?: boolean;
  enrollmentPending: boolean;
  debugFeedback: boolean;
  credentialStorage: string;
  gateway?: string;
  gatewayPort?: number;
};

type DeviceTraceEvent = {
  sequence: number;
  uptimeMs: number;
  attempt: number;
  stage: string;
  detail?: string;
  confidence?: number;
};

type EnrollmentMaterial = {
  enrollmentToken: string;
  gateway: string;
  gatewayHttpPort: number;
  gatewayPort: number;
  caSha256: string;
  expiresInSeconds: number;
};

type DeviceRecord = {
  name: string;
  hardwareId: string;
  firmwareVersion: string;
  publicKeyFingerprint: string;
  state: string;
  updatedAt: string;
};

type PendingRequest = {
  resolve: (response: SerialResponse) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof window.setTimeout>;
};

function requestID() {
  return crypto.randomUUID().replaceAll("-", "").slice(0, 24);
}

function responseResult<T>(response: SerialResponse): T {
  if (!response.ok) throw new Error(response.error || "The speaker rejected the request.");
  return response.result as T;
}

function isDeviceHello(value: unknown): value is DeviceHello {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<DeviceHello>;
  return candidate.model === "Waveshare ESP32-S3-AUDIO-Board"
    && candidate.protocolVersion === 1
    && typeof candidate.firmwareVersion === "string"
    && candidate.firmwareVersion.length > 0
    && candidate.firmwareVersion.length <= 64
    && typeof candidate.hardwareId === "string"
    && /^[0-9a-f]{2}(?::[0-9a-f]{2}){5}$/i.test(candidate.hardwareId)
    && typeof candidate.publicKey === "string"
    && candidate.publicKey.length >= 100
    && candidate.publicKey.length <= 512
    && typeof candidate.publicKeyFingerprint === "string"
    && /^[0-9a-f]{64}$/i.test(candidate.publicKeyFingerprint)
    && typeof candidate.developmentWake === "string";
}

function isPrivateIPv4(value: unknown) {
  if (typeof value !== "string") return false;
  const octets = value.split(".");
  if (octets.length !== 4 || octets.some((octet) => !/^\d{1,3}$/.test(octet) || Number(octet) > 255)) return false;
  const [first, second] = octets.map(Number);
  return first === 10 || (first === 172 && second >= 16 && second <= 31) || (first === 192 && second === 168);
}

function isEnrollmentMaterial(value: Partial<EnrollmentMaterial>): value is EnrollmentMaterial {
  return typeof value.enrollmentToken === "string"
    && /^[A-Za-z0-9_-]{20,512}$/.test(value.enrollmentToken)
    && isPrivateIPv4(value.gateway)
    && Number.isInteger(value.gatewayPort)
    && Number(value.gatewayPort) >= 1
    && Number(value.gatewayPort) <= 65535
    && Number.isInteger(value.gatewayHttpPort)
    && Number(value.gatewayHttpPort) >= 1
    && Number(value.gatewayHttpPort) <= 65535
    && typeof value.caSha256 === "string"
    && /^[0-9a-f]{64}$/i.test(value.caSha256)
    && Number.isInteger(value.expiresInSeconds)
    && Number(value.expiresInSeconds) >= 30
    && Number(value.expiresInSeconds) <= 900;
}

function isDeviceTraceEvent(value: unknown): value is DeviceTraceEvent {
  if (!value || typeof value !== "object") return false;
  const event = value as Partial<DeviceTraceEvent>;
  return Number.isSafeInteger(event.sequence) && Number(event.sequence) >= 1
    && Number.isSafeInteger(event.uptimeMs) && Number(event.uptimeMs) >= 0
    && Number.isSafeInteger(event.attempt) && Number(event.attempt) >= 0
    && typeof event.stage === "string" && event.stage.length > 0 && event.stage.length <= 40
    && (event.detail === undefined || (typeof event.detail === "string" && event.detail.length <= 64))
    && (event.confidence === undefined || (typeof event.confidence === "number" && event.confidence >= 0 && event.confidence <= 1));
}

export function DevicePairing() {
  const { request } = useSnowballAuth();
  const portRef = useRef<SerialPortLike | null>(null);
  const readerRef = useRef<ReadableStreamDefaultReader<Uint8Array> | null>(null);
  const writerRef = useRef<WritableStreamDefaultWriter<Uint8Array> | null>(null);
  const readLoopRef = useRef<Promise<void> | null>(null);
  const pendingRef = useRef(new Map<string, PendingRequest>());
  const [supported, setSupported] = useState<boolean | null>(null);
  const [connected, setConnected] = useState(false);
  const [busy, setBusy] = useState(false);
  const [hello, setHello] = useState<DeviceHello | null>(null);
  const [deviceStatus, setDeviceStatus] = useState<DeviceStatus | null>(null);
  const [deviceName, setDeviceName] = useState("Snowball Speaker");
  const [ssid, setSSID] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState("Connect the Waveshare board to this computer with USB-C.");
  const [lastEvent, setLastEvent] = useState("");
  const [trace, setTrace] = useState<DeviceTraceEvent[]>([]);
  const [devices, setDevices] = useState<DeviceRecord[]>([]);

  const loadDevices = useCallback(async () => {
    const response = await request("/api/devices", { cache: "no-store" });
    if (!response.ok) return [];
    const data = (await response.json()) as { devices?: DeviceRecord[] };
    const next = data.devices || [];
    setDevices(next);
    return next;
  }, [request]);

  const rejectPending = useCallback((reason: string) => {
    for (const pending of pendingRef.current.values()) {
      window.clearTimeout(pending.timer);
      pending.reject(new Error(reason));
    }
    pendingRef.current.clear();
  }, []);

  const closePort = useCallback(async () => {
    rejectPending("The USB device was disconnected.");
    const reader = readerRef.current;
    const writer = writerRef.current;
    readerRef.current = null;
    writerRef.current = null;
    await reader?.cancel().catch(() => undefined);
    await readLoopRef.current?.catch(() => undefined);
    readLoopRef.current = null;
    try { writer?.releaseLock(); } catch { /* already released */ }
    const port = portRef.current;
    portRef.current = null;
    await port?.close().catch(() => undefined);
    setConnected(false);
    setHello(null);
    setDeviceStatus(null);
    setTrace([]);
  }, [rejectPending]);

  useEffect(() => {
    const initialize = window.setTimeout(() => {
      setSupported(window.isSecureContext && Boolean((navigator as SerialNavigator).serial));
      void loadDevices();
    }, 0);
    return () => {
      window.clearTimeout(initialize);
      void closePort();
    };
  }, [closePort, loadDevices]);

  const readLoop = useCallback(async (reader: ReadableStreamDefaultReader<Uint8Array>) => {
    const decoder = new TextDecoder("utf-8", { fatal: false });
    let buffered = "";
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffered += decoder.decode(value, { stream: true });
        if (buffered.length > 16_384) buffered = buffered.slice(-4096);
        let newline = buffered.indexOf("\n");
        while (newline >= 0) {
          const line = buffered.slice(0, newline).trim();
          buffered = buffered.slice(newline + 1);
          newline = buffered.indexOf("\n");
          if (!line.startsWith("{") || line.length > 8192) continue;
          let response: SerialResponse;
          try {
            response = JSON.parse(line) as SerialResponse;
          } catch {
            continue;
          }
          if (response.id) {
            const pending = pendingRef.current.get(response.id);
            if (pending) {
              pendingRef.current.delete(response.id);
              window.clearTimeout(pending.timer);
              pending.resolve(response);
            }
          } else if (response.event) {
            if (response.event === "debug" && response.stage) {
              setLastEvent(`${response.stage}${response.detail ? ` · ${response.detail}` : ""}`);
            } else {
              setLastEvent(response.command ? `${response.event}: ${response.command}` : response.event);
            }
          }
        }
      }
    } catch (error) {
      if (portRef.current) setMessage(error instanceof Error ? error.message : "USB serial connection failed.");
    } finally {
      try { reader.releaseLock(); } catch { /* port shutdown may release first */ }
      if (readerRef.current === reader) {
        readerRef.current = null;
        setConnected(false);
        rejectPending("The USB serial connection ended.");
      }
    }
  }, [rejectPending]);

  const sendOperation = useCallback(async (op: string, payload?: Record<string, unknown>) => {
    const writer = writerRef.current;
    if (!writer) throw new Error("Connect the USB device first.");
    const id = requestID();
    const response = new Promise<SerialResponse>((resolve, reject) => {
      const timer = window.setTimeout(() => {
        pendingRef.current.delete(id);
        reject(new Error("The speaker did not answer the USB request."));
      }, 7000);
      pendingRef.current.set(id, { resolve, reject, timer });
    });
    try {
      await writer.write(new TextEncoder().encode(`${JSON.stringify({ version: 1, id, op, ...(payload ? { payload } : {}) })}\n`));
    } catch (error) {
      const pending = pendingRef.current.get(id);
      if (pending) window.clearTimeout(pending.timer);
      pendingRef.current.delete(id);
      throw error;
    }
    return response;
  }, []);

  async function refreshDeviceStatus() {
    const status = responseResult<DeviceStatus>(await sendOperation("status"));
    setDeviceStatus(status);
    return status;
  }

  async function refreshTrace() {
    const result = responseResult<{ events?: unknown[] }>(await sendOperation("trace"));
    const events = Array.isArray(result.events) ? result.events.filter(isDeviceTraceEvent).slice(-32) : [];
    setTrace(events);
    return events;
  }

  async function setDebugFeedback(enabled: boolean) {
    setBusy(true);
    try {
      responseResult<{ debugFeedback: boolean }>(await sendOperation("debug-mode", { enabled }));
      await refreshDeviceStatus();
      setMessage(`Audible debug feedback ${enabled ? "enabled" : "disabled"}.`);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not change debug feedback.");
    } finally {
      setBusy(false);
    }
  }

  async function runAudioSelfTest() {
    setBusy(true);
    try {
      const result = responseResult<{
        speakerWrite?: unknown;
        sampleFrames?: unknown;
        peak?: unknown;
      }>(await sendOperation("audio-self-test"));
      if (result.speakerWrite !== true
        || !Number.isSafeInteger(result.sampleFrames)
        || Number(result.sampleFrames) <= 0
        || !Array.isArray(result.peak)
        || result.peak.length !== 4
        || !result.peak.every((value) => Number.isSafeInteger(value) && Number(value) >= 0 && Number(value) <= 65_535)
        || Number(result.peak[1]) === 0
        || Number(result.peak[3]) === 0) {
        throw new Error("The speaker returned an invalid audio self-test result.");
      }
      setMessage(`Audio self-test passed · speaker write OK · RMNM input peaks ${result.peak.join(", ")}.`);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Audio self-test failed.");
    } finally {
      setBusy(false);
    }
  }

  async function connect() {
    const serial = (navigator as SerialNavigator).serial;
    if (!window.isSecureContext || !serial) {
      setMessage("Web Serial requires trusted HTTPS in desktop Chrome or Edge.");
      return;
    }
    setBusy(true);
    setMessage("Choose the Espressif USB Serial/JTAG device.");
    try {
      if (portRef.current) throw new Error("Disconnect the current USB device first.");
      const port = await serial.requestPort({ filters: [{ usbVendorId: 0x303a }] });
      await port.open({ baudRate: 115200, bufferSize: 4096 });
      if (!port.readable || !port.writable) throw new Error("The selected USB device has no serial data channel.");
      portRef.current = port;
      const reader = port.readable.getReader();
      const writer = port.writable.getWriter();
      readerRef.current = reader;
      writerRef.current = writer;
      setConnected(true);
      readLoopRef.current = readLoop(reader);
      const identity = responseResult<unknown>(await sendOperation("hello"));
      if (!isDeviceHello(identity)) {
        throw new Error("This is not a supported Snowball speaker firmware.");
      }
      setHello(identity);
      await refreshDeviceStatus();
      await refreshTrace().catch(() => []);
      setMessage(`Connected to ${identity.model}.`);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not connect to the USB device.");
      await closePort();
    } finally {
      setBusy(false);
    }
  }

  async function provision(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!hello || !ssid.trim()) return;
    setBusy(true);
    setMessage("Creating a short-lived device enrollment and sending Wi-Fi over USB…");
    try {
      const network = ssid.trim();
      const networkBytes = new TextEncoder().encode(network).length;
      const passwordBytes = new TextEncoder().encode(password).length;
      if (networkBytes > 32 || (passwordBytes > 0 && (passwordBytes < 8 || passwordBytes > 63))) {
        throw new Error("Wi-Fi names must fit 32 bytes; WPA passwords must fit 8–63 bytes.");
      }
      const enrollmentResponse = await request("/api/devices/enrollment", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          name: deviceName,
          model: hello.model,
          firmwareVersion: hello.firmwareVersion,
          protocolVersion: hello.protocolVersion,
          hardwareId: hello.hardwareId,
          publicKey: hello.publicKey,
          publicKeyFingerprint: hello.publicKeyFingerprint,
        }),
      });
      const enrollment = (await enrollmentResponse.json().catch(() => ({}))) as Partial<EnrollmentMaterial> & { error?: string };
      if (!enrollmentResponse.ok) throw new Error(enrollment.error || "Gateway enrollment could not be created.");
      if (!isEnrollmentMaterial(enrollment)) throw new Error("Gateway returned invalid enrollment material.");
      const provisionResponse = await sendOperation("provision", {
        ssid: network,
        password,
        gateway: enrollment.gateway,
        gatewayHttpPort: enrollment.gatewayHttpPort,
        gatewayPort: enrollment.gatewayPort,
        caSha256: enrollment.caSha256,
        enrollmentToken: enrollment.enrollmentToken,
        expiresInSeconds: enrollment.expiresInSeconds,
      });
      responseResult<{ state: string }>(provisionResponse);
      setPassword("");
      setMessage("Wi-Fi configuration accepted. Waiting for the speaker to join the LAN…");
      let status: DeviceStatus | null = null;
      for (let attempt = 0; attempt < 20; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 1000));
        status = await refreshDeviceStatus();
        if (status.wifiConnected && status.gatewayReachable === true && !status.enrollmentPending) break;
      }
      const registry = await loadDevices();
      const gatewayActive = registry.some((device) =>
        device.hardwareId.toLocaleLowerCase() === hello.hardwareId.toLocaleLowerCase()
        && device.publicKeyFingerprint.toLocaleLowerCase() === hello.publicKeyFingerprint.toLocaleLowerCase()
        && device.state === "active");
      setMessage(status?.wifiConnected && status.gatewayReachable === true && !status.enrollmentPending && gatewayActive
        ? "Pairing complete · Wi-Fi connected · Gateway reachable · registry active · device-key proof accepted."
        : status?.wifiConnected && status.gatewayReachable !== true
          ? "Speaker joined Wi-Fi, but the configured Gateway is not reachable from that network. Pairing is not complete."
          : status?.wifiConnected && !status.enrollmentPending
          ? "The board reports enrollment complete, but the Gateway active record is missing. Do not start voice testing yet."
          : status?.wifiConnected
            ? "Speaker joined Wi-Fi, but its authenticated Gateway enrollment is still pending."
            : "Configuration was saved, but Wi-Fi has not connected yet. Check the network name and password.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Speaker provisioning failed.");
    } finally {
      setPassword("");
      setBusy(false);
    }
  }

  return (
    <section className="device-pairing" aria-labelledby="device-pairing-title">
      <div className="device-pairing-copy">
        <p className="card-kicker">USB SPEAKER SETUP</p>
        <h2 id="device-pairing-title">Pair a Waveshare speaker</h2>
        <p>USB stays between this browser and the board. The Snowball container never receives USB access or your Wi-Fi password.</p>
      </div>

      {supported === false ? (
        <p className="pairing-warning">Open this trusted HTTPS page in desktop Chrome or Edge. Web Serial is not available in this browser.</p>
      ) : (
        <div className="pairing-controls">
          <button className="secondary-button" type="button" disabled={busy} onClick={() => connected ? void closePort() : void connect()}>
            {connected ? "Disconnect USB" : "Connect USB device"}
          </button>
          {hello && (
            <dl className="device-identity">
              <div><dt>Board</dt><dd>{hello.model}</dd></div>
              <div><dt>Firmware</dt><dd>{hello.firmwareVersion}</dd></div>
              <div><dt>Hardware ID</dt><dd>{hello.hardwareId}</dd></div>
              <div><dt>Public key</dt><dd>{hello.publicKeyFingerprint.slice(0, 16)}…</dd></div>
              <div><dt>Development wake</dt><dd>{hello.developmentWake}</dd></div>
            </dl>
          )}
          {hello && (
            <div className="pairing-controls">
              <button className="secondary-button" type="button" disabled={busy} onClick={() => void refreshTrace()}>
                Refresh device trace
              </button>
              <button className="secondary-button" type="button" disabled={busy} onClick={() => void runAudioSelfTest()}>
                Test speaker and microphones
              </button>
              <button className="secondary-button" type="button" disabled={busy || !deviceStatus} onClick={() => void setDebugFeedback(!deviceStatus?.debugFeedback)}>
                Debug sounds {deviceStatus?.debugFeedback ? "off" : "on"}
              </button>
            </div>
          )}
          {hello && (
            <form className="pairing-form" onSubmit={provision}>
              <label>Speaker name<input value={deviceName} maxLength={64} required onChange={(event) => setDeviceName(event.target.value)} /></label>
              <label>Wi-Fi network<input value={ssid} maxLength={32} autoComplete="off" required onChange={(event) => setSSID(event.target.value)} /></label>
              <label>Wi-Fi password<input value={password} maxLength={63} type="password" autoComplete="new-password" onChange={(event) => setPassword(event.target.value)} /></label>
              <button className="save-button" type="submit" disabled={busy || !ssid.trim()}>Pair speaker</button>
            </form>
          )}
        </div>
      )}

      <p className="pairing-message" role="status">{message}</p>
      {deviceStatus && <p className="pairing-state">USB status · {deviceStatus.wifiConnected ? "Wi-Fi connected" : deviceStatus.configured ? "configured, offline" : "not configured"} · {deviceStatus.gatewayReachable === true ? "Gateway reachable" : deviceStatus.wifiConnected ? "Gateway not yet reachable" : "Gateway unknown"} · {deviceStatus.credentialStorage}</p>}
      {lastEvent && <p className="pairing-state">Last board event · {lastEvent}</p>}
      {trace.length > 0 && (
        <div className="paired-devices device-trace">
          <h3>Recent device trace</h3>
          {trace.slice().reverse().map((event) => (
            <div key={event.sequence}>
              <span><strong>{event.stage}</strong><small>attempt {event.attempt} · {event.uptimeMs} ms{event.detail ? ` · ${event.detail}` : ""}</small></span>
              <em>{event.confidence === undefined ? "" : event.confidence.toFixed(3)}</em>
            </div>
          ))}
        </div>
      )}
      {devices.length > 0 && (
        <div className="paired-devices">
          <h3>Gateway device registry</h3>
          {devices.map((device) => (
            <div key={device.publicKeyFingerprint}>
              <span><strong>{device.name}</strong><small>{device.hardwareId} · firmware {device.firmwareVersion}</small></span>
              <em>{device.state}</em>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
