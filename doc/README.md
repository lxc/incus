# Incus documentation

The Incus documentation is available at: <https://linuxcontainers.org/incus/docs/main/>

GitHub provides a basic rendering of the documentation as well, but important features like includes and clickable links are missing. Therefore, we recommend reading the [published documentation](https://linuxcontainers.org/incus/docs/main/).

## How it works

<!-- Include start docs -->

### Documentation framework

Incus' documentation is built with [Sphinx](https://www.sphinx-doc.org/en/master/index.html).

It is written in [Markdown](https://commonmark.org/) with [MyST](https://myst-parser.readthedocs.io/) extensions.
For syntax help and guidelines, see the [documentation cheat sheet](https://linuxcontainers.org/incus/docs/main/doc-cheat-sheet/) ([source](https://raw.githubusercontent.com/lxc/incus/main/doc/doc-cheat-sheet.md)).

For structuring, the documentation uses the [Diátaxis](https://diataxis.fr/) approach.

### Build the documentation

To build the documentation, run `make doc` from the root directory of the repository.
This command installs the required tools and renders the output to the `doc/html/` directory.
To update the documentation for changed files only (without re-installing the tools), run `make doc-incremental`.

Before opening a pull request, make sure that the documentation builds without any warnings (warnings are treated as errors).
To preview the documentation locally, run `make doc-serve` and go to [`http://localhost:8001`](http://localhost:8001) to view the rendered documentation.
