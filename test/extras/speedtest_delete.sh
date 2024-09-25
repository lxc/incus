#!/bin/bash

CNAME="speedtest"

count=${1}
if [ -z "${count}" ]; then
  echo "USAGE: ${0} 10"
  echo "This deletes 10 busybox containers"
  exit 1
fi

if [ "${2}" != "notime" ]; then
  time ${0} "${count}" notime
  exit 0
fi

PIDS=""
for c in $(seq "$count"); do
  incus delete "${CNAME}${c}" 2>&1 &
  PIDS="$PIDS $!"
done

for pid in $PIDS; do
  wait "$pid"
done

echo -e "\nRun completed"
