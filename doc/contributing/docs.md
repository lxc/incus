# Contribute to the documentation

We want Incus to be as easy and straight-forward to use as possible.
Therefore, we aim to provide documentation that contains the information that users need to work with Incus, that covers all common use cases, and that answers typical questions.

You can contribute to the documentation in various different ways.
We appreciate your contributions!

Typical ways to contribute are:

- Add or update documentation for new features or feature improvements that you contribute to the code.
  We'll review the documentation update and merge it together with your code.
- Add or update documentation that clarifies any doubts you had when working with the product.
  Such contributions can be done through a pull request or through a post in the [Tutorials](https://discuss.linuxcontainers.org/c/tutorials/16) section on the forum.
  New tutorials will be considered for inclusion in the docs (through a link or by including the actual content).
- To request a fix to the documentation, open a documentation issue on [GitHub](https://github.com/lxc/incus/issues).
  We'll evaluate the issue and update the documentation accordingly.
- Post a question or a suggestion on the [forum](https://discuss.linuxcontainers.org).
  We'll monitor the posts and, if needed, update the documentation accordingly.
- Ask questions or provide suggestions in the `#lxc` channel on [IRC](https://web.libera.chat/#lxc).
  Given the dynamic nature of IRC, we cannot guarantee answers or reactions to IRC posts, but we monitor the channel and try to improve our documentation based on the received feedback.

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start docs -->
```

When you open a pull request, a preview of the documentation output is built automatically.

## Automatic documentation checks

GitHub runs automatic checks on the documentation to verify the spelling, the validity of links, correct formatting of the Markdown files, and the use of inclusive language.

You can (and should!) run these tests locally as well with the following commands:

- Check the spelling: `make doc-spellcheck`
- Check the validity of links: `make doc-linkcheck`
- Check the Markdown formatting: `make doc-lint`
- Check for inclusive language: `make doc-woke`

To run the above, you will need the following:

- Python 3.8 or higher
- The `venv` python package
- The `aspell` tool for spellchecking
- The `mdl` markdown lint tool

## Document configuration options

```{note}
We are currently in the process of moving the documentation of configuration options to code comments.
At the moment, not all configuration options follow this approach.
```

The documentation of configuration options is extracted from comments in the Go code.
Look for comments that start with `gendoc:generate` in the code.

When you add or change a configuration option, make sure to include the required documentation comment for it.

Then run `make generate-config` to re-generate the `doc/config_options.txt` file.
The updated file should be checked in.

The documentation includes sections from the `doc/config_options.txt` to display a group of configuration options.
For example, to include the core server options:

````
% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-core start -->
    :end-before: <!-- config group server-core end -->
```
````

If you add a configuration option to an existing group, you don't need to do any updates to the documentation files.
The new option will automatically be picked up.
You only need to add an include to a documentation file if you are defining a new group.
