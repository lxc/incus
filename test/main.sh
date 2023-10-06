#!/bin/sh -eu
[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

# Don't translate incus output for parsing in it in tests.
export LC_ALL="C"

# Force UTC for consistency
export TZ="UTC"

export DEBUG=""
if [ -n "${INCUS_VERBOSE:-}" ]; then
  DEBUG="--verbose"
fi

if [ -n "${INCUS_DEBUG:-}" ]; then
  DEBUG="--debug"
fi

if [ -n "${DEBUG:-}" ]; then
  set -x
fi

if [ -z "${INCUS_BACKEND:-}" ]; then
    INCUS_BACKEND="dir"
fi

# shellcheck disable=SC2034
INCUS_NETNS=""

import_subdir_files() {
    test "$1"
    # shellcheck disable=SC2039,3043
    local file
    for file in "$1"/*.sh; do
        # shellcheck disable=SC1090
        . "$file"
    done
}

import_subdir_files includes

echo "==> Checking for dependencies"
check_dependencies incusd incus curl dnsmasq jq git xgettext sqlite3 msgmerge msgfmt shuf setfacl socat dig

if [ "${USER:-'root'}" != "root" ]; then
  echo "The testsuite must be run as root." >&2
  exit 1
fi

if [ -n "${INCUS_LOGS:-}" ] && [ ! -d "${INCUS_LOGS}" ]; then
  echo "Your INCUS_LOGS path doesn't exist: ${INCUS_LOGS}"
  exit 1
fi

echo "==> Available storage backends: $(available_storage_backends | sort)"
if [ "$INCUS_BACKEND" != "random" ] && ! storage_backend_available "$INCUS_BACKEND"; then
  if [ "${INCUS_BACKEND}" = "ceph" ] && [ -z "${INCUS_CEPH_CLUSTER:-}" ]; then
    echo "Ceph storage backend requires that \"INCUS_CEPH_CLUSTER\" be set."
    exit 1
  fi
  echo "Storage backend \"$INCUS_BACKEND\" is not available"
  exit 1
fi
echo "==> Using storage backend ${INCUS_BACKEND}"

import_storage_backends

cleanup() {
  # Allow for failures and stop tracing everything
  set +ex
  DEBUG=

  # Allow for inspection
  if [ -n "${INCUS_INSPECT:-}" ]; then
    if [ "${TEST_RESULT}" != "success" ]; then
      echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION}"
    fi
    echo "==> Test result: ${TEST_RESULT}"

    # shellcheck disable=SC2086
    printf "To poke around, use:\\n INCUS_DIR=%s INCUS_CONF=%s sudo -E %s/bin/incus COMMAND\\n" "${INCUS_DIR}" "${INCUS_CONF}" ${GOPATH:-}
    echo "Tests Completed (${TEST_RESULT}): hit enter to continue"
    read -r _
  fi

  echo ""
  echo "df -h output:"
  df -h

  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "==> Skipping cleanup (GitHub Action runner detected)"
  else
    echo "==> Cleaning up"

    umount -l "${TEST_DIR}/dev"
    cleanup_incus "$TEST_DIR"
  fi

  if [ "${INCUS_BACKEND}" = "ceph" ]; then
    ceph status
  fi

  echo ""
  echo ""
  if [ "${TEST_RESULT}" != "success" ]; then
    echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION}"
  fi
  echo "==> Test result: ${TEST_RESULT}"
}

# Must be set before cleanup()
TEST_CURRENT=setup
TEST_CURRENT_DESCRIPTION=setup
# shellcheck disable=SC2034
TEST_RESULT=failure

trap cleanup EXIT HUP INT TERM

# Import all the testsuites
import_subdir_files suites

# Setup test directory
TEST_DIR=$(mktemp -d -p "$(pwd)" tmp.XXX)
chmod +x "${TEST_DIR}"

if [ -n "${INCUS_TMPFS:-}" ]; then
  mount -t tmpfs tmpfs "${TEST_DIR}" -o mode=0751 -o size=6G
fi

mkdir -p "${TEST_DIR}/dev"
mount -t tmpfs none "${TEST_DIR}"/dev
export INCUS_DEVMONITOR_DIR="${TEST_DIR}/dev"

INCUS_CONF=$(mktemp -d -p "${TEST_DIR}" XXX)
export INCUS_CONF

INCUS_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
export INCUS_DIR
chmod +x "${INCUS_DIR}"
spawn_incus "${INCUS_DIR}" true
INCUS_ADDR=$(cat "${INCUS_DIR}/incus.addr")
export INCUS_ADDR

run_test() {
  TEST_CURRENT=${1}
  TEST_CURRENT_DESCRIPTION=${2:-${1}}
  TEST_UNMET_REQUIREMENT=""

  echo "==> TEST BEGIN: ${TEST_CURRENT_DESCRIPTION}"
  START_TIME=$(date +%s)

  # shellcheck disable=SC2039,3043
  local skip=false

  # Skip test if requested.
  if [ -n "${INCUS_SKIP_TESTS:-}" ]; then
    for testName in ${INCUS_SKIP_TESTS}; do
      if [ "test_${testName}" = "${TEST_CURRENT}" ]; then
          echo "==> SKIP: ${TEST_CURRENT} as specified in INCUS_SKIP_TESTS"
          skip=true
          break
      fi
    done
  fi

  if [ "${skip}" = false ]; then
    # Run test.
    ${TEST_CURRENT}

    # Check whether test was skipped due to unmet requirements, and if so check if the test is required and fail.
    if [ -n "${TEST_UNMET_REQUIREMENT}" ]; then
      if [ -n "${INCUS_REQUIRED_TESTS:-}" ]; then
        for testName in ${INCUS_REQUIRED_TESTS}; do
          if [ "test_${testName}" = "${TEST_CURRENT}" ]; then
              echo "==> REQUIRED: ${TEST_CURRENT} ${TEST_UNMET_REQUIREMENT}"
              false
              return
          fi
        done
      else
        # Skip test if its requirements are not met and is not specified in required tests.
        echo "==> SKIP: ${TEST_CURRENT} ${TEST_UNMET_REQUIREMENT}"
      fi
    fi
  fi

  END_TIME=$(date +%s)

  echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION} ($((END_TIME-START_TIME))s)"
}

# allow for running a specific set of tests
if [ "$#" -gt 0 ] && [ "$1" != "all" ] && [ "$1" != "cluster" ] && [ "$1" != "standalone" ]; then
  run_test "test_${1}"
  # shellcheck disable=SC2034
  TEST_RESULT=success
  exit
fi

if [ "${INCUS_BACKEND}" = "ceph" ]; then
    run_test test_clustering_storage "clustering storage"
fi

# shellcheck disable=SC2034
TEST_RESULT=success
