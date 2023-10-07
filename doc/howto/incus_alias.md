(incus-alias)=
# How to add command aliases

The Incus command-line client supports adding aliases for commands that you use frequently.
You can use aliases as shortcuts for longer commands, or to automatically add flags to existing commands.

To manage command aliases, you use the [`incus alias`](incus_alias.md) command.

For example, to always ask for confirmation when deleting an instance, create an alias for `incus delete` that always runs `incus delete -i`:

    incus alias add delete "delete -i"

To see all configured aliases, run [`incus alias list`](incus_alias_list.md).
Run [`incus alias --help`](incus_alias.md) to see all available subcommands.
