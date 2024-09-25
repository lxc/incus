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
