import "fake-indexeddb/auto";
import { describe, expect, test } from "vitest";
import { Clock, formatHLC, parseHLC } from "../src/hlc.js";
import { ulid } from "../src/ids.js";
import { LocalStore, applyLocal } from "../src/store.js";
import { backoffDelay } from "../src/transport.js";
import {
  aad,
  deriveDeviceKey,
  exportPublicKey,
  fromBase64Url,
  generateDeviceKeyPair,
  open,
  seal,
  toBase64Url,
  EnvelopeError,
} from "../src/crypto.js";
import type { Mutation } from "../src/types.js";

let dbn = 0;
const freshDB = () => `unit-${++dbn}-${Math.random()}`;

describe("ulid", () => {
  test("is a monotonic 26-char Crockford ULID", () => {
    let prev = "";
    for (let i = 0; i < 2000; i++) {
      const id = ulid();
      expect(id).toMatch(/^[0-7][0-9A-HJKMNP-TV-Z]{25}$/);
      expect(id > prev).toBe(true);
      prev = id;
    }
  });
  test("starts with the time, so later ids sort later", () => {
    const a = ulid(Date.now() + 60_000);
    const b = ulid(Date.now() + 120_000);
    expect(a.slice(0, 10) < b.slice(0, 10)).toBe(true);
  });
});

describe("hlc", () => {
  test("matches the server encoding and sorts as text", () => {
    expect(formatHLC({ wall: 1_700_000_000_000, count: 3, node: "a" })).toBe("001700000000000:00003:a");
    expect(parseHLC("001700000000000:00003:a")).toEqual({ wall: 1_700_000_000_000, count: 3, node: "a" });
    expect(parseHLC("junk")).toBeNull();
    let t = 1000;
    const c = new Clock("n", undefined, () => t);
    const a = c.now();
    const b = c.now();
    expect(b > a).toBe(true);
    c.observe("000000000005000:00007:server");
    expect(c.now()).toBe("000000000005000:00008:n");
    t = 9000;
    expect(c.now()).toBe("000000000009000:00000:n");
    const restored = new Clock("n", c.current, () => 1);
    expect(restored.now() > c.current).toBe(true);
  });
});

describe("backoff", () => {
  test("full jitter within the exponential cap", () => {
    expect(backoffDelay(0, 1000, 300_000, () => 1)).toBe(1000);
    expect(backoffDelay(3, 1000, 300_000, () => 1)).toBe(8000);
    expect(backoffDelay(30, 1000, 300_000, () => 1)).toBe(300_000);
    expect(backoffDelay(5, 1000, 300_000, () => 0)).toBe(250);
    for (let i = 0; i < 100; i++) {
      const d = backoffDelay(4, 1000, 300_000);
      expect(d).toBeGreaterThanOrEqual(250);
      expect(d).toBeLessThanOrEqual(16_000);
    }
  });
});

const mut = (p: Partial<Mutation> & Pick<Mutation, "entity_id" | "op">): Mutation => ({
  id: ulid(),
  entity: "product",
  hlc: "",
  at: 0,
  ...p,
});

describe("applyLocal", () => {
  test("create, update, counters, delete", () => {
    let d = applyLocal(null, mut({ entity_id: "p", op: "create", set: { name: "Tea", stock: 3 } }));
    d = applyLocal(d, mut({ entity_id: "p", op: "update", inc: { stock: -1, sold: 1 } }));
    expect(d).toEqual({ name: "Tea", stock: 2, sold: 1 });
    expect(applyLocal(null, mut({ entity_id: "p", op: "update", set: { a: 1 } }))).toBeNull();
    expect(applyLocal(d, mut({ entity_id: "p", op: "delete" }))).toBeNull();
  });
});

describe("LocalStore", () => {
  test("local writes stay visible over newer server state until acknowledged", async () => {
    const s = await LocalStore.open(freshDB());
    const create = mut({ entity_id: "p1", op: "create", set: { name: "Tea", stock: 10 } });
    await s.write([create]);
    await s.applyResults([create], [{ id: create.id, status: "applied", seq: 1 }]);
    await s.applyChanges([{ entity: "product", entity_id: "p1", seq: 1, version: 1, data: { name: "Tea", stock: 10 } }], {});

    const sale = mut({ entity_id: "p1", op: "update", inc: { stock: -2 } });
    await s.write([sale]);
    expect((await s.get("product", "p1"))?.stock).toBe(8);

    // Another till's sale arrives first: base becomes 9, our pending -2 replays on top.
    await s.applyChanges([{ entity: "product", entity_id: "p1", seq: 2, version: 2, data: { name: "Tea", stock: 9 } }], { cursor: 2 });
    expect((await s.get("product", "p1"))?.stock).toBe(7);
    expect(await s.getMeta("cursor")).toBe(2);

    // Stale changes are ignored.
    await s.applyChanges([{ entity: "product", entity_id: "p1", seq: 1, version: 1, data: { name: "Tea", stock: 10 } }], {});
    expect((await s.get("product", "p1"))?.stock).toBe(7);

    expect((await s.pending(10)).map((m) => m.id)).toEqual([sale.id]);
    await s.applyResults([sale], [{ id: sale.id, status: "applied", seq: 3 }]);
    expect(await s.pendingCount()).toBe(0);
    // Until the next pull, the row shows the base (9); the pull brings 7.
    await s.applyChanges([{ entity: "product", entity_id: "p1", seq: 3, version: 3, data: { name: "Tea", stock: 7 } }], {});
    expect((await s.get("product", "p1"))?.stock).toBe(7);
    s.close();
  });

  test("a rejected mutation is undone and kept as a dead letter", async () => {
    const s = await LocalStore.open(freshDB());
    const create = mut({ entity_id: "p9", op: "create", set: { name: "Bad" } });
    await s.write([create]);
    expect(await s.get("product", "p9")).toBeDefined();
    const { rejected } = await s.applyResults(
      [create],
      [{ id: create.id, status: "rejected", reason: "invalid", record: { id: "p9", version: 0, deleted: true } }],
    );
    expect(rejected).toHaveLength(1);
    expect(await s.get("product", "p9")).toBeUndefined();
    expect((await s.deadLetters())[0]?.reason).toBe("invalid");
    expect(await s.pendingCount()).toBe(0);
    s.close();
  });

  test("retry results stay queued; the outbox is append-only until pruned", async () => {
    let now = 1000;
    const s = await LocalStore.open(freshDB(), () => now);
    const a = mut({ entity_id: "a", op: "create", set: {} });
    const b = mut({ entity_id: "b", op: "create", set: {} });
    await s.write([a, b]);
    await s.applyResults([a, b], [{ id: a.id, status: "applied" }, { id: b.id, status: "retry" }]);
    expect((await s.pending(10)).map((m) => m.id)).toEqual([b.id]);
    expect(await s.outboxSize()).toBe(2);
    expect((await s.ack(a.id))?.status).toBe("applied");
    now = 5000;
    expect(await s.prune(2000)).toBe(1);
    expect(await s.outboxSize()).toBe(1);
    s.close();
  });

  test("dropMissing removes what the server lost but keeps pending creates", async () => {
    const s = await LocalStore.open(freshDB());
    await s.applySnapshot("product", [
      { id: "gone", version: 1, data: { name: "x" } },
      { id: "kept", version: 1, data: { name: "y" } },
    ]);
    await s.write([mut({ entity_id: "new", op: "create", set: { name: "z" } })]);
    await s.dropMissing("product", new Set(["kept"]));
    expect((await s.all("product")).map((r) => r.id).sort()).toEqual(["kept", "new"]);
    s.close();
  });
});

describe("crypto", () => {
  test("both sides derive the same key; envelopes bind their context", async () => {
    const device = await generateDeviceKeyPair();
    const server = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
    const serverPub = await exportPublicKey(server.publicKey);
    const k1 = await deriveDeviceKey(device.privateKey, serverPub, "dev-1");
    const k2 = await deriveDeviceKey(server.privateKey, await exportPublicKey(device.publicKey), "dev-1");
    const ad = aad("req", "push", "acme", "dev-1");
    const env = await seal(k1, "k1", "dev-1", ad, '{"x":1}');
    expect(await open(k2, env, ad)).toBe('{"x":1}');
    await expect(open(k2, env, aad("req", "pull", "acme", "dev-1"))).rejects.toBeInstanceOf(EnvelopeError);
    const k3 = await deriveDeviceKey(device.privateKey, serverPub, "dev-2");
    await expect(open(k3, env, ad)).rejects.toBeInstanceOf(EnvelopeError);
    expect(toBase64Url(fromBase64Url("AQID_-8"))).toBe("AQID_-8");
  });
});
