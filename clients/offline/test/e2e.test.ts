// End-to-end: the client against the real Go engine (pkg/offline), over
// HTTP, with and without payload encryption. Needs Go; runs when
// GOLARAVEL_E2E=1 (npm run test:e2e). GOLARAVEL_GO overrides the go binary.
import "fake-indexeddb/auto";
import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { SyncClient, type SyncOptions } from "../src/client.js";

const enabled = process.env.GOLARAVEL_E2E === "1";
const repo = resolve(__dirname, "../../..");
let server: ChildProcess | undefined;
let base = "";
let dir = "";
const clients: SyncClient[] = [];

beforeAll(async () => {
  if (!enabled) return;
  dir = mkdtempSync(join(tmpdir(), "golaravel-e2e-bin-"));
  const bin = join(dir, "e2eserver");
  execFileSync(process.env.GOLARAVEL_GO ?? "go", ["build", "-o", bin, "./pkg/offline/internal/e2eserver"], {
    cwd: repo,
    stdio: "inherit",
  });
  server = spawn(bin, ["-addr", "127.0.0.1:0"], { stdio: ["ignore", "pipe", "inherit"] });
  base = await new Promise<string>((ok, fail) => {
    const timer = setTimeout(() => fail(new Error("e2e server did not start")), 20_000);
    server!.stdout!.on("data", (b: Buffer) => {
      const m = /LISTENING (\S+)/.exec(b.toString());
      if (m) {
        clearTimeout(timer);
        ok(m[1]!);
      }
    });
    server!.on("exit", (code) => fail(new Error(`e2e server exited with ${code}`)));
  });
}, 180_000);

afterAll(async () => {
  for (const c of clients) await c.close();
  server?.kill();
  if (dir) rmSync(dir, { recursive: true, force: true });
});

const headers = (tenant = "acme") => ({ "X-Test-User": "cashier", "X-Test-Tenant": tenant });

async function open(opts: Partial<SyncOptions> = {}): Promise<SyncClient> {
  const c = await SyncClient.open({
    url: `${base}/sync`,
    entities: ["product"],
    database: `e2e-${Math.random()}`,
    headers: () => headers(),
    autoStart: false,
    pollInterval: 0,
    ...opts,
  });
  clients.push(c);
  return c;
}

async function admin(method: "GET" | "POST", id: string, body?: unknown, tenant = "acme") {
  const r = await fetch(`${base}/admin/products/${id}`, {
    method,
    headers: { ...headers(tenant), "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return { status: r.status, body: (await r.json()) as Record<string, unknown> };
}

describe.skipIf(!enabled).each([
  { mode: "plaintext", encrypt: false },
  { mode: "encrypted", encrypt: true },
])("against the Go server ($mode)", ({ encrypt }) => {
  test("offline writes, counters from two tills, admin edits, rejections", async () => {
    const till1 = await open({ encrypt });
    const till2 = await open({ encrypt });
    const p1 = till1.table<{ name: string; price: number; stock: number }>("product");
    const p2 = till2.table<{ name: string; price: number; stock: number }>("product");

    const id = await p1.create({ name: "Tea", price: 250, stock: 10 });
    await till1.syncNow();
    await till2.syncNow();
    expect(await p2.get(id)).toMatchObject({ name: "Tea", stock: 10 });

    // Both tills sell offline, then reconnect.
    await p1.increment(id, "stock", -2);
    await p2.increment(id, "stock", -3);
    await till1.syncNow();
    await till2.syncNow();
    await till1.syncNow();
    expect((await admin("GET", id)).body.stock).toBe(5);
    expect((await p1.get(id))?.stock).toBe(5);
    expect((await p2.get(id))?.stock).toBe(5);

    // An admin edit reaches the tills.
    await admin("POST", id, { name: "Green tea" });
    await till2.syncNow();
    expect((await p2.get(id))?.name).toBe("Green tea");

    // A sale grouped with an invalid change is rejected as a whole and undone.
    const rejected: string[] = [];
    till1.on("rejected", (d) => rejected.push(...d.map((x) => x.reason)));
    await till1.batch((b) => {
      b.increment("product", id, "stock", -1);
      b.update("product", id, { price: -1 });
    });
    expect((await p1.get(id))?.stock).toBe(4);
    await till1.syncNow();
    expect(rejected.some((r) => r.includes("price must not be negative"))).toBe(true);
    expect((await p1.get(id))?.stock).toBe(5);
    expect((await admin("GET", id)).body.stock).toBe(5);
    expect(till1.status.pending).toBe(0);
  }, 30_000);

  test("a new device loads existing data by snapshot; tenants stay apart", async () => {
    await admin("POST", `seed-${encrypt}`, { name: "Seeded", price: 1 });
    await admin("POST", `other-${encrypt}`, { name: "Globex only" }, "globex");
    const c = await open({ encrypt });
    await c.syncNow();
    const names = (await c.table("product").all()).map((r) => r.name);
    expect(names).toContain("Seeded");
    expect(names).not.toContain("Globex only");
  }, 30_000);

  test("deletes propagate", async () => {
    const a = await open({ encrypt });
    const b = await open({ encrypt });
    const id = await a.table("product").create({ name: "Temp" });
    await a.syncNow();
    await b.syncNow();
    expect(await b.table("product").get(id)).toBeDefined();
    await a.table("product").delete(id);
    await a.syncNow();
    await b.syncNow();
    expect(await b.table("product").get(id)).toBeUndefined();
    expect((await admin("GET", id)).body.deleted).toBe(true);
  }, 30_000);
});

test.skipIf(enabled)("e2e tests are skipped (set GOLARAVEL_E2E=1 to run them)", () => {});
