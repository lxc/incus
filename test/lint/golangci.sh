#!/bin/sh -eu

target_branch=""
if [ -n "${GITHUB_ACTIONS:-}" ]; then
  # Target branch when running in github actions (see https://docs.github.com/en/actions/learn-github-actions/variables#default-environment-variables).
  target_branch="${GITHUB_BASE_REF}"
elif [ -n "${1:-}" ]; then
  # Allow a target branch parameter.
  target_branch="${1}"
else
  # Default target branch.
  for branch in main origin lxc/main; do
    if git show-ref --quiet "refs/heads/${branch}" >/dev/null 2>&1; then
        target_branch="${branch}"
        break
    fi
  done
fi

# Check if we found a target branch.
if [ -z "${target_branch}" ]; then
  echo "The target branch for golangci couldn't be found, skipping."
  return
fi

# Gets the most recent commit hash from the target branch.
rev="$(git log "${target_branch}" --oneline --no-abbrev-commit -n1 | cut -d' ' -f1)"

echo "Checking for golangci-lint errors between HEAD and ${target_branch}..."
golangci-lint run --timeout 5m --new --new-from-rev "${rev}"
