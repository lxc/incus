#!/bin/sh -eu
codespell -S 'internal/server/apparmor/*.profile.go' -I .codespell-ignore client cmd doc grafana internal scripts shared test
