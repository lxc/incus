# Contributing

<!-- Include start contributing -->

The Incus team appreciates contributions to the project, through pull requests, issues on the [GitHub repository](https://github.com/lxc/incus/issues), or discussions or questions on the [forum](https://discuss.linuxcontainers.org).

Check the following guidelines before contributing to the project.

## Code of Conduct

When contributing, you must adhere to the Code of Conduct, which is available at: [`https://github.com/lxc/incus/blob/main/CODE_OF_CONDUCT.md`](https://github.com/lxc/incus/blob/main/CODE_OF_CONDUCT.md)

## License and copyright

By default, any contribution to this project is made under the Apache
2.0 license.

The author of a change remains the copyright holder of their code
(no copyright assignment).

## Policy on the use of Large Language Models (LLMs) and AI tooling
### For issue reporting

We do NOT allow direct filing of issues by LLMs.

We REQUIRE a human being to go through our issue reporting form on
Github and accurately describe their issue and provide all needed
information.

The more concise and to the point the issue is, the more likely it is to
be understood, tracked down and resolved quickly.

Long winded AI written essays can easily look overwhelming and cause our
maintainers and other contributors to just entirely skip the issue to
focus their energy on something else.

We also don't benefit from AI generated root cause analysis or proposed
fixes in those issues. If you yourself understand the code base well
enough to go through that content and suggested fix, then turn it into a
pull request and submit it yourself. Otherwise, please limit your report
to describing the issue at hand and we'll take it from there.

### For contributions

We REQUIRE all contributions to Incus to be submitted by human beings who
can assert full copyright ownership of their contribution or have been
allowed by their employer to contribute. This is what the DCO (see below)
requires of all contributors.

AI tools can sometimes be beneficial, particularly when it comes to
finding patterns among a large data set (entire code base), performing
tedious repetitive changes or large refactoring/re-organization.

While we now tolerate the use of such tools, they must abide by our
instructions (`AGENTS.md`) and their operators cannot override those
instructions.

We expect everyone contributing to Incus to fully own their
contribution, be able to reason about it, be able to explain why things
were done a particular way and act as the full owner of that code. AI
tools are treated the same as traditional tooling like `sed`, `awk` or
`coccinelle`.

For the purpose of this project, AI tools CANNOT be treated as author,
co-author or be credited in any way that would suggest any ownership
over the contribution.

The contributor should have done all the thinking, planning and
understanding of the changes needed to resolve an issue or implement a
new feature prior to using automated tooling to perform the grunt work.

Unguided use of those tools or the inability to prove understanding of
the code contributed will result in a loss of trust in that contributor
by project maintainers which can then lead to exclusion from any further
contribution to the project.

It's also worth pointing out that while those tools are good at
implementing the more boring/repetitive/grunt work. We've generally
found that you only really understand the project and its structure by
having done such work yourself a few times.

### For anyone with write access to the repository

Anyone with write access to this repository must ensure to NEVER run an
AI agent or similar tool on a system which holds repository credentials
(SSH key, GPG key, web browser cookies, ...).

Any use of AI tooling should be done inside of a clean VM/container that
itself cannot directly push to or alter this repository in any way.

The safest approach is to SSH into that environment and then extract the
changes using `git format-patch`, then review and apply them to your
actual tree, tweak them as needed, sign them off and then push and open
the pull request.

Any potential credential compromise or loss of control should be
immediately reported to `security@linuxcontainers.org`.

## Pull requests

Changes to this project should be proposed as pull requests on GitHub
at: [`https://github.com/lxc/incus`](https://github.com/lxc/incus)

Proposed changes will then go through review there and once approved,
be merged in the main branch.

### Commit structure

Separate commits should be used for:

- API extension (`api: Add XYZ extension`, contains `doc/api-extensions.md` and `internal/version/api.go`)
- Documentation (`doc: Update XYZ` for files in `doc/`)
- API structure (`shared/api: Add XYZ` for changes to `shared/api/`)
- Go client package (`client: Add XYZ` for changes to `client/`)
- CLI (`cmd/<command>: Change XYZ` for changes to `cmd/`)
- Incus daemon (`incus/<package>: Add support for XYZ` for changes to `incus/`)
- Tests (`tests: Add test for XYZ` for changes to `tests/`)

The same kind of pattern extends to the other tools in the Incus code tree
and depending on complexity, things may be split into even smaller chunks.

When updating strings in the CLI tool (`cmd/`), you may need a commit to update the templates:

    make i18n
    git commit -a -s -m "i18n: Update translation templates" po/

When updating API (`shared/api`), you may need a commit to update the swagger YAML:

    make update-api
    git commit -s -m "doc/rest-api: Refresh swagger YAML" doc/rest-api.yaml

This structure makes it easier for contributions to be reviewed and also
greatly simplifies the process of back-porting fixes to stable branches.

### Developer Certificate of Origin

To improve tracking of contributions to this project we use the DCO 1.1
and use a "sign-off" procedure for all changes going into the branch.

The sign-off is a simple line at the end of the explanation for the
commit which certifies that you wrote it or otherwise have the right
to pass it on as an open-source contribution.

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.
660 York Street, Suite 102,
San Francisco, CA 94110 USA

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

An example of a valid sign-off line is:

```
Signed-off-by: Random J Developer <random@developer.org>
```

Use a known identity and a valid e-mail address.
Sorry, no anonymous contributions are allowed.

We also require each commit be individually signed-off by their author,
even when part of a larger set. You may find `git commit -s` useful.

<!-- Include end contributing -->

## More information

For more information, see [Contributing](https://linuxcontainers.org/incus/docs/main/contributing/) in the documentation.
