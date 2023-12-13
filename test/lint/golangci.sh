#!/bin/sh -eu

# Default target branch.
target_branch="main"
if [ -n "${GITHUB_ACTIONS:-}" ]; then
  # Target branch when running in github actions (see https://docs.github.com/en/actions/learn-github-actions/variables#default-environment-variables).
  target_branch="${GITHUB_BASE_REF}"
elif [ -n "${1:-}" ]; then
  # Allow a target branch parameter.
  target_branch="${1}"
fi

# Gets the most recent commit hash from the target branch.
rev="$(git log "${target_branch}" --oneline --no-abbrev-commit -n1 | cut -d' ' -f1)"

echo "Checking for golangci-lint errors between HEAD and ${target_branch}..."
golangci-lint run --timeout 5m --new --new-from-rev "${rev}"