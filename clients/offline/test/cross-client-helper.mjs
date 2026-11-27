// Drives a real @golaravel/offline SyncClient from another process, for the
// Dart package's cross-client end-to-end test (clients/flutter/test/
// cross_client_test.dart). Reads one JSON command per line on stdin and
// answers one JSON line on stdout: {"id", "ok", "result" | "error"}.
//
//   node test/cross-client-helper.mjs <sync url> <tenant> <encrypt: 0|1>
//
// Needs `npm run build` first (it imports ../dist).
import "fake-indexeddb/auto";
import { createInterface } from "node:readline";
import { SyncClient } from "../dist/index.js";

const [url, tenant, encrypt] = process.argv.slice(2);
const client = await SyncClient.open({
  url,
  entities: ["product"],
  database: `cross-${tenant}-${Math.random()}`,
  headers: () => ({ "X-Test-User": "ts-till", "X-Test-Tenant": tenant }),
  encrypt: encrypt === "1",
  device: { name: "ts cross-client", platform: "node" },
  autoStart: false,
  pollInterval: 0,
});
const products = client.table("product");
const rejected = [];
client.on("rejected", (d) => rejected.push(...d.map((x) => x.reason)));

const commands = {
  create: ({ fields, id }) => products.create(fields, id),
  update: ({ id, fields }) => products.update(id, fields),
  increment: ({ id, field, by }) => products.increment(id, field, by),
  delete: ({ id }) => products.delete(id),
  get: async ({ id }) => (await products.get(id)) ?? null,
  all: () => products.all(),
  batch: ({ changes }) =>
    client.batch((b) => {
      for (const c of changes) {
        if (c.op === "increment") b.increment("product", c.id, c.field, c.by);
        else if (c.op === "update") b.update("product", c.id, c.fields);
      }
    }),
  sync: () => client.syncNow(),
  status: () => client.status,
  rejected: () => rejected.splice(0),
  close: async () => {
    await client.close();
    setTimeout(() => process.exit(0), 10);
  },
};

process.stdout.write(JSON.stringify({ ready: true }) + "\n");
const rl = createInterface({ input: process.stdin });
let chain = Promise.resolve();
rl.on("line", (line) => {
  chain = chain.then(async () => {
    const { id, cmd, args } = JSON.parse(line);
    try {
      const result = await commands[cmd](args ?? {});
      process.stdout.write(JSON.stringify({ id, ok: true, result: result ?? null }) + "\n");
    } catch (err) {
      process.stdout.write(JSON.stringify({ id, ok: false, error: String(err?.message ?? err) }) + "\n");
    }
  });
});
