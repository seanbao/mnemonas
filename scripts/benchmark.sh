#!/bin/bash
# Performance benchmark script for MnemoNAS WebDAV
# Tests PROPFIND response time with varying directory sizes

set -e

BASE_URL="${1:-http://localhost:8080}"
DAV_URL="$BASE_URL/dav"
TEST_DIR="$HOME/.mnemonas/metadata/benchmark-test"

echo "=== MnemoNAS WebDAV Performance Benchmark ==="
echo "Base URL: $BASE_URL"
echo ""

# Function to create test files
create_test_files() {
    local count=$1
    local dir="$TEST_DIR/dir-$count"
    
    echo "Creating $count test files in $dir..."
    mkdir -p "$dir"
    
    for i in $(seq 1 $count); do
        # Create minimal metadata files
        cat > "$dir/file-$(printf '%05d' $i).json" << EOF
{"name":"file-$(printf '%05d' $i).txt","isDir":false,"size":1024,"modTime":"$(date -Iseconds)","contentHash":"test$i"}
EOF
    done
}

# Function to benchmark PROPFIND
benchmark_propfind() {
    local path=$1
    local depth=$2
    local desc=$3
    
    echo -n "PROPFIND $desc (Depth: $depth): "
    
    # Warm up cache
    curl -s -X PROPFIND -H "Depth: $depth" "$DAV_URL$path" > /dev/null 2>&1 || true
    
    # Measure time (3 runs, take average)
    local total=0
    for i in 1 2 3; do
        local start=$(date +%s%N)
        curl -s -X PROPFIND -H "Depth: $depth" "$DAV_URL$path" > /dev/null 2>&1 || true
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
rm -rf "$TEST_DIR"
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
curl -s -X PROPFIND -H "Depth: 1" "$DAV_URL/benchmark-test/dir-1000" > /dev/null 2>&1 || true
end=$(date +%s%N)
echo "$(( (end - start) / 1000000 ))ms"

# Second request (cache hit)
echo -n "Second request (cached): "
start=$(date +%s%N)
curl -s -X PROPFIND -H "Depth: 1" "$DAV_URL/benchmark-test/dir-1000" > /dev/null 2>&1 || true
end=$(date +%s%N)
echo "$(( (end - start) / 1000000 ))ms"

echo ""
echo "--- API Metrics ---"
curl -s "$BASE_URL/api/v1/metrics" | python3 -c "
import sys, json
data = json.load(sys.stdin)['data']
print(f\"Total requests: {data['requests']['total']}\")
print(f\"Avg latency: {data['latency']['avg_ms']:.2f}ms\")
print(f\"Max latency: {data['latency']['max_ms']:.2f}ms\")
print(f\"Error rate: {data['requests']['error_rate']*100:.2f}%\")
" 2>/dev/null || echo "(metrics parsing failed)"

echo ""
echo "--- Cleanup ---"
rm -rf "$TEST_DIR"
echo "Test files removed."

echo ""
echo "=== Benchmark Complete ==="
