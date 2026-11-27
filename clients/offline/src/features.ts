// Client for pkg/features' public endpoint (Manager.Handler): the tenant's
// Public features, cached locally so an offline device still knows which
// features are on.

import { NetworkError, SyncHttpError } from "./transport.js";

/** Where the last known features are kept between app starts. */
export interface FeatureStorage {
  get(key: string): string | null | Promise<string | null>;
  set(key: string, value: string): void | Promise<void>;
}

export interface FeatureOptions {
  /** Full URL of the endpoint, e.g. "https://api.example.com/api/features". */
  url: string;
  /** Auth and tenant headers, called before every request. */
  headers?: () => HeadersInit | Promise<HeadersInit>;
  /** fetch to use; in Tauri, the HTTP plugin's fetch. Default globalThis.fetch. */
  fetch?: typeof fetch;
  /**
   * Cache for the last known features. Default: localStorage when available,
   * else memory only. Pass false to keep nothing between app starts.
   */
  storage?: FeatureStorage | false;
  /** Storage key; include the tenant so tenants never share a cache. Default "golaravel-features:" + url. */
  storageKey?: string;
  /** How often to check for changes, ms (default 60 s). 0 disables polling. */
  pollInterval?: number;
  /** Request timeout, ms (default 30 s). */
  timeout?: number;
  /** Fetch and start polling on open (default true). */
  autoStart?: boolean;
  now?: () => number;
}

export interface FeatureSnapshot {
  features: Record<string, unknown>;
  etag?: string;
  /** When the server last confirmed these values (ms since epoch). */
  fetchedAt?: number;
}

export interface FeatureEvents {
  /** Features changed (from the server or the cache on open). */
  change: Record<string, unknown>;
  /** A refresh failed; the last known features stay in use. */
  error: unknown;
}

type Listener<T> = (value: T) => void;

class MemoryStorage implements FeatureStorage {
  private readonly m = new Map<string, string>();
  get(key: string) {
    return this.m.get(key) ?? null;
  }
  set(key: string, value: string) {
    this.m.set(key, value);
  }
}

function defaultStorage(): FeatureStorage {
  try {
    const ls = (globalThis as { localStorage?: Storage }).localStorage;
    if (ls) {
      const probe = "__golaravel_features_probe__";
      ls.setItem(probe, "1");
      ls.removeItem(probe);
      return { get: (k) => ls.getItem(k), set: (k, v) => ls.setItem(k, v) };
    }
  } catch {
    // private mode or blocked storage
  }
  return new MemoryStorage();
}

/**
 * The tenant's public features, kept up to date in the background.
 *
 *   const features = await FeatureClient.open({ url: "/api/features", storageKey: `features:${tenantId}` });
 *   if (features.enabled("invoices.recurring")) showRecurring();
 *   features.on("change", rerender);
 *
 * Reads are synchronous and never wait for the network: they answer from
 * the last known values (cached across app starts) until the server is
 * reachable. A feature the client has never seen is off.
 */
export class FeatureClient {
  private snap: FeatureSnapshot = { features: {} };
  private readonly listeners: { [K in keyof FeatureEvents]: Set<Listener<FeatureEvents[K]>> } = {
    change: new Set(),
    error: new Set(),
  };
  private readonly opts: Required<Omit<FeatureOptions, "headers" | "storage">> &
    Pick<FeatureOptions, "headers"> & { storage: FeatureStorage | false };
  private timer?: ReturnType<typeof setTimeout>;
  private inflight?: Promise<boolean>;
  private closed = false;
  private readonly unlisten: (() => void)[] = [];

  private constructor(options: FeatureOptions) {
    this.opts = {
      fetch: globalThis.fetch?.bind(globalThis),
      storageKey: "golaravel-features:" + options.url,
      pollInterval: 60_000,
      timeout: 30_000,
      autoStart: true,
      now: Date.now,
      ...options,
      storage: options.storage === undefined ? defaultStorage() : options.storage,
    };
  }

  /** Loads the cached features, then (with autoStart) refreshes in the background. */
  static async open(options: FeatureOptions): Promise<FeatureClient> {
    const c = new FeatureClient(options);
    await c.loadCache();
    if (c.opts.autoStart) c.start();
    return c;
  }

  /** True only when the feature's value is exactly true. */
  enabled(key: string): boolean {
    return this.snap.features[key] === true;
  }

  /** The feature's value, or fallback when unknown. */
  value<T>(key: string, fallback: T): T {
    return key in this.snap.features ? (this.snap.features[key] as T) : fallback;
  }

  /** Every known feature (a copy). */
  all(): Record<string, unknown> {
    return structuredClone(this.snap.features);
  }

  /** When the server last confirmed the current values; undefined if never. */
  get fetchedAt(): number | undefined {
    return this.snap.fetchedAt;
  }

  on<K extends keyof FeatureEvents>(event: K, fn: Listener<FeatureEvents[K]>): () => void {
    this.listeners[event].add(fn);
    return () => this.listeners[event].delete(fn);
  }

  /** Starts polling and refreshing when the device comes online or the tab is shown. */
  start(): void {
    if (this.closed) return;
    const g = globalThis as { addEventListener?: typeof addEventListener; removeEventListener?: typeof removeEventListener; document?: Document };
    if (g.addEventListener && this.unlisten.length === 0) {
      const online = () => void this.refresh().catch(() => undefined);
      g.addEventListener("online", online);
      this.unlisten.push(() => g.removeEventListener?.("online", online));
      if (g.document) {
        const doc = g.document;
        const visible = () => {
          if (doc.visibilityState === "visible") online();
        };
        doc.addEventListener("visibilitychange", visible);
        this.unlisten.push(() => doc.removeEventListener("visibilitychange", visible));
      }
    }
    void this.refresh().catch(() => undefined);
  }

  /**
   * Asks the server for the current features. Resolves true when they
   * changed. Rejects on failure (also emitted as "error"); the last known
   * features stay in use. Concurrent calls share one request.
   */
  refresh(): Promise<boolean> {
    if (this.closed) return Promise.resolve(false);
    this.inflight ??= this.fetchOnce().finally(() => {
      this.inflight = undefined;
      this.schedule();
    });
    return this.inflight;
  }

  /** Stops polling and listeners. Cached values stay readable. */
  close(): void {
    this.closed = true;
    clearTimeout(this.timer);
    for (const u of this.unlisten.splice(0)) u();
  }

  private schedule() {
    clearTimeout(this.timer);
    if (this.closed || this.opts.pollInterval <= 0) return;
    this.timer = setTimeout(() => void this.refresh().catch(() => undefined), this.opts.pollInterval);
  }

  private async fetchOnce(): Promise<boolean> {
    try {
      return await this.request();
    } catch (err) {
      this.emit("error", err);
      throw err;
    }
  }

  private async request(): Promise<boolean> {
    const headers = new Headers(this.opts.headers ? await this.opts.headers() : undefined);
    headers.set("Accept", "application/json");
    if (this.snap.etag) headers.set("If-None-Match", this.snap.etag);
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.opts.timeout);
    let resp: Response;
    try {
      resp = await this.opts.fetch(this.opts.url, { method: "GET", headers, signal: ctrl.signal, cache: "no-store" });
    } catch (err) {
      throw new NetworkError(err);
    } finally {
      clearTimeout(timer);
    }
    if (resp.status === 304) {
      this.snap = { ...this.snap, fetchedAt: this.opts.now() };
      await this.saveCache();
      return false;
    }
    let json: unknown;
    try {
      json = await resp.json();
    } catch {
      json = undefined;
    }
    if (!resp.ok) {
      const e = (json ?? {}) as { error?: string; message?: string };
      throw new SyncHttpError(resp.status, e.error ?? "http_error", e.message ?? "");
    }
    const features = (json as { features?: unknown } | undefined)?.features;
    if (!features || typeof features !== "object" || Array.isArray(features)) {
      throw new SyncHttpError(resp.status, "bad_response", "features endpoint returned no features object");
    }
    const changed = JSON.stringify(features) !== JSON.stringify(this.snap.features);
    this.snap = {
      features: features as Record<string, unknown>,
      etag: resp.headers.get("ETag") ?? undefined,
      fetchedAt: this.opts.now(),
    };
    await this.saveCache();
    if (changed) this.emit("change", this.all());
    return changed;
  }

  private async loadCache() {
    if (!this.opts.storage) return;
    try {
      const raw = await this.opts.storage.get(this.opts.storageKey);
      if (!raw) return;
      const s = JSON.parse(raw) as FeatureSnapshot;
      if (s && typeof s.features === "object" && s.features && !Array.isArray(s.features)) {
        this.snap = { features: s.features, etag: s.etag, fetchedAt: s.fetchedAt };
      }
    } catch {
      // unreadable cache: start empty
    }
  }

  private async saveCache() {
    if (!this.opts.storage) return;
    try {
      await this.opts.storage.set(this.opts.storageKey, JSON.stringify(this.snap));
    } catch {
      // storage full or blocked: keep working from memory
    }
  }

  private emit<K extends keyof FeatureEvents>(event: K, value: FeatureEvents[K]) {
    for (const fn of this.listeners[event]) {
      try {
        fn(value);
      } catch {
        // a listener must not break the client
      }
    }
  }
}
