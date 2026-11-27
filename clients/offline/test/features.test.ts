import { describe, expect, test } from "vitest";
import { FeatureClient, type FeatureStorage } from "../src/features.js";
import { NetworkError, SyncHttpError } from "../src/transport.js";

// A stand-in for Manager.Handler: ETag + If-None-Match, 403 without tenant.
class FakeFeatures {
  features: Record<string, unknown> = { "invoices.recurring": false, "ui.theme": { color: "blue" } };
  offline = false;
  status = 200;
  requests: { etag: string | null; auth: string | null }[] = [];
  tag = () => `"${btoa(JSON.stringify(this.features))}"`;
  fetch: typeof fetch = async (_input, init) => {
    const h = new Headers(init?.headers);
    this.requests.push({ etag: h.get("If-None-Match"), auth: h.get("Authorization") });
    if (this.offline) throw new TypeError("fetch failed");
    if (this.status !== 200) return Response.json({ error: "Tenant Required" }, { status: this.status });
    if (h.get("If-None-Match") === this.tag()) return new Response(null, { status: 304 });
    return Response.json({ features: this.features }, { headers: { ETag: this.tag() } });
  };
}

class MapStorage implements FeatureStorage {
  m = new Map<string, string>();
  get = (k: string) => this.m.get(k) ?? null;
  set = (k: string, v: string) => void this.m.set(k, v);
}

const open = (srv: FakeFeatures, storage: FeatureStorage | false = new MapStorage(), extra = {}) =>
  FeatureClient.open({ url: "https://api.test/features", fetch: srv.fetch, storage, autoStart: false, pollInterval: 0, ...extra });

describe("FeatureClient", () => {
  test("unknown features are off until the server answers", async () => {
    const c = await open(new FakeFeatures());
    expect(c.enabled("invoices.recurring")).toBe(false);
    expect(c.value("ui.theme", null)).toBeNull();
    expect(c.fetchedAt).toBeUndefined();
  });

  test("refresh loads values, then revalidates with the ETag", async () => {
    const srv = new FakeFeatures();
    const c = await open(srv, new MapStorage(), { headers: () => ({ Authorization: "Bearer t" }) });
    const changes: Record<string, unknown>[] = [];
    c.on("change", (f) => changes.push(f));

    expect(await c.refresh()).toBe(true);
    expect(c.value("ui.theme", {})).toEqual({ color: "blue" });
    expect(srv.requests[0]).toEqual({ etag: null, auth: "Bearer t" });

    expect(await c.refresh()).toBe(false); // 304
    expect(srv.requests[1]!.etag).toBe(srv.tag());
    expect(changes).toHaveLength(1);

    srv.features = { ...srv.features, "invoices.recurring": true };
    expect(await c.refresh()).toBe(true);
    expect(c.enabled("invoices.recurring")).toBe(true);
    expect(changes).toHaveLength(2);
  });

  test("keeps the last known features offline and across restarts", async () => {
    const srv = new FakeFeatures();
    srv.features["invoices.recurring"] = true;
    const storage = new MapStorage();
    const first = await open(srv, storage);
    await first.refresh();
    first.close();

    srv.offline = true;
    const second = await open(srv, storage);
    expect(second.enabled("invoices.recurring")).toBe(true); // from cache, no network
    const errors: unknown[] = [];
    second.on("error", (e) => errors.push(e));
    await expect(second.refresh()).rejects.toBeInstanceOf(NetworkError);
    expect(second.enabled("invoices.recurring")).toBe(true);
    expect(errors).toHaveLength(1);

    // Back online: the cached ETag still validates.
    srv.offline = false;
    expect(await second.refresh()).toBe(false);
    expect(srv.requests.at(-1)!.etag).toBe(srv.tag());
  });

  test("server errors are reported and do not clear values", async () => {
    const srv = new FakeFeatures();
    const c = await open(srv);
    await c.refresh();
    srv.status = 403;
    const err = await c.refresh().catch((e) => e);
    expect(err).toBeInstanceOf(SyncHttpError);
    expect((err as SyncHttpError).status).toBe(403);
    expect(c.value("ui.theme", null)).toEqual({ color: "blue" });
  });

  test("ignores a corrupt cache and works without storage", async () => {
    const storage = new MapStorage();
    storage.m.set("golaravel-features:https://api.test/features", "{not json");
    const c = await open(new FakeFeatures(), storage);
    expect(c.all()).toEqual({});
    const none = await open(new FakeFeatures(), false);
    await none.refresh();
    expect(none.value("ui.theme", null)).toEqual({ color: "blue" });
  });

  test("concurrent refreshes share one request", async () => {
    const srv = new FakeFeatures();
    const c = await open(srv);
    await Promise.all([c.refresh(), c.refresh(), c.refresh()]);
    expect(srv.requests).toHaveLength(1);
  });

  test("all() returns a copy", async () => {
    const c = await open(new FakeFeatures());
    await c.refresh();
    (c.all()["ui.theme"] as { color: string }).color = "red";
    expect(c.value("ui.theme", {})).toEqual({ color: "blue" });
  });
});
