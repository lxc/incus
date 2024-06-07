#!/bin/sh -eu

target_branch=""
if [ -n "${GITHUB_BASE_REF:-}" ]; then
  # Attempt to fetch the reference (Github uses a shallow clone).
  git fetch -q origin "${GITHUB_BASE_REF}" || true

  # Target branch when scanning a Github pull request
  target_branch="origin/${GITHUB_BASE_REF}"
elif [ -n "${GITHUB_BEFORE:-}" ]; then
  # Target branch when scanning a Github merge
  target_branch="${GITHUB_BEFORE}"

  # Attempt to fetch the reference (Github uses a shallow clone).
  git fetch -q origin "${GITHUB_BEFORE}" || true
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
