# mini-oidc

`mini-oidc` is an extremely basic OIDC provider which can be used with the `incus` command line.
It doesn't use web authentication and instead just automatically approves any authentication request.

Usage:

```shell
mini-oidc <port> [<user-file>]
```

By default, it will authenticate everyone as `unknown`, but this can be overridden by writing the username to be returned in the file named in the 2nd
(optional) argument. This effectively allows scripting a variety of users without having to deal with actual login.

The `storage` sub-package is a based on https://github.com/zitadel/oidc/tree/main/example/server/storage.
The following changes were made:

- Added IncusDeviceClient
- Added option to configure access token and refresh token expiry
