#!/bin/sh -eu
#
# Performance tests runner
#

[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

PERF_LOG_CSV="perf.csv"

# shellcheck disable=SC2034
INCUS_NETNS=""

import_subdir_files() {
    test  "$1"
    # shellcheck disable=SC2039,3043
    local file
    for file in "$1"/*.sh; do
        # shellcheck disable=SC1090
        . "$file"
    done
}

import_subdir_files includes

log_message() {
    echo "==>" "$@"
}

run_benchmark() {
    # shellcheck disable=SC2039,3043
    local label description
    label="$1"
    description="$2"
    shift 2

    log_message "Benchmark start: $label - $description"
    incus-benchmark "$@" --report-file "$PERF_LOG_CSV" --report-label "$label"
    log_message "Benchmark completed: $label"
}

cleanup() {
    if [ "$TEST_RESULT" != "success" ]; then
        rm -f "$PERF_LOG_CSV"
    fi
    incus-benchmark delete  # ensure all test containers have been deleted
    kill_incus "$INCUS_DIR"
    cleanup_incus "$TEST_DIR"
    log_message "Performance tests result: $TEST_RESULT"
}

trap cleanup EXIT HUP INT TERM

# Setup test directories
TEST_DIR=$(mktemp -d -p "$(pwd)" tmp.XXX)

if [ -n "${INCUS_TMPFS:-}" ]; then
  mount -t tmpfs tmpfs "${TEST_DIR}" -o mode=0751 -o size=6G
fi

INCUS_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
export INCUS_DIR
chmod +x "${TEST_DIR}" "${INCUS_DIR}"

if [ -z "${INCUS_BACKEND:-}" ]; then
    INCUS_BACKEND="dir"
fi

import_storage_backends

spawn_incus "${INCUS_DIR}" true
ensure_import_testimage

# shellcheck disable=SC2034
TEST_RESULT=failure

run_benchmark "create-one" "create 1 container" init --count 1 "${INCUS_TEST_IMAGE:-"testimage"}"
run_benchmark "start-one" "start 1 container" start
run_benchmark "stop-one" "stop 1 container" stop
run_benchmark "delete-one" "delete 1 container" delete
run_benchmark "create-128" "create 128 containers" init --count 128 "${INCUS_TEST_IMAGE:-"testimage"}"
run_benchmark "start-128" "start 128 containers" start
run_benchmark "delete-128" "delete 128 containers" delete

# shellcheck disable=SC2034
TEST_RESULT=success
