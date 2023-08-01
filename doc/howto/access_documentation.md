(access-documentation)=
# How to access the local LXD documentation

The latest version of the LXD documentation is available at [`documentation.ubuntu.com/lxd`](https://documentation.ubuntu.com/lxd/).

Alternatively, you can access a local version of the LXD documentation that is embedded in the LXD snap.
This version of the documentation exactly matches the version of your LXD deployment, but might be missing additions, fixes, or clarifications that were added after the release of the snap.

Complete the following steps to access the local LXD documentation:

1. Make sure that your LXD server is {ref}`exposed to the network <server-expose>`.
   You can expose the server during {ref}`initialization <initialize>`, or afterwards by setting the {config:option}`server-core:core.https_address` server configuration option.

1. Access the documentation in your browser by entering the server address followed by `/documentation/` (for example, `https://192.0.2.10:8443/documentation/`).

   If you have not set up a secure {ref}`authentication-server-certificate`, LXD uses a self-signed certificate, which will cause a security warning in your browser.
   Use your browser's mechanism to continue despite the security warning.
