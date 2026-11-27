const ENCODING = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

let lastTime = -1;
let lastRandom = new Uint8Array(10);

/**
 * Returns a ULID (48-bit milliseconds + 80 random bits, Crockford base32).
 * IDs from one page increase monotonically, so the outbox's key order is
 * the order mutations were made.
 */
export function ulid(now: number = Date.now()): string {
  let time = now;
  let random: Uint8Array;
  if (time <= lastTime) {
    time = lastTime;
    random = lastRandom.slice();
    for (let i = random.length - 1; i >= 0; i--) {
      random[i] = (random[i]! + 1) & 0xff;
      if (random[i] !== 0) break;
    }
  } else {
    random = crypto.getRandomValues(new Uint8Array(10));
    lastTime = time;
  }
  lastRandom = random;
  return encodeTime(time) + encodeRandom(random);
}

function encodeTime(ms: number): string {
  let out = "";
  for (let i = 0; i < 10; i++) {
    out = ENCODING[ms % 32]! + out;
    ms = Math.floor(ms / 32);
  }
  return out;
}

function encodeRandom(bytes: Uint8Array): string {
  // 80 bits -> 16 chars of 5 bits.
  let out = "";
  let buffer = 0;
  let bits = 0;
  for (const b of bytes) {
    buffer = (buffer << 8) | b;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      out += ENCODING[(buffer >> bits) & 31];
    }
    buffer &= (1 << bits) - 1;
  }
  return out;
}
