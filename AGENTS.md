# Legal

 - All contributions to this repository must be compatible with the Apache 2.0 license.
 - Specifically (but not limited to), contributions cannot include code licensed under the terms of the GPL, AGPL or LGPL licenses.
 - Only human beings are allowed to sign the Developer Certificate of Ownership (DCO / Signed-off-by).
 - Only human beings can ever be credited within commit messages.

# Formatting

 - Code comments should be no longer than one line, unless they are required to cover complex unintuitive logic.
 - Commit messages should similarly be kept as short and to the point as possible, no need to summarize the whole issue.
 - We don't use the define and test one line `if` syntax, instead splitting defintion and testing across two lines.

# Testing / validation

 - The commit structure described in `CONTRIBUTING.md` should generally be followed.
 - All branches are expected to pass `make static-analysis` and `go test -v ./...`.
 - Excessive unit tests are generally discouraged.
 - When possible, existing system tests should be extended to cover new features.
 - A full local system test run isn't required prior to contribution, all tests get run in our CI.
