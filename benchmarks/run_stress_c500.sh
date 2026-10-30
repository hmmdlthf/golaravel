#!/usr/bin/env bash
set -eo pipefail

BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$BENCH_DIR/.." && pwd)"
RESULTS_DIR="$BENCH_DIR/results_c500"
mkdir -p "$RESULTS_DIR"

echo "=== Extreme Concurrency Load Test (c=500, duration=5s) ==="

run_stress() {
    local NAME="$1"
    local PORT="$2"
    local START_CMD="$3"

    eval "$START_CMD" &
    local PID=$!
    sleep 1

    echo "Running c=500 on $NAME..."
    hey -z 5s -c 500 "http://127.0.0.1:$PORT/api/user" > "$RESULTS_DIR/${NAME}.txt" 2>&1 || true

    kill -9 $PID 2>/dev/null || true
    pkill -f "frankenphp.*9004" 2>/dev/null || true
    pkill -f "bench-" 2>/dev/null || true
    sleep 1
}

run_stress "golaravel" 9001 "$ROOT_DIR/bin/bench-golaravel"
run_stress "fiber" 9003 "$ROOT_DIR/bin/bench-fiber"
run_stress "gin" 9002 "$ROOT_DIR/bin/bench-gin"
run_stress "laravel_octane" 9004 "frankenphp run --config $BENCH_DIR/servers/octane/Caddyfile"

echo "Done c=500 stress test!"
