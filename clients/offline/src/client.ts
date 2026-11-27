import { aad, deriveDeviceKey, exportPublicKey, generateDeviceKeyPair, open, seal, type Endpoint } from "./crypto.js";
import { Clock } from "./hlc.js";
import { ulid } from "./ids.js";
import { LocalStore, type Touched } from "./store.js";
import { NetworkError, SyncHttpError, Transport, backoffDelay } from "./transport.js";
import type {
  DeadLetter,
  Device,
  Envelope,
  Mutation,
  Op,
  PullResponse,
  PushResponse,
  SnapshotResponse,
} from "./types.js";

export interface SyncOptions {
  /** Base URL of the mounted sync routes, e.g. "https://api.example.com/api/sync". */
  url: string;
  /** Entities to load on first sync and resyncs (must have Snapshot on the server). */
  entities: string[];
  /** IndexedDB database name; one per signed-in tenant. Default "golaravel-offline". */
  database?: string;
  /** Auth and tenant headers, called before every request (refresh tokens here). */
  headers?: () => HeadersInit | Promise<HeadersInit>;
  /** fetch to use; in Tauri, the HTTP plugin's fetch. Default globalThis.fetch. */
  fetch?: typeof fetch;
  /** Register the device with a key and encrypt every payload. */
  encrypt?: boolean;
  /** Shown in the server's device list. */
  device?: { name?: string; platform?: string; appVersion?: string };
  /** Mutations per push (default 100; the server caps at 500). */
  batchSize?: number;
  /** Changes per pull page (default 500). */
  pullLimit?: number;
  /** How often to pull while idle, ms (default 30 s). 0 disables polling. */
  pollInterval?: number;
  /** Delay before pushing after a local write, ms (default 200). */
  writeDelay?: number;
  /** Backoff after failures: first delay and cap, ms (default 1 s and 5 min). */
  backoff?: { initial?: number; max?: number };
  /** Keep acknowledged outbox entries this long, ms (default 7 days). */
  keepAcknowledged?: number;
  /** Request timeout, ms (default 30 s). */
  timeout?: number;
  /** Start syncing on open (default true). */
  autoStart?: boolean;
  now?: () => number;
  random?: () => number;
}

export type SyncState = "idle" | "syncing" | "offline" | "backoff" | "error" | "revoked" | "stopped";

export interface SyncStatus {
  state: SyncState;
  /** Mutations waiting for the server. */
  pending: number;
  /** This tab runs the sync loop (one tab per database does). */
  leader: boolean;
  lastSyncAt?: number;
  nextRetryAt?: number;
  /** Error code or message of the last failure. */
  error?: string;
}

export interface SyncEvents {
  status: SyncStatus;
  /** Entities whose local state changed (local write, push answer or pull). */
  change: Touched[];
  /** Mutations the server refused for good; their local effect is undone. */
  rejected: DeadLetter[];
}

type Listener<T> = (value: T) => void;

interface Session {
  device: Device;
  key?: CryptoKey;
}

/** The batch stopped at a mutation the server could not apply yet. */
class RetryLater extends Error {}

/** One local change, before ids and clocks are assigned. */
export interface Change {
  entity: string;
  id: string;
  op: Op;
  set?: Record<string, unknown>;
  inc?: Record<string, number>;
}

/**
 * Offline-first sync client. Reads and writes go to IndexedDB and never
 * wait for the network; a background loop pushes the outbox and pulls
 * server changes whenever the server can be reached.
 */
export class SyncClient {
  private readonly listeners: { [K in keyof SyncEvents]: Set<Listener<SyncEvents[K]>> } = {
    status: new Set(),
    change: new Set(),
    rejected: new Set(),
  };
  private _status: SyncStatus = { state: "stopped", pending: 0, leader: false };
  private running?: Promise<void>;
  private timer?: ReturnType<typeof setTimeout>;
  private timerAt = Infinity;
  private attempt = 0;
  private started = false;
  private leader = false;
  private releaseLeader?: () => void;
  private readonly channel?: BroadcastChannel;
  private readonly transport: Transport;
  private readonly opts: Required<Omit<SyncOptions, "headers" | "device" | "backoff">> &
    Pick<SyncOptions, "headers" | "device"> & { backoff: { initial: number; max: number } };
  private readonly unlisten: (() => void)[] = [];

  private constructor(
    readonly store: LocalStore,
    private readonly clock: Clock,
    options: SyncOptions,
  ) {
    this.opts = {
      database: "golaravel-offline",
      fetch: globalThis.fetch?.bind(globalThis),
      encrypt: false,
      batchSize: 100,
      pullLimit: 500,
      pollInterval: 30_000,
      writeDelay: 200,
      keepAcknowledged: 7 * 24 * 3600_000,
      timeout: 30_000,
      autoStart: true,
      now: Date.now,
      random: Math.random,
      ...options,
      backoff: { initial: 1000, max: 300_000, ...options.backoff },
    };
    this.transport = new Transport({
      url: this.opts.url,
      fetch: this.opts.fetch,
      timeout: this.opts.timeout,
      ...(this.opts.headers ? { headers: this.opts.headers } : {}),
    });
    if (typeof BroadcastChannel !== "undefined") {
      this.channel = new BroadcastChannel(`golaravel-offline:${this.opts.database}`);
      this.channel.onmessage = (ev: MessageEvent) => this.onMessage(ev.data);
    }
  }

  static async open(options: SyncOptions): Promise<SyncClient> {
    const now = options.now ?? Date.now;
    const store = await LocalStore.open(options.database ?? "golaravel-offline", now);
    let node = await store.getMeta<string>("node");
    if (!node) {
      node = ulid();
      await store.setMeta({ node });
    }
    const clock = new Clock(node, await store.getMeta<string>("hlc"), now);
    const client = new SyncClient(store, clock, options);
    client._status.pending = await store.pendingCount();
    if (client.opts.autoStart) client.start();
    return client;
  }

  // ---- events and status ----------------------------------------------

  get status(): SyncStatus {
    return { ...this._status };
  }

  on<K extends keyof SyncEvents>(event: K, fn: Listener<SyncEvents[K]>): () => void {
    this.listeners[event].add(fn);
    return () => this.listeners[event].delete(fn);
  }

  private emit<K extends keyof SyncEvents>(event: K, value: SyncEvents[K]): void {
    for (const fn of this.listeners[event]) {
      try {
        fn(value);
      } catch (err) {
        console.error("@golaravel/offline listener failed", err);
      }
    }
  }

  private setStatus(patch: Partial<SyncStatus>): void {
    this._status = { ...this._status, ...patch, leader: this.leader };
    this.emit("status", this.status);
  }

  // ---- local reads and writes ----------------------------------------

  table<T extends Record<string, unknown> = Record<string, unknown>>(entity: string): Table<T> {
    return new Table<T>(this, entity);
  }

  /**
   * Writes changes that must reach the server together (a sale, its lines,
   * the stock changes): they are pushed as one group and applied or
   * rejected as a whole.
   */
  async batch(fn: (b: BatchWriter) => void | Promise<void>): Promise<void> {
    const w = new BatchWriter();
    await fn(w);
    if (w.changes.length) await this.write(w.changes, ulid());
  }

  /** Records changes locally and queues them for the server. */
  async write(changes: Change[], group?: string): Promise<void> {
    const muts: Mutation[] = [];
    for (const c of changes) {
      if (!this.opts.entities.includes(c.entity)) throw new Error(`@golaravel/offline: "${c.entity}" is not in options.entities`);
      const row = await this.store.row(c.entity, c.id);
      const m: Mutation = { id: ulid(), entity: c.entity, entity_id: c.id, op: c.op, hlc: this.clock.now(), at: this.opts.now() };
      if (c.set && Object.keys(c.set).length) m.set = c.set;
      if (c.inc && Object.keys(c.inc).length) m.inc = c.inc;
      if (group) m.group = group;
      if (row?.base) m.base_version = row.base.version;
      muts.push(m);
    }
    const touched = await this.store.write(muts);
    await this.store.setMeta({ hlc: this.clock.current });
    this.setStatus({ pending: await this.store.pendingCount() });
    this.changed(touched);
    this.kick(this.opts.writeDelay);
  }

  /** Mutations the server refused. */
  deadLetters(): Promise<DeadLetter[]> {
    return this.store.deadLetters();
  }

  /** Queues a refused mutation again (as a new mutation), e.g. after fixing data. */
  async retryDeadLetter(id: string): Promise<void> {
    const dl = await this.store.takeDeadLetter(id);
    if (!dl) return;
    const m = dl.mutation;
    await this.write([
      { entity: m.entity, id: m.entity_id, op: m.op, ...(m.set ? { set: m.set } : {}), ...(m.inc ? { inc: m.inc } : {}) },
    ]);
  }

  /** Forgets a refused mutation. */
  async discardDeadLetter(id: string): Promise<void> {
    await this.store.takeDeadLetter(id);
  }

  private changed(touched: Touched[], broadcast = true): void {
    if (!touched.length) return;
    this.emit("change", touched);
    if (broadcast) this.channel?.postMessage({ type: "change", touched });
  }

  private onMessage(msg: { type: string; touched?: Touched[] }): void {
    if (msg?.type === "kick") this.kick(this.opts.writeDelay);
    else if (msg?.type === "change" && msg.touched) {
      this.changed(msg.touched, false);
      void this.store.pendingCount().then((pending) => this.setStatus({ pending }));
    }
  }

  // ---- loop control ----------------------------------------------------

  /** Starts background sync (open does this unless autoStart is false). */
  start(): void {
    if (this.started) return;
    this.started = true;
    this.setStatus({ state: "idle" });
    const nav = globalThis.navigator as Navigator | undefined;
    if (nav?.locks?.request) {
      // One tab per database runs the loop; the others hand it work.
      void nav.locks.request(`golaravel-offline:${this.opts.database}`, () => {
        if (!this.started) return undefined;
        this.leader = true;
        this.setStatus({});
        this.kick(0);
        return new Promise<void>((resolve) => (this.releaseLeader = resolve));
      });
    } else {
      this.leader = true;
      this.setStatus({});
      this.kick(0);
    }
    const g = globalThis as unknown as Window;
    if (typeof g.addEventListener === "function") {
      const online = () => {
        this.attempt = 0;
        this.kick(0);
      };
      const visible = () => {
        if (globalThis.document?.visibilityState === "visible") this.kick(0);
      };
      g.addEventListener("online", online);
      globalThis.document?.addEventListener?.("visibilitychange", visible);
      this.unlisten.push(() => g.removeEventListener("online", online));
      this.unlisten.push(() => globalThis.document?.removeEventListener?.("visibilitychange", visible));
    }
  }

  /** Stops background sync; local reads and writes keep working. */
  stop(): void {
    this.started = false;
    this.clearTimer();
    this.leader = false;
    this.releaseLeader?.();
    this.releaseLeader = undefined;
    for (const f of this.unlisten.splice(0)) f();
    this.setStatus({ state: "stopped" });
  }

  /** Stops syncing and closes the database. */
  async close(): Promise<void> {
    this.stop();
    await this.running?.catch(() => undefined);
    this.channel?.close();
    this.store.close();
  }

  /**
   * Syncs now and resolves when done; rejects with the failure. In a tab
   * that is not the leader it asks the leader to sync instead. Without
   * start() (autoStart: false) it syncs once, with no background retries.
   */
  async syncNow(): Promise<void> {
    if (this.started && !this.leader) {
      this.channel?.postMessage({ type: "kick" });
      return;
    }
    return this.cycle();
  }

  private kick(delay: number): void {
    if (!this.started) return;
    if (!this.leader) {
      this.channel?.postMessage({ type: "kick" });
      return;
    }
    const at = this.opts.now() + delay;
    if (this.timer && at >= this.timerAt) return;
    this.clearTimer();
    this.timerAt = at;
    this.timer = setTimeout(() => {
      this.timer = undefined;
      this.timerAt = Infinity;
      this.cycle().catch(() => undefined);
    }, delay);
  }

  private clearTimer(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
    this.timerAt = Infinity;
  }

  private cycle(): Promise<void> {
    if (this.running) return this.running;
    this.running = (async () => {
      this.setStatus({ state: "syncing" });
      try {
        const session = await this.session();
        if (!(await this.store.getMeta<boolean>("snapshot"))) await this.snapshotAll(session);
        await this.pushAll(session);
        await this.pullAll(session);
        await this.store.prune(this.opts.now() - this.opts.keepAcknowledged);
        await this.store.setMeta({ hlc: this.clock.current });
        this.attempt = 0;
        this.setStatus({
          state: "idle",
          lastSyncAt: this.opts.now(),
          pending: await this.store.pendingCount(),
          error: undefined,
          nextRetryAt: undefined,
        });
        // Something was written while we were busy: go again.
        if (this._status.pending > 0) this.kick(this.opts.writeDelay);
        else if (this.opts.pollInterval > 0) this.kick(this.opts.pollInterval);
      } catch (err) {
        await this.failed(err);
        throw err;
      }
    })().finally(() => {
      this.running = undefined;
    });
    return this.running;
  }

  private async failed(err: unknown): Promise<void> {
    const pending = await this.store.pendingCount().catch(() => this._status.pending);
    if (err instanceof SyncHttpError) {
      switch (err.code) {
        case "device_revoked":
          // Signed out from the server; the app decides what happens next.
          this.clearTimer();
          this.setStatus({ state: "revoked", error: err.code, pending, nextRetryAt: undefined });
          return;
        case "resync_required":
          await this.store.setMeta({ snapshot: undefined, cursor: undefined });
          return this.retry({ state: "backoff", error: err.code, pending }, 0);
        case "unknown_device":
        case "reregister":
          await this.store.setMeta({ device: undefined, key: undefined, keypair: undefined });
          return this.retry({ state: "backoff", error: err.code, pending }, 0);
      }
      return this.retry({ state: err.transient ? "backoff" : "error", error: err.code, pending }, err.retryAfter);
    }
    if (err instanceof NetworkError) return this.retry({ state: "offline", error: err.message, pending });
    if (err instanceof RetryLater) return this.retry({ state: "backoff", error: "retry", pending });
    return this.retry({ state: "error", error: err instanceof Error ? err.message : String(err), pending });
  }

  private retry(status: Partial<SyncStatus>, retryAfter?: number): void {
    let delay = backoffDelay(this.attempt++, this.opts.backoff.initial, this.opts.backoff.max, this.opts.random);
    if (retryAfter !== undefined) delay = retryAfter === 0 ? Math.min(delay, 1000) : Math.max(delay, retryAfter * 1000);
    this.setStatus({ ...status, nextRetryAt: this.opts.now() + delay });
    this.kick(delay);
  }

  // ---- protocol ---------------------------------------------------------

  private async session(): Promise<Session> {
    const device = await this.store.getMeta<Device>("device");
    const key = await this.store.getMeta<CryptoKey>("key");
    if (device && (!this.opts.encrypt || key)) return key ? { device, key } : { device };

    const info: Record<string, string> = {
      name: this.opts.device?.name ?? "",
      platform: this.opts.device?.platform ?? "",
      app_version: this.opts.device?.appVersion ?? "",
    };
    let keypair: CryptoKeyPair | undefined;
    if (this.opts.encrypt) {
      keypair = await generateDeviceKeyPair();
      info.public_key = await exportPublicKey(keypair.publicKey);
    }
    const registered = await this.transport.call<Device>("POST", "/devices", { body: info });
    if (!this.opts.encrypt) {
      await this.store.setMeta({ device: registered, cursor: undefined, snapshot: undefined });
      return { device: registered };
    }
    if (!registered.server_key) throw new Error("@golaravel/offline: the server did not enable encryption for this device");
    const derived = await deriveDeviceKey(keypair!.privateKey, registered.server_key.public_key, registered.id);
    await this.store.setMeta({ device: registered, key: derived, cursor: undefined, snapshot: undefined });
    return { device: registered, key: derived };
  }

  private async call<T>(s: Session, endpoint: Endpoint, method: "GET" | "POST", path: string, body?: unknown): Promise<T> {
    const d = s.device;
    if (!s.key) return this.transport.call<T>(method, path, { device: d.id, body });
    const kid = d.server_key?.kid ?? "";
    const payload =
      body === undefined ? undefined : await seal(s.key, kid, d.id, aad("req", endpoint, d.tenant_id, d.id), JSON.stringify(body));
    const env = await this.transport.call<Envelope>(method, path, { device: d.id, body: payload });
    return JSON.parse(await open(s.key, env, aad("res", endpoint, d.tenant_id, d.id))) as T;
  }

  private async pushAll(s: Session): Promise<void> {
    const size = Math.max(1, this.opts.batchSize);
    for (;;) {
      const candidates = await this.store.pending(size + 200);
      if (!candidates.length) return;
      // Never split a group across batches: the server applies a group
      // only when all of it arrives together.
      let n = Math.min(size, candidates.length);
      const g = candidates[n - 1]!.group;
      while (g && n < candidates.length && candidates[n]!.group === g) n++;
      const batch = candidates.slice(0, n);

      const resp = await this.call<PushResponse>(s, "push", "POST", "/push", { device_id: s.device.id, mutations: batch });
      if (!Array.isArray(resp?.results) || resp.results.length !== batch.length) {
        throw new Error("@golaravel/offline: malformed push response");
      }
      this.clock.observe(resp.server_hlc);
      const { touched, rejected } = await this.store.applyResults(batch, resp.results);
      this.setStatus({ pending: await this.store.pendingCount() });
      this.changed(touched);
      if (rejected.length) this.emit("rejected", rejected);
      if (resp.results.some((r) => r.status === "retry")) throw new RetryLater("server asked to retry");
    }
  }

  private async pullAll(s: Session): Promise<void> {
    let cursor = (await this.store.getMeta<number>("cursor")) ?? 0;
    for (;;) {
      const resp = await this.call<PullResponse>(s, "pull", "GET", `/pull?cursor=${cursor}&limit=${this.opts.pullLimit}`);
      this.clock.observe(resp.server_hlc);
      this.changed(await this.store.applyChanges(resp.changes ?? [], { cursor: resp.cursor }));
      cursor = resp.cursor;
      if (!resp.has_more) return;
    }
  }

  private async snapshotAll(s: Session): Promise<void> {
    let start: number | undefined;
    for (const entity of this.opts.entities) {
      const seen = new Set<string>();
      let after = "";
      let complete = true;
      for (;;) {
        let page: SnapshotResponse;
        try {
          const q = `/snapshot?entity=${encodeURIComponent(entity)}&after=${encodeURIComponent(after)}&limit=${this.opts.pullLimit}`;
          page = await this.call<SnapshotResponse>(s, "snapshot", "GET", q);
        } catch (err) {
          if (err instanceof SyncHttpError && err.code === "unknown_entity") {
            complete = false; // not snapshot-able on the server: pull only
            break;
          }
          throw err;
        }
        if (after === "") start = Math.min(start ?? Infinity, page.cursor ?? 0);
        for (const r of page.records) seen.add(r.id);
        this.changed(await this.store.applySnapshot(entity, page.records));
        if (!page.has_more || !page.next) break;
        after = page.next;
      }
      if (complete) this.changed(await this.store.dropMissing(entity, seen));
    }
    await this.store.setMeta({ cursor: start ?? 0, snapshot: true });
  }
}

/** One entity type's local view. */
export class Table<T extends Record<string, unknown>> {
  constructor(
    private readonly client: SyncClient,
    readonly entity: string,
  ) {}

  get(id: string): Promise<(T & { id: string }) | undefined> {
    return this.client.store.get<T>(this.entity, id);
  }

  all(): Promise<(T & { id: string })[]> {
    return this.client.store.all<T>(this.entity);
  }

  /** Creates an entity offline; returns its (client-generated) id. */
  async create(fields: Partial<T>, id: string = ulid()): Promise<string> {
    await this.client.write([{ entity: this.entity, id, op: "create", set: fields }]);
    return id;
  }

  update(id: string, fields: Partial<T>): Promise<void> {
    return this.client.write([{ entity: this.entity, id, op: "update", set: fields }]);
  }

  /** Adds to a counter field; concurrent increments from devices add up. */
  increment(id: string, field: keyof T & string, by: number): Promise<void> {
    return this.client.write([{ entity: this.entity, id, op: "update", inc: { [field]: by } }]);
  }

  delete(id: string): Promise<void> {
    return this.client.write([{ entity: this.entity, id, op: "delete" }]);
  }

  /** Calls fn with all rows now and whenever they change. */
  subscribe(fn: (rows: (T & { id: string })[]) => void): () => void {
    let alive = true;
    const refresh = () =>
      void this.all().then((rows) => {
        if (alive) fn(rows);
      });
    const off = this.client.on("change", (touched) => {
      if (touched.some((t) => t.entity === this.entity)) refresh();
    });
    refresh();
    return () => {
      alive = false;
      off();
    };
  }
}

/** Collects the changes of a SyncClient.batch call. */
export class BatchWriter {
  readonly changes: Change[] = [];

  create(entity: string, fields: Record<string, unknown>, id: string = ulid()): string {
    this.changes.push({ entity, id, op: "create", set: fields });
    return id;
  }

  update(entity: string, id: string, fields: Record<string, unknown>): void {
    this.changes.push({ entity, id, op: "update", set: fields });
  }

  increment(entity: string, id: string, field: string, by: number): void {
    this.changes.push({ entity, id, op: "update", inc: { [field]: by } });
  }

  delete(entity: string, id: string): void {
    this.changes.push({ entity, id, op: "delete" });
  }
}
