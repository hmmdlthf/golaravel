import { iterate, openDatabase, request, transact } from "./idb.js";
import type { Change, DeadLetter, Mutation, Result, ServerRecord, Status } from "./types.js";

// IndexedDB layout
//
//   entities  [entity, id] -> EntityRow   what the UI reads
//   outbox    id -> Mutation               append-only: entries never change
//   queue     id -> {id}                   outbox ids not yet acknowledged
//   acks      id -> AckRow                 the server's answer per mutation
//   dead      id -> DeadLetter             rejected mutations, for the UI
//   meta      key -> any                   device, cursor, clock, keys
//
// Each row keeps the last state the server confirmed (base) and the state
// the UI sees (data): base with every unacknowledged mutation replayed on
// top. When the server's state arrives, base is replaced and pending
// mutations replayed again, so local edits stay visible until the server
// has them, and a rejected edit disappears once it leaves the queue.

const DB_VERSION = 1;

export interface Base {
  /** null when the server has the entity deleted. */
  data: Record<string, unknown> | null;
  version: number;
  hlc: string;
}

export interface EntityRow {
  entity: string;
  id: string;
  base: Base | null;
  data: Record<string, unknown> | null;
  pending: number;
}

interface OutboxRow extends Mutation {
  ek: string;
}

export interface AckRow {
  id: string;
  status: Status;
  original?: Status;
  seq?: number;
  reason?: string;
  at: number;
}

export interface Touched {
  entity: string;
  id: string;
}

const ek = (entity: string, id: string) => `${entity}\u0000${id}`;

export function applyLocal(data: Record<string, unknown> | null, m: Mutation): Record<string, unknown> | null {
  if (m.op === "delete") return null;
  let out: Record<string, unknown> | null;
  if (m.op === "create") out = { ...(data ?? {}), ...(m.set ?? {}) };
  else if (data === null) return null; // update of something we do not have
  else out = { ...data, ...(m.set ?? {}) };
  for (const [field, by] of Object.entries(m.inc ?? {})) {
    const cur = out[field];
    out[field] = (typeof cur === "number" ? cur : 0) + by;
  }
  return out;
}

export class LocalStore {
  private constructor(
    readonly db: IDBDatabase,
    private readonly now: () => number,
  ) {}

  static async open(name: string, now: () => number = Date.now): Promise<LocalStore> {
    const db = await openDatabase(name, DB_VERSION, (db) => {
      const entities = db.createObjectStore("entities", { keyPath: ["entity", "id"] });
      entities.createIndex("entity", "entity");
      const outbox = db.createObjectStore("outbox", { keyPath: "id" });
      outbox.createIndex("ek", "ek");
      db.createObjectStore("queue", { keyPath: "id" });
      const acks = db.createObjectStore("acks", { keyPath: "id" });
      acks.createIndex("at", "at");
      db.createObjectStore("dead", { keyPath: "id" });
      db.createObjectStore("meta");
    });
    return new LocalStore(db, now);
  }

  close(): void {
    this.db.close();
  }

  // ---- local writes ---------------------------------------------------

  /** Appends mutations to the outbox and applies them locally, atomically. */
  async write(muts: Mutation[]): Promise<Touched[]> {
    return transact(this.db, ["entities", "outbox", "queue"], "readwrite", async (tx) => {
      const entities = tx.objectStore("entities");
      const touched = new Map<string, Touched>();
      for (const m of muts) {
        const key = ek(m.entity, m.entity_id);
        await request(tx.objectStore("outbox").add({ ...m, ek: key } satisfies OutboxRow));
        await request(tx.objectStore("queue").add({ id: m.id }));
        const row: EntityRow = (await request(entities.get([m.entity, m.entity_id]))) ?? {
          entity: m.entity,
          id: m.entity_id,
          base: null,
          data: null,
          pending: 0,
        };
        row.data = applyLocal(row.data, m);
        row.pending++;
        await request(entities.put(row));
        touched.set(key, { entity: m.entity, id: m.entity_id });
      }
      return [...touched.values()];
    });
  }

  // ---- reads ----------------------------------------------------------

  async get<T = Record<string, unknown>>(entity: string, id: string): Promise<(T & { id: string }) | undefined> {
    const row: EntityRow | undefined = await transact(this.db, ["entities"], "readonly", (tx) =>
      request(tx.objectStore("entities").get([entity, id])),
    );
    return row?.data ? ({ ...row.data, id } as T & { id: string }) : undefined;
  }

  async row(entity: string, id: string): Promise<EntityRow | undefined> {
    return transact(this.db, ["entities"], "readonly", (tx) => request(tx.objectStore("entities").get([entity, id])));
  }

  async all<T = Record<string, unknown>>(entity: string): Promise<(T & { id: string })[]> {
    const rows: EntityRow[] = await transact(this.db, ["entities"], "readonly", (tx) =>
      request(tx.objectStore("entities").index("entity").getAll(entity)),
    );
    return rows.filter((r) => r.data).map((r) => ({ ...r.data, id: r.id }) as T & { id: string });
  }

  /** Oldest unacknowledged mutations, in the order they were made. */
  async pending(limit: number): Promise<Mutation[]> {
    return transact(this.db, ["outbox", "queue"], "readonly", async (tx) => {
      const ids: string[] = [];
      await iterate<{ id: string }>(tx.objectStore("queue"), null, (v) => {
        ids.push(v.id);
        return ids.length < limit;
      });
      const out: Mutation[] = [];
      for (const id of ids) {
        const row: OutboxRow | undefined = await request(tx.objectStore("outbox").get(id));
        if (row) out.push(stripRow(row));
      }
      return out;
    });
  }

  async pendingCount(): Promise<number> {
    return transact(this.db, ["queue"], "readonly", (tx) => request(tx.objectStore("queue").count()));
  }

  // ---- server answers -------------------------------------------------

  /**
   * Records push results. Acknowledged mutations leave the queue; rejected
   * ones go to dead letters; records the server sent replace the base.
   */
  async applyResults(muts: Mutation[], results: Result[]): Promise<{ touched: Touched[]; rejected: DeadLetter[] }> {
    const at = this.now();
    return transact(this.db, ["entities", "outbox", "queue", "acks", "dead"], "readwrite", async (tx) => {
      const rejected: DeadLetter[] = [];
      const records = new Map<string, ServerRecord | undefined>();
      const touched = new Map<string, Touched>();
      for (let i = 0; i < results.length; i++) {
        const r = results[i]!;
        const m = muts[i];
        if (!m || r.id !== m.id || r.status === "retry") continue;
        const status = r.status === "duplicate" ? (r.original ?? "applied") : r.status;
        await request(tx.objectStore("queue").delete(m.id));
        const ack: AckRow = { id: m.id, status: r.status, at };
        if (r.original) ack.original = r.original;
        if (r.seq) ack.seq = r.seq;
        if (r.reason) ack.reason = r.reason;
        await request(tx.objectStore("acks").put(ack));
        if (status === "rejected") {
          const dl: DeadLetter = { id: m.id, mutation: m, reason: r.reason ?? "rejected", at };
          await request(tx.objectStore("dead").put(dl));
          rejected.push(dl);
        }
        const key = ek(m.entity, m.entity_id);
        touched.set(key, { entity: m.entity, id: m.entity_id });
        if (r.record) records.set(key, r.record);
      }
      for (const t of touched.values()) {
        const rec = records.get(ek(t.entity, t.id));
        await this.rebase(tx, t, rec ? toBase(rec) : undefined);
      }
      return { touched: [...touched.values()], rejected };
    });
  }

  /** Applies pulled changes and saves the cursor in the same transaction. */
  async applyChanges(changes: Change[], meta: Record<string, unknown>): Promise<Touched[]> {
    return transact(this.db, ["entities", "outbox", "queue", "meta"], "readwrite", async (tx) => {
      const touched: Touched[] = [];
      for (const c of changes) {
        const t = { entity: c.entity, id: c.entity_id };
        const base: Base = { data: c.deleted ? null : (c.data ?? {}), version: c.version ?? 0, hlc: c.hlc ?? "" };
        if (await this.rebase(tx, t, base)) touched.push(t);
      }
      for (const [k, v] of Object.entries(meta)) await request(tx.objectStore("meta").put(v, k));
      return touched;
    });
  }

  /** Stores one snapshot page as server state. */
  async applySnapshot(entity: string, records: ServerRecord[]): Promise<Touched[]> {
    return this.applyChanges(
      records.map((r) => ({
        entity,
        entity_id: r.id,
        seq: 0,
        version: r.version,
        ...(r.hlc ? { hlc: r.hlc } : {}),
        ...(r.data ? { data: r.data } : {}),
      })),
      {},
    );
  }

  /**
   * After a full snapshot: entities the server no longer has are dropped,
   * unless local mutations are still pending for them.
   */
  async dropMissing(entity: string, seen: Set<string>): Promise<Touched[]> {
    return transact(this.db, ["entities", "outbox", "queue"], "readwrite", async (tx) => {
      const rows: EntityRow[] = await request(tx.objectStore("entities").index("entity").getAll(entity));
      const touched: Touched[] = [];
      for (const row of rows) {
        if (row.base && !seen.has(row.id)) {
          const t = { entity, id: row.id };
          await this.rebase(tx, t, { data: null, version: row.base.version, hlc: row.base.hlc }, true);
          touched.push(t);
        }
      }
      return touched;
    });
  }

  /**
   * Replaces the row's base (when given and not older than the current
   * one) and replays pending mutations. Returns whether anything changed.
   */
  private async rebase(tx: IDBTransaction, t: Touched, base: Base | undefined, force = false): Promise<boolean> {
    const entities = tx.objectStore("entities");
    const row: EntityRow = (await request(entities.get([t.entity, t.id]))) ?? {
      entity: t.entity,
      id: t.id,
      base: null,
      data: null,
      pending: 0,
    };
    if (base) {
      if (!force && row.base && base.version < row.base.version) return false; // stale
      row.base = base;
    }
    const pending = await this.pendingFor(tx, ek(t.entity, t.id));
    let data = row.base?.data ? { ...row.base.data } : null;
    for (const m of pending) data = applyLocal(data, m);
    row.data = data;
    row.pending = pending.length;
    if (row.data === null && row.pending === 0 && row.base === null) {
      // Nothing known and nothing pending (a rejected local create).
      await request(entities.delete([t.entity, t.id]));
      return true;
    }
    // A deleted base stays as a tombstone so older changes are ignored.
    await request(entities.put(row));
    return true;
  }

  private async pendingFor(tx: IDBTransaction, key: string): Promise<Mutation[]> {
    const rows: OutboxRow[] = await request(tx.objectStore("outbox").index("ek").getAll(key));
    rows.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    const out: Mutation[] = [];
    for (const r of rows) {
      if ((await request(tx.objectStore("queue").count(r.id))) > 0) out.push(stripRow(r));
    }
    return out;
  }

  // ---- dead letters ---------------------------------------------------

  async deadLetters(): Promise<DeadLetter[]> {
    return transact(this.db, ["dead"], "readonly", (tx) => request(tx.objectStore("dead").getAll()));
  }

  async takeDeadLetter(id: string): Promise<DeadLetter | undefined> {
    return transact(this.db, ["dead"], "readwrite", async (tx) => {
      const dl: DeadLetter | undefined = await request(tx.objectStore("dead").get(id));
      if (dl) await request(tx.objectStore("dead").delete(id));
      return dl;
    });
  }

  // ---- meta -----------------------------------------------------------

  async getMeta<T>(key: string): Promise<T | undefined> {
    return transact(this.db, ["meta"], "readonly", (tx) => request(tx.objectStore("meta").get(key)));
  }

  async setMeta(values: Record<string, unknown>): Promise<void> {
    await transact(this.db, ["meta"], "readwrite", async (tx) => {
      for (const [k, v] of Object.entries(values)) {
        if (v === undefined) await request(tx.objectStore("meta").delete(k));
        else await request(tx.objectStore("meta").put(v, k));
      }
    });
  }

  // ---- housekeeping ---------------------------------------------------

  /**
   * Deletes outbox entries (and their acks) acknowledged before `before`.
   * The outbox is append-only while a mutation is in flight; once the
   * server has answered and the answer is old, the entry is only history.
   */
  async prune(before: number): Promise<number> {
    return transact(this.db, ["outbox", "acks"], "readwrite", async (tx) => {
      const ids: string[] = [];
      await iterate<AckRow>(tx.objectStore("acks").index("at"), IDBKeyRange.upperBound(before, true), (a) => {
        ids.push(a.id);
      });
      for (const id of ids) {
        await request(tx.objectStore("acks").delete(id));
        await request(tx.objectStore("outbox").delete(id));
      }
      return ids.length;
    });
  }

  async ack(id: string): Promise<AckRow | undefined> {
    return transact(this.db, ["acks"], "readonly", (tx) => request(tx.objectStore("acks").get(id)));
  }

  async outboxSize(): Promise<number> {
    return transact(this.db, ["outbox"], "readonly", (tx) => request(tx.objectStore("outbox").count()));
  }
}

function toBase(r: ServerRecord): Base {
  return { data: r.deleted ? null : (r.data ?? {}), version: r.version, hlc: r.hlc ?? "" };
}

function stripRow(row: OutboxRow): Mutation {
  const { ek: _ek, ...m } = row;
  void _ek;
  return m;
}
