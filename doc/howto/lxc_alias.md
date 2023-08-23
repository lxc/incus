(lxc-alias)=
# How to add command aliases

The LXD command-line client supports adding aliases for commands that you use frequently.
You can use aliases as shortcuts for longer commands, or to automatically add flags to existing commands.

To manage command aliases, you use the [`lxc alias`](lxc_alias.md) command.

For example, to always ask for confirmation when deleting an instance, create an alias for `lxc delete` that always runs `lxc delete -i`:

    lxc alias add delete "delete -i"

To see all configured aliases, run [`lxc alias list`](lxc_alias_list.md).
Run [`lxc alias --help`](lxc_alias.md) to see all available subcommands.
