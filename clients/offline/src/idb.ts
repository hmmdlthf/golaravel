// Minimal promise helpers over IndexedDB.
//
// Inside a transaction, only await IndexedDB requests: awaiting anything
// else (fetch, WebCrypto, timers) lets the transaction auto-commit.

export function request<T>(r: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

export function openDatabase(
  name: string,
  version: number,
  upgrade: (db: IDBDatabase, oldVersion: number, tx: IDBTransaction) => void,
): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const r = indexedDB.open(name, version);
    r.onupgradeneeded = (ev) => upgrade(r.result, ev.oldVersion, r.transaction!);
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
    r.onblocked = () => reject(new Error(`IndexedDB ${name} is open in an older version elsewhere; reload other tabs`));
  });
}

/** Runs fn in a transaction and resolves once it has committed. */
export async function transact<T>(
  db: IDBDatabase,
  stores: string[],
  mode: IDBTransactionMode,
  fn: (tx: IDBTransaction) => Promise<T>,
): Promise<T> {
  const tx = db.transaction(stores, mode);
  const done = new Promise<void>((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error ?? new Error("IndexedDB transaction aborted"));
  });
  let result: T;
  try {
    result = await fn(tx);
  } catch (err) {
    try {
      tx.abort();
    } catch {
      // already finished
    }
    await done.catch(() => undefined);
    throw err;
  }
  await done;
  return result;
}

/** Iterates a cursor, stopping when visit returns false. */
export function iterate<T>(
  source: IDBObjectStore | IDBIndex,
  query: IDBKeyRange | IDBValidKey | null,
  visit: (value: T, cursor: IDBCursorWithValue) => boolean | void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const r = source.openCursor(query);
    r.onerror = () => reject(r.error);
    r.onsuccess = () => {
      const c = r.result;
      if (!c) return resolve();
      if (visit(c.value as T, c) === false) return resolve();
      c.continue();
    };
  });
}
