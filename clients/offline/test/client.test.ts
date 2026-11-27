import "fake-indexeddb/auto";
import { afterEach, describe, expect, test } from "vitest";
import { SyncClient, type SyncOptions } from "../src/client.js";
import { FakeServer } from "./fake-server.js";

const open: SyncClient[] = [];
let n = 0;

async function client(server: FakeServer, opts: Partial<SyncOptions> = {}): Promise<SyncClient> {
  const c = await SyncClient.open({
    url: "http://sync.test/sync",
    entities: ["product", "sale"],
    database: `client-${++n}-${Math.random()}`,
    fetch: server.fetch,
    autoStart: false,
    pollInterval: 0,
    ...opts,
  });
  open.push(c);
  return c;
}

afterEach(async () => {
  for (const c of open.splice(0)) await c.close();
});

describe("SyncClient", () => {
  test("writes work offline and reach the server when it comes back", async () => {
    const server = new FakeServer();
    server.offline = 100;
    const c = await client(server);
    const products = c.table<{ name: string; stock: number }>("product");
    const id = await products.create({ name: "Tea", stock: 10 });
    await products.increment(id, "stock", -2);
    expect((await products.get(id))?.stock).toBe(8);
    expect(c.status.pending).toBe(2);

    await expect(c.syncNow()).rejects.toThrow();
    expect(c.status.state).toBe("offline");

    server.offline = 0;
    await c.syncNow();
    expect(c.status).toMatchObject({ state: "idle", pending: 0 });
    expect(server.rows.get(`product/${id}`)?.data).toEqual({ name: "Tea", stock: 8 });
    expect((await products.get(id))?.stock).toBe(8);
  });

  test("first sync loads a snapshot and server edits arrive by pull", async () => {
    const server = new FakeServer();
    server.edit("product", "p1", { name: "From admin", stock: 5 });
    const c = await client(server, { entities: ["product", "unsynced"] });
    await c.syncNow();
    expect(await c.table("product").get("p1")).toMatchObject({ name: "From admin" });

    server.edit("product", "p1", { name: "Renamed" });
    const seen: string[] = [];
    c.on("change", (t) => seen.push(...t.map((x) => x.id)));
    await c.syncNow();
    expect(await c.table("product").get("p1")).toMatchObject({ name: "Renamed", stock: 5 });
    expect(seen).toContain("p1");
  });

  test("a retried batch is not applied twice (lost acknowledgment)", async () => {
    const server = new FakeServer();
    const c = await client(server);
    const id = await c.table("product").create({ stock: 1 });
    await c.table("product").increment(id, "stock", 4);
    // The server applies the push, but the response never arrives.
    server.dropPushResponse = true;
    await expect(c.syncNow()).rejects.toThrow();
    expect(c.status.pending).toBe(2);
    await c.syncNow();
    expect(server.pushes).toHaveLength(2);
    expect(server.rows.get(`product/${id}`)?.data.stock).toBe(5);
    expect(c.status.pending).toBe(0);
  });

  test("groups are never split across batches", async () => {
    const server = new FakeServer();
    const c = await client(server, { batchSize: 2 });
    await c.table("product").create({ name: "a" });
    await c.batch((b) => {
      const sale = b.create("sale", { total: 300 });
      b.create("sale", { sale, line: 1 });
      b.increment("product", "p-x", "stock", -1);
    });
    await c.syncNow();
    expect(server.pushes.map((p) => p.length)).toEqual([4]);
    const groups = new Set(server.pushes[0]!.slice(1).map((m) => m.group));
    expect(groups.size).toBe(1);
  });

  test("a retry result stops the batch and backs off", async () => {
    const server = new FakeServer();
    const c = await client(server, { random: () => 1 });
    const a = await c.table("product").create({ n: 1 });
    await c.table("product").create({ n: 2 });
    const [first] = await c.store.pending(1);
    server.retryOnce.add(first!.id);
    await expect(c.syncNow()).rejects.toThrow();
    expect(c.status).toMatchObject({ state: "backoff", error: "retry", pending: 2 });
    expect(c.status.nextRetryAt).toBeGreaterThan(Date.now());
    await c.syncNow();
    expect(c.status.pending).toBe(0);
    expect(server.rows.has(`product/${a}`)).toBe(true);
  });

  test("rejected mutations are undone locally and reported", async () => {
    const server = new FakeServer();
    const c = await client(server);
    const products = c.table("product");
    const id = await products.create({ name: "Bad" });
    const [m] = await c.store.pending(1);
    server.reject.add(m!.id);
    const rejected: string[] = [];
    c.on("rejected", (d) => rejected.push(...d.map((x) => x.reason)));
    await c.syncNow();
    expect(rejected).toEqual(["invalid: test"]);
    expect(await products.get(id)).toBeUndefined();
    const [dl] = await c.deadLetters();
    expect(dl?.mutation.entity_id).toBe(id);

    server.reject.clear();
    await c.retryDeadLetter(dl!.id);
    await c.syncNow();
    expect(await products.get(id)).toMatchObject({ name: "Bad" });
    expect(await c.deadLetters()).toHaveLength(0);
  });

  test("410 resync reloads from a snapshot", async () => {
    const server = new FakeServer();
    const c = await client(server);
    server.edit("product", "p1", { name: "one" });
    await c.syncNow();
    server.edit("product", "p2", { name: "two" });
    server.rows.delete("product/p1"); // hard-deleted, and the history compacted
    server.horizon = server.seq + 1;
    await expect(c.syncNow()).rejects.toThrow();
    expect(c.status.error).toBe("resync_required");
    server.horizon = 0;
    await c.syncNow();
    const ids = (await c.table("product").all()).map((r) => r.id).sort();
    expect(ids).toEqual(["p2"]);
  });

  test("a revoked device stops; an unknown one registers again", async () => {
    const server = new FakeServer();
    const c = await client(server);
    await c.syncNow();
    server.devices.clear();
    await expect(c.syncNow()).rejects.toThrow();
    expect(c.status.error).toBe("unknown_device");
    await c.syncNow();
    expect(server.devices.has("dev-2")).toBe(true);

    server.revoked.add("dev-2");
    await expect(c.syncNow()).rejects.toThrow();
    expect(c.status.state).toBe("revoked");
  });

  test("Retry-After is honored", async () => {
    const server = new FakeServer();
    const c = await client(server, { random: () => 0 });
    server.failures.push({ status: 503, code: "unavailable", retryAfter: 60 });
    await expect(c.syncNow()).rejects.toThrow();
    expect(c.status.state).toBe("backoff");
    expect(c.status.nextRetryAt! - Date.now()).toBeGreaterThan(55_000);
  });

  test("background loop retries with backoff until the server is back", async () => {
    const server = new FakeServer();
    server.offline = 2;
    const c = await client(server, { autoStart: true, backoff: { initial: 20, max: 50 }, writeDelay: 0 });
    await c.table("product").create({ name: "Tea" });
    await waitFor(() => c.status.state === "idle" && c.status.pending === 0);
    expect(server.rows.size).toBe(1);
  });

  // Needs the Web Locks API (browsers; Node 22+).
  test.skipIf(!globalThis.navigator?.locks)("one tab syncs; others hand it their writes", async () => {
    const server = new FakeServer();
    const database = `tabs-${Math.random()}`;
    const leader = await client(server, { database, autoStart: true, writeDelay: 0 });
    await waitFor(() => leader.status.leader);
    const follower = await client(server, { database, autoStart: true, writeDelay: 0 });
    await new Promise((r) => setTimeout(r, 20));
    expect(follower.status.leader).toBe(false);
    const changes: string[] = [];
    follower.on("change", (t) => changes.push(...t.map((x) => x.id)));
    const id = await follower.table("product").create({ name: "from tab 2" });
    await waitFor(() => server.rows.has(`product/${id}`));
    await waitFor(() => changes.filter((x) => x === id).length >= 2); // local write + leader's sync
  });
});

async function waitFor(cond: () => boolean, ms = 3000): Promise<void> {
  const until = Date.now() + ms;
  while (!cond()) {
    if (Date.now() > until) throw new Error("timed out");
    await new Promise((r) => setTimeout(r, 5));
  }
}
