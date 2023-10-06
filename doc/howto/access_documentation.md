(access-documentation)=
# How to access the local Incus documentation

The latest version of the Incus documentation is available at [`https://linuxcontainers.org/incus/docs/main/`](https://linuxcontainers.org/incus/docs/main/).

Alternatively, you can access a local version of the Incus documentation that is embedded in the Incus snap.
This version of the documentation exactly matches the version of your Incus deployment, but might be missing additions, fixes, or clarifications that were added after the release of the snap.

Complete the following steps to access the local Incus documentation:

1. Make sure that your Incus server is {ref}`exposed to the network <server-expose>`.
   You can expose the server during {ref}`initialization <initialize>`, or afterwards by setting the {config:option}`server-core:core.https_address` server configuration option.

1. Access the documentation in your browser by entering the server address followed by `/documentation/` (for example, `https://192.0.2.10:8443/documentation/`).

   If you have not set up a secure {ref}`authentication-server-certificate`, Incus uses a self-signed certificate, which will cause a security warning in your browser.
   Use your browser's mechanism to continue despite the security warning.
