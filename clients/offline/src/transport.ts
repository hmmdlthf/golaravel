/** The server answered with an error status. */
export class SyncHttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    /** Seconds, from Retry-After. */
    readonly retryAfter?: number,
  ) {
    super(message || `sync request failed with ${status}`);
    this.name = "SyncHttpError";
  }

  /** Worth retrying later: overload, maintenance, rate limits. */
  get transient(): boolean {
    return this.status >= 500 || this.status === 429 || this.status === 408;
  }
}

/** The server could not be reached (offline, DNS, TLS, CORS). */
export class NetworkError extends Error {
  constructor(cause: unknown) {
    super(cause instanceof Error ? cause.message : "network error");
    this.name = "NetworkError";
  }
}

export interface TransportOptions {
  url: string;
  fetch: typeof fetch;
  headers?: () => HeadersInit | Promise<HeadersInit>;
  timeout: number;
}

export const DEVICE_HEADER = "X-Sync-Device";

export class Transport {
  constructor(private readonly opts: TransportOptions) {}

  async call<T>(method: "GET" | "POST" | "DELETE", path: string, opts: { device?: string; body?: unknown } = {}): Promise<T> {
    const headers = new Headers(this.opts.headers ? await this.opts.headers() : undefined);
    headers.set("Accept", "application/json");
    if (opts.device) headers.set(DEVICE_HEADER, opts.device);
    let body: string | undefined;
    if (opts.body !== undefined) {
      headers.set("Content-Type", "application/json");
      body = JSON.stringify(opts.body);
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.opts.timeout);
    let resp: Response;
    try {
      resp = await this.opts.fetch(this.opts.url.replace(/\/+$/, "") + path, {
        method,
        headers,
        body,
        signal: ctrl.signal,
        cache: "no-store",
      });
    } catch (err) {
      throw new NetworkError(err);
    } finally {
      clearTimeout(timer);
    }
    if (resp.status === 204) return undefined as T;
    let json: unknown;
    try {
      json = await resp.json();
    } catch {
      json = undefined;
    }
    if (!resp.ok) {
      const e = (json ?? {}) as { error?: string; message?: string };
      const ra = Number(resp.headers.get("Retry-After"));
      throw new SyncHttpError(resp.status, e.error ?? "http_error", e.message ?? "", Number.isFinite(ra) && ra > 0 ? ra : undefined);
    }
    return json as T;
  }
}

/** Full-jitter exponential backoff: random(0, min(max, initial * 2^attempt)). */
export function backoffDelay(attempt: number, initial: number, max: number, random: () => number = Math.random): number {
  const cap = Math.min(max, initial * 2 ** Math.min(attempt, 30));
  return Math.max(Math.min(250, cap), Math.round(random() * cap));
}
