`mini-oidc` is an extremely basic OIDC provider which can be used with the `incus` command line.
It doesn't use web authentication and instead just automatically approves any authentication request.

By default, it will authenticate everyone as `unknown`, but this can be overriden by writing the username to be returned in the `user.data` file.
This effectively allows scripting a variety of users without having to deal with actual login.

The `storage` sub-package is a copy of https://github.com/zitadel/oidc/tree/main/example/server/storage with the exception of the added IncusDeviceClient.
