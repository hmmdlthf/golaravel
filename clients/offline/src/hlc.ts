/**
 * Hybrid logical clock, encoded exactly like the server's:
 * "000001732700000000:00000:node". The text form sorts in clock order.
 */
export interface HLC {
  wall: number;
  count: number;
  node: string;
}

const MAX_COUNT = 99999;

export function formatHLC(h: HLC): string {
  return `${String(h.wall).padStart(15, "0")}:${String(h.count).padStart(5, "0")}:${h.node}`;
}

export function parseHLC(s: string): HLC | null {
  const m = /^(\d{15}):(\d{5}):(.{1,64})$/.exec(s);
  if (!m) return null;
  return { wall: Number(m[1]), count: Number(m[2]), node: m[3]! };
}

export class Clock {
  private last: HLC;

  constructor(
    private readonly node: string,
    last?: string,
    private readonly wallClock: () => number = Date.now,
  ) {
    this.last = (last && parseHLC(last)) || { wall: 0, count: 0, node };
  }

  /** A timestamp later than any issued or observed. */
  now(): string {
    const wall = this.wallClock();
    if (wall > this.last.wall) {
      this.last = { wall, count: 0, node: this.node };
    } else if (this.last.count >= MAX_COUNT) {
      this.last = { wall: this.last.wall + 1, count: 0, node: this.node };
    } else {
      this.last = { wall: this.last.wall, count: this.last.count + 1, node: this.node };
    }
    return formatHLC(this.last);
  }

  /** Merge a remote timestamp so later local ones sort after it. */
  observe(remote: string | undefined): void {
    const r = remote ? parseHLC(remote) : null;
    if (!r) return;
    if (r.wall > this.last.wall || (r.wall === this.last.wall && r.count > this.last.count)) {
      this.last = { wall: r.wall, count: r.count, node: this.node };
    }
  }

  /** The latest timestamp, to persist across reloads. */
  get current(): string {
    return formatHLC(this.last);
  }
}
