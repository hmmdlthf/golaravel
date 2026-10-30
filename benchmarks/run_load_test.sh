#!/usr/bin/env bash
set -eo pipefail

BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$BENCH_DIR/.." && pwd)"
RESULTS_DIR="$BENCH_DIR/results"
mkdir -p "$RESULTS_DIR"

echo "=================================================================="
echo "  GoLaravel vs Gin vs Fiber vs Laravel Octane: High Concurrency HTTP Load Test"
echo "  Load Generator: hey (16 CPU cores, HTTP/1.1 Keep-Alive, Concurrency=200)"
echo "=================================================================="

# Function to run benchmark on a target server
run_bench_suite() {
    local NAME="$1"
    local PORT="$2"
    local START_CMD="$3"
    local WARMUP_TIME="${4:-2}"

    echo ""
    echo ">>> Starting $NAME on port :$PORT..."
    eval "$START_CMD" &
    local SERVER_PID=$!
    
    # Wait for server ready
    local READY=0
    for i in {1..30}; do
        if curl -s "http://127.0.0.1:$PORT/api/user" > /dev/null 2>&1; then
            READY=1
            break
        fi
        sleep 0.2
    done

    if [ "$READY" -ne 1 ]; then
        echo "ERROR: $NAME failed to start on port $PORT"
        kill -9 $SERVER_PID 2>/dev/null || true
        return 1
    fi

    echo "    $NAME is UP (PID: $SERVER_PID). Warming up..."
    hey -z 3s -c 50 "http://127.0.0.1:$PORT/api/user" > /dev/null 2>&1
    sleep 1

    echo "    [1/2] Benchmarking JSON endpoint (/api/user) - 10s @ c=200..."
    local JSON_OUT="$RESULTS_DIR/${NAME}_json.txt"
    hey -z 10s -c 200 "http://127.0.0.1:$PORT/api/user" > "$JSON_OUT" 2>&1

    echo "    [2/2] Benchmarking Dynamic Route (/api/users/101/posts/55) - 10s @ c=200..."
    local ROUTE_OUT="$RESULTS_DIR/${NAME}_route.txt"
    hey -z 10s -c 200 "http://127.0.0.1:$PORT/api/users/101/posts/55" > "$ROUTE_OUT" 2>&1

    echo "    Stopping $NAME..."
    kill -9 $SERVER_PID 2>/dev/null || true
    # Also kill any child processes if frankenphp/caddy
    pkill -f "frankenphp.*9004" 2>/dev/null || true
    pkill -f "bench-" 2>/dev/null || true
    sleep 1
}

# 1. Benchmark GoLaravel (:9001)
run_bench_suite "golaravel" 9001 "$ROOT_DIR/bin/bench-golaravel"

# 2. Benchmark Fiber (:9003)
run_bench_suite "fiber" 9003 "$ROOT_DIR/bin/bench-fiber"

# 3. Benchmark Gin (:9002)
run_bench_suite "gin" 9002 "$ROOT_DIR/bin/bench-gin"

# 4. Benchmark Laravel Octane (:9004)
run_bench_suite "laravel_octane" 9004 "frankenphp run --config $BENCH_DIR/servers/octane/Caddyfile"

echo ""
echo "=================================================================="
echo "  PARSING & COMPILING RESULTS TABLE"
echo "=================================================================="

parse_rps() {
    local FILE="$1"
    grep "Requests/sec:" "$FILE" | awk '{printf "%.0f", $2}'
}

parse_p99() {
    local FILE="$1"
    grep "99% in" "$FILE" | awk '{print $3}'
}

parse_avg() {
    local FILE="$1"
    grep "Average:" "$FILE" | awk '{print $2}'
}

echo ""
printf "%-18s | %-12s | %-10s | %-12s | %-10s\n" "Framework" "JSON RPS" "JSON p99" "Param RPS" "Param p99"
printf -- "-------------------+--------------+------------+--------------+------------\n"

for FRAMEWORK in "golaravel" "fiber" "gin" "laravel_octane"; do
    JSON_FILE="$RESULTS_DIR/${FRAMEWORK}_json.txt"
    ROUTE_FILE="$RESULTS_DIR/${FRAMEWORK}_route.txt"

    JSON_RPS=$(parse_rps "$JSON_FILE")
    JSON_P99=$(parse_p99 "$JSON_FILE")
    ROUTE_RPS=$(parse_rps "$ROUTE_FILE")
    ROUTE_P99=$(parse_p99 "$ROUTE_FILE")

    printf "%-18s | %-12s | %-10s | %-12s | %-10s\n" "$FRAMEWORK" "$JSON_RPS req/s" "$JSON_P99" "$ROUTE_RPS req/s" "$ROUTE_P99"
done
echo ""
