#!/bin/sh -eu
codespell -S 'internal/server/apparmor/*.profile.go' -S 'doc/html/*' -S 'doc/reference/manpages/*' -I .codespell-ignore client cmd doc grafana internal scripts shared test
