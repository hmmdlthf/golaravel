// An in-memory stand-in for pkg/offline, enough to drive the client in
// unit tests (the e2e test runs against the real Go server). Field-level
// LWW is reduced to "last arrival wins"; counters add up.
import type { Change, Mutation, Result } from "../src/types.js";

interface Row {
  data: Record<string, unknown>;
  version: number;
  deleted: boolean;
  seq: number;
}

export class FakeServer {
  rows = new Map<string, Row>();
  seen = new Map<string, Result>();
  seq = 0;
  horizon = 0;
  devices = new Set<string>();
  registered = 0;
  revoked = new Set<string>();
  pushes: Mutation[][] = [];
  /** Throw a network error for the next n requests. */
  offline = 0;
  /** Answer the next requests with these HTTP errors. */
  failures: { status: number; code: string; retryAfter?: number }[] = [];
  /** Mutation ids to reject / to answer "retry" for once. */
  reject = new Set<string>();
  retryOnce = new Set<string>();
  /** Apply the next push but lose its response, like a dropped connection. */
  dropPushResponse = false;

  key = (entity: string, id: string) => `${entity}/${id}`;

  fetch: typeof fetch = async (input, init) => {
    if (this.offline > 0) {
      this.offline--;
      throw new TypeError("fetch failed");
    }
    const url = new URL(String(input));
    const path = url.pathname.replace(/^\/sync/, "");
    const device = new Headers(init?.headers).get("X-Sync-Device") ?? "";
    const fail = this.failures.shift();
    if (fail) {
      const headers: Record<string, string> = {};
      if (fail.retryAfter) headers["Retry-After"] = String(fail.retryAfter);
      return json(fail.status, { error: fail.code, message: fail.code }, headers);
    }
    if (path === "/devices" && init?.method === "POST") {
      const id = `dev-${++this.registered}`;
      this.devices.add(id);
      return json(201, { id, tenant_id: "acme", cursor: 0, created_at: 0, encrypted: false });
    }
    if (!this.devices.has(device)) return json(403, { error: "unknown_device" });
    if (this.revoked.has(device)) return json(403, { error: "device_revoked" });
    if (path === "/push") {
      const body = this.push(JSON.parse(String(init?.body)).mutations);
      if (this.dropPushResponse) {
        this.dropPushResponse = false;
        throw new TypeError("connection reset");
      }
      return json(200, body);
    }
    if (path === "/pull") {
      const cursor = Number(url.searchParams.get("cursor"));
      if (cursor < this.horizon) return json(410, { error: "resync_required" });
      const limit = Number(url.searchParams.get("limit") || 500);
      const changed = [...this.rows.entries()].filter(([, r]) => r.seq > cursor).sort((a, b) => a[1].seq - b[1].seq);
      const page = changed.slice(0, limit);
      const changes: Change[] = page.map(([k, r]) => {
        const [entity, id] = k.split("/") as [string, string];
        return r.deleted
          ? { entity, entity_id: id, seq: r.seq, deleted: true, version: r.version }
          : { entity, entity_id: id, seq: r.seq, version: r.version, data: r.data };
      });
      return json(200, {
        changes,
        cursor: page.length ? page[page.length - 1]![1].seq : cursor,
        has_more: changed.length > limit,
        server_hlc: "",
      });
    }
    if (path === "/snapshot") {
      const entity = url.searchParams.get("entity")!;
      if (entity === "unsynced") return json(404, { error: "unknown_entity" });
      const records = [...this.rows.entries()]
        .filter(([k, r]) => k.startsWith(entity + "/") && !r.deleted)
        .map(([k, r]) => ({ id: k.split("/")[1]!, version: r.version, data: r.data }));
      return json(200, { entity, records, has_more: false, cursor: this.seq });
    }
    return json(404, { error: "not_found" });
  };

  push(muts: Mutation[]): { results: Result[]; server_hlc: string } {
    this.pushes.push(muts);
    const results: Result[] = [];
    let blocked = false;
    for (const m of muts) {
      if (blocked) {
        results.push({ id: m.id, status: "retry", reason: "blocked" });
        continue;
      }
      const prev = this.seen.get(m.id);
      if (prev) {
        results.push({ id: m.id, status: "duplicate", original: prev.status });
        continue;
      }
      if (this.retryOnce.delete(m.id)) {
        blocked = true;
        results.push({ id: m.id, status: "retry", reason: "transient" });
        continue;
      }
      const k = this.key(m.entity, m.entity_id);
      if (this.reject.has(m.id)) {
        const r: Result = { id: m.id, status: "rejected", reason: "invalid: test" };
        const cur = this.rows.get(k);
        r.record = cur && !cur.deleted ? { id: m.entity_id, version: cur.version, data: cur.data } : { id: m.entity_id, version: 0, deleted: true };
        this.seen.set(m.id, r);
        results.push(r);
        continue;
      }
      const cur = this.rows.get(k);
      const data = { ...(cur && !cur.deleted ? cur.data : {}), ...(m.set ?? {}) };
      for (const [f, by] of Object.entries(m.inc ?? {})) data[f] = ((data[f] as number) ?? 0) + by;
      this.rows.set(k, { data, version: (cur?.version ?? 0) + 1, deleted: m.op === "delete", seq: ++this.seq });
      const r: Result = { id: m.id, status: "applied", seq: this.seq };
      this.seen.set(m.id, r);
      results.push(r);
    }
    return { results, server_hlc: "" };
  }

  /** An edit made on the server (admin panel). */
  edit(entity: string, id: string, fields: Record<string, unknown>): void {
    const k = this.key(entity, id);
    const cur = this.rows.get(k);
    this.rows.set(k, { data: { ...(cur?.data ?? {}), ...fields }, version: (cur?.version ?? 0) + 1, deleted: false, seq: ++this.seq });
  }
}

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
}
