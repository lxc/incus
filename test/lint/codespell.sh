#!/bin/sh -eu
codespell -S 'internal/server/apparmor/*.profile.go' -S 'doc/html/*' -I .codespell-ignore client cmd doc grafana internal scripts shared test
