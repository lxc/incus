#!/bin/sh
keep_running="yes"
trap 'keep_running="no"' 1
while [ $keep_running = "yes" ]; do
    sleep 1
done

echo "Called with signal 1"


keep_running="yes"
trap 'keep_running="no"' "$(kill -l 10)"
while [ $keep_running = "yes" ]; do
    sleep 1
done

echo "Called with signal 10"

sleep 5
exit 1
