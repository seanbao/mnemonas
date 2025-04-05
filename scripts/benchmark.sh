#!/bin/bash
# Performance benchmark script for MnemoNAS WebDAV
# Tests PROPFIND response time with varying directory sizes

set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
DAV_URL="$BASE_URL/dav"
STORAGE_ROOT="${MNEMONAS_STORAGE_ROOT:-$HOME/.mnemonas}"
TEST_DIR="$STORAGE_ROOT/files/benchmark-test"

cleanup() {
    rm -rf "$TEST_DIR"
}

trap cleanup EXIT

run_propfind() {
    local path=$1
    local depth=$2
    local status

    if ! status=$(curl -sS -o /dev/null -w "%{http_code}" -X PROPFIND -H "Depth: $depth" "$DAV_URL$path"); then
        echo "PROPFIND $path failed to reach $DAV_URL$path" >&2
        return 1
    fi
    if [ "$status" != "207" ]; then
        echo "PROPFIND $path returned unexpected HTTP status: $status" >&2
        return 1
    fi
}

echo "=== MnemoNAS WebDAV Performance Benchmark ==="
echo "Base URL: $BASE_URL"
echo "Storage Root: $STORAGE_ROOT"
echo ""

# Function to create test files
create_test_files() {
    local count=$1
    local dir="$TEST_DIR/dir-$count"
    
    echo "Creating $count test files in $dir..."
    mkdir -p "$dir"
    
    for i in $(seq 1 $count); do
        printf 'benchmark file %05d\n' "$i" > "$dir/file-$(printf '%05d' $i).txt"
    done
}

# Function to benchmark PROPFIND
benchmark_propfind() {
    local path=$1
    local depth=$2
    local desc=$3
    
    echo -n "PROPFIND $desc (Depth: $depth): "
    
    # Warm up cache
    run_propfind "$path" "$depth"
    
    # Measure time (3 runs, take average)
    local total=0
    for i in 1 2 3; do
        local start=$(date +%s%N)
        run_propfind "$path" "$depth"
        local end=$(date +%s%N)
        local duration=$(( (end - start) / 1000000 ))
        total=$((total + duration))
    done
    
    local avg=$((total / 3))
    echo "${avg}ms (avg of 3 runs)"
}

# Function to benchmark GET
benchmark_get() {
    local path=$1
    local desc=$2
    
    echo -n "GET $desc: "
    
    local start=$(date +%s%N)
    curl -s "$DAV_URL$path" > /dev/null 2>&1 || true
    local end=$(date +%s%N)
    local duration=$(( (end - start) / 1000000 ))
    
    echo "${duration}ms"
}

# Clean up old test data
cleanup
mkdir -p "$TEST_DIR"

echo "--- Creating test directories ---"

# Create directories with different file counts
create_test_files 10
create_test_files 100
create_test_files 500
create_test_files 1000

echo ""
echo "--- PROPFIND Benchmarks ---"

# Test root directory
benchmark_propfind "/" "1" "root"

# Test different directory sizes
benchmark_propfind "/benchmark-test/dir-10" "1" "10 files"
benchmark_propfind "/benchmark-test/dir-100" "1" "100 files"
benchmark_propfind "/benchmark-test/dir-500" "1" "500 files"
benchmark_propfind "/benchmark-test/dir-1000" "1" "1000 files"

echo ""
echo "--- Cache Effect Test ---"

# First request (cache miss)
echo "Testing cache effect on 1000-file directory..."
echo -n "First request (cold): "
start=$(date +%s%N)
run_propfind "/benchmark-test/dir-1000" "1"
end=$(date +%s%N)
echo "$(( (end - start) / 1000000 ))ms"

# Second request (cache hit)
echo -n "Second request (cached): "
start=$(date +%s%N)
run_propfind "/benchmark-test/dir-1000" "1"
end=$(date +%s%N)
echo "$(( (end - start) / 1000000 ))ms"

echo ""
echo "--- API Metrics ---"
if metrics_json=$(curl -fsS "$BASE_URL/api/v1/metrics"); then
    printf '%s' "$metrics_json" | python3 -c "
import sys, json
data = json.load(sys.stdin)['data']
print(f\"Total requests: {data['requests']['total']}\")
print(f\"Avg latency: {data['latency']['avg_ms']:.2f}ms\")
print(f\"Max latency: {data['latency']['max_ms']:.2f}ms\")
print(f\"Error rate: {data['requests']['error_rate']*100:.2f}%\")
" 2>/dev/null || echo "(metrics parsing failed)"
else
    echo "(metrics request failed)"
fi

echo ""
echo "--- Cleanup ---"
cleanup
echo "Test files removed."

echo ""
echo "=== Benchmark Complete ==="
