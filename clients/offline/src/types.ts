// Wire types, mirroring pkg/offline's JSON.

export type Op = "create" | "update" | "delete";

/** One immutable outbox entry. */
export interface Mutation {
  id: string;
  entity: string;
  entity_id: string;
  op: Op;
  set?: Record<string, unknown>;
  inc?: Record<string, number>;
  hlc: string;
  group?: string;
  base_version?: number;
  /** Client wall time (Unix ms). */
  at: number;
}

export type Status = "applied" | "resolved" | "duplicate" | "rejected" | "retry";

export interface ServerRecord {
  id: string;
  version: number;
  hlc?: string;
  deleted?: boolean;
  data?: Record<string, unknown>;
}

export interface Result {
  id: string;
  status: Status;
  original?: Status;
  seq?: number;
  reason?: string;
  record?: ServerRecord;
  entity?: string;
  entity_id?: string;
}

export interface PushResponse {
  results: Result[];
  server_hlc: string;
}

export interface Change {
  entity: string;
  entity_id: string;
  seq: number;
  deleted?: boolean;
  version?: number;
  hlc?: string;
  data?: Record<string, unknown>;
}

export interface PullResponse {
  changes: Change[];
  cursor: number;
  has_more: boolean;
  server_hlc: string;
}

export interface SnapshotResponse {
  entity: string;
  records: ServerRecord[];
  next?: string;
  has_more: boolean;
  cursor?: number;
}

export interface ServerPublicKey {
  kid: string;
  public_key: string;
}

export interface Device {
  id: string;
  tenant_id: string;
  user_id?: string;
  name?: string;
  platform?: string;
  app_version?: string;
  cursor: number;
  created_at: number;
  encrypted: boolean;
  server_key?: ServerPublicKey;
}

export interface Envelope {
  v: number;
  kid: string;
  device_id: string;
  iv: string;
  ct: string;
}

/** A mutation the server refused for good. */
export interface DeadLetter {
  id: string;
  mutation: Mutation;
  reason: string;
  at: number;
}
