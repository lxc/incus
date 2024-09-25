(incus-alias)=
# How to manage command aliases

The Incus command-line client `incus` has support for adding aliases for commands that you use frequently.
You can use aliases as shortcuts for longer commands, or to automatically add flags to existing commands.

Managing aliases is done through the [`incus alias`](incus_alias.md) command.

Within the [`incus alias`](incus_alias.md) command, you can use the following subcommands:

- `incus alias add` to add a new command alias
- `incus alias list` to list all command aliases
- `incus alias remove` to remove a command alias
- `incus alias rename` to rename a command alias

Run [`incus alias --help`](incus_alias.md) to see all available subcommands and parameters.

```{note}
_Command aliases_ are different from {ref}`_image aliases_ <images>`.
An image alias is an alternative name for an image, usually a shorter name or another common mnemonic for that image.

Image aliases are a server-side concept part of the Incus API whereas command aliases are purely part of the command line tool configuration.
```

## How to add a command alias

To always ask for confirmation when deleting an instance, create an alias for
[`incus delete`](incus_delete.md) that always runs `incus delete --interactive`.

The following command for `incus alias`, will _add_ the command alias with name `delete`,
and will invoke the same Incus command but with the added `--interactive` flag.

    incus alias add delete "delete --interactive"

Note that when you now run `incus delete mycontainer` to delete an instance called `myinstance`,
the Incus command-line client will replace `incus delete` with `incus delete --interactive`
and will instead execute `incus delete --interactive myinstance`.

When a command alias has the same name as an Incus command, the command alias will mask the Incus command.

You would need to remove first the command alias if you want to run verbatim the Incus command of the same name.
In addition, when you use a command alias with parameters (in this case, the name of the container),
the Incus command-line client will place those parameters at the end of
the aliased command unless they are manually placed elsewhere through the `@ARGS@` string.

Finally, the command in the command alias should be enclosed in quotes.

## How to list all command aliases

To see all configured aliases, run [`incus alias list`](incus_alias_list.md).

## How to remove a command alias

To remove an existing command alias, type [`incus alias remove`](incus_alias_remove.md)
and add the name of that command alias.

## How to rename a command alias

To rename an existing command alias, type [`incus alias rename`](incus_alias_rename.md),
then add the name of that existing command alias, and finally the name of the new command alias.

## Built-in `shell` alias

Incus comes with the `shell` built-in command alias. That alias is based on the
[`incus exec`](incus_exec.md) command, executing `exec @ARGS@ -- su -l`.

```
$ incus alias list
+-----------+----------------------+
|   ALIAS   |        TARGET        |
+-----------+----------------------+
| shell     | exec @ARGS@ -- su -l |
+-----------+----------------------+
```

If you run `incus shell myinstance`, this command alias will expand into `incus exec myinstance -- su -l`.

The `--` construct is a command-line artifact that instructs the Incus command-line client to stop processing further parameters, like the `-l` that follows.
Without `--`, the expanded command `incus exec mycontainer su -l` would fail,
because the Incus command-line client would try to parse the `-l` flag. In this particular case, it would fail with an error because there is no `-l` parameter for `incus shell`.

The `su -l` command is synonymous to `su -` or `su --login`.
It launches a login shell in the instance as the `root` user.
The command reads the necessary configuration files to launch a login shell for user `root`.

The `shell` alias is built-in into the Incus server. Therefore, the Incus client is not able to remove it.
If you try to remove it, there will be an error that the alias does not exist.

```
$ incus alias remove shell
Error: Alias shell doesn't exist
$
```

If you add a new command alias with the name `shell`, the new command alias will be masking the built-in command alias.
That is, the Incus command-line client will be using your newly added alias instead and the built-in
command alias will be hidden. When you remove the newly added alias `shell`, the built-in alias will appear again.

## How to use a command alias to get a non-root shell in an instance

Several Incus images have been configured to create a non-root username as shown in the table below.

| Distribution          | Username         | Image |
| :----------- | :--------------: | :----------- |
| Alpine | `alpine` | `images:alpine/edge/cloud` |
| Debian | `debian` | `images:debian/12/cloud` |
| Fedora | `fedora` | `images:fedora/40/cloud` |
| Ubuntu | `ubuntu` | `images:ubuntu/24.04/cloud` |

You can get a shell into the instance for this non-root username with the following command.

```
$ incus launch images:debian/12/cloud mycontainer
Launching mycontainer
$ incus exec mycontainer -- su -l debian
debian@mycontainer:~$
```

By using the Incus command aliases, you can also create a command alias to get a shell into that instance.
In this command alias, you specify to `su -l` into the username `debian`.

```
$ incus alias add debian 'exec @ARGS@ -- su -l debian'
$
```

Finally, you can now get a shell into the instance with the following convenient command:

```
$ incus debian mycontainer
debian@mycontainer:~$
```

```{note}
As an alternative to `su`, you may use instead `sudo`. In that case, the command would be as follows.

     incus alias add debian `exec @ARGS@ -- sudo --login --user debian`
```

```{note}
When launching a system container or a virtual machine, Incus allows to specify environment variables.

     incus launch -c environment.MYVARIABLE=myvalue images:debian/12 myinstance

A login shell in such an instance does not have access to those environment variables. This is due to the semantics of login shells with either `su -l` or `sudo --login` which do not preserve any environment variables. If you want to preserve any environment variables, you would instead use either `su --preserve-environment` or `sudo --preserve-env`.

Another alternative is to add the environment variables into the instance to the file system file `/etc/environment`. By doing so, any new login shell to the instance will be able to parse this file and enable any environment variables.
```
