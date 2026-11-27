import type { Envelope } from "./types.js";

// Payload encryption, matching pkg/offline/crypto.go:
//
//   secret = ECDH-P256(device private, server public)     32 bytes
//   key    = HKDF-SHA256(secret, salt = device id, info = INFO) -> AES-256-GCM
//
// The device's private key and the derived AES key are non-extractable
// CryptoKeys, stored as-is in IndexedDB: script on the page can use them
// but never read them out.

const INFO = "golaravel-offline/1 aes-256-gcm";
const enc = new TextEncoder();
const dec = new TextDecoder();

export function toBase64Url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function fromBase64Url(s: string): Uint8Array {
  const b = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4));
  const out = new Uint8Array(b.length);
  for (let i = 0; i < b.length; i++) out[i] = b.charCodeAt(i);
  return out;
}

export async function generateDeviceKeyPair(): Promise<CryptoKeyPair> {
  return crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, false, ["deriveBits"]);
}

export async function exportPublicKey(key: CryptoKey): Promise<string> {
  return toBase64Url(new Uint8Array(await crypto.subtle.exportKey("raw", key)));
}

export async function deriveDeviceKey(privateKey: CryptoKey, serverPublicKey: string, deviceId: string): Promise<CryptoKey> {
  const server = await crypto.subtle.importKey(
    "raw",
    fromBase64Url(serverPublicKey),
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
  const secret = await crypto.subtle.deriveBits({ name: "ECDH", public: server }, privateKey, 256);
  const hkdf = await crypto.subtle.importKey("raw", secret, "HKDF", false, ["deriveKey"]);
  return crypto.subtle.deriveKey(
    { name: "HKDF", hash: "SHA-256", salt: enc.encode(deviceId), info: enc.encode(INFO) },
    hkdf,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"],
  );
}

export type Direction = "req" | "res";
export type Endpoint = "push" | "pull" | "snapshot";

/** Binds an envelope to its direction, endpoint, tenant and device. */
export function aad(dir: Direction, endpoint: Endpoint, tenant: string, deviceId: string): Uint8Array {
  return enc.encode(`golaravel-offline/1\n${dir}\n${endpoint}\n${tenant}\n${deviceId}`);
}

export async function seal(key: CryptoKey, kid: string, deviceId: string, data: Uint8Array, plaintext: string): Promise<Envelope> {
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv, additionalData: data }, key, enc.encode(plaintext));
  return { v: 1, kid, device_id: deviceId, iv: toBase64Url(iv), ct: toBase64Url(new Uint8Array(ct)) };
}

export class EnvelopeError extends Error {
  constructor() {
    super("envelope does not decrypt");
    this.name = "EnvelopeError";
  }
}

export async function open(key: CryptoKey, env: Envelope, data: Uint8Array): Promise<string> {
  if (env?.v !== 1 || typeof env.iv !== "string" || typeof env.ct !== "string") throw new EnvelopeError();
  try {
    const pt = await crypto.subtle.decrypt(
      { name: "AES-GCM", iv: fromBase64Url(env.iv), additionalData: data },
      key,
      fromBase64Url(env.ct),
    );
    return dec.decode(pt);
  } catch {
    throw new EnvelopeError();
  }
}
