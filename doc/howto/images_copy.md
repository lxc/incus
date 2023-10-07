(images-copy)=
# How to copy and import images

To add images to an image store, you can either copy them from another server or import them from files (either local files or files on a web server).

## Copy an image from a remote

To copy an image from one server to another, enter the following command:

    incus image copy [<source_remote>:]<image> <target_remote>:

```{note}
To copy the image to your local image store, specify `local:` as the target remote.
```

See [`incus image copy --help`](incus_image_copy.md) for a list of all available flags.
The most relevant ones are:

`--alias`
: Assign an alias to the copy of the image.

`--copy-aliases`
: Copy the aliases that the source image has.

`--auto-update`
: Keep the copy up-to-date with the original image.

`--vm`
: When copying from an alias, copy the image that can be used to create virtual machines.

## Import an image from files

If you have image files that use the required {ref}`image-format`, you can import them into your image store.

There are several ways of obtaining such image files:

- Exporting an existing image (see {ref}`images-manage-export`)
- Building your own image using `distrobuilder` (see {ref}`images-create-build`)
- Downloading image files from a {ref}`remote image server <remote-image-servers>` (note that it is usually easier to {ref}`use the remote image <images-remote>` directly instead of downloading it to a file and importing it)

### Import from the local file system

To import an image from the local file system, use the [`incus image import`](incus_image_import.md) command.
This command supports both {ref}`unified images <image-format-unified>` (compressed file or directory) and {ref}`split images <image-format-split>` (two files).

To import a unified image from one file or directory, enter the following command:

    incus image import <image_file_or_directory_path> [<target_remote>:]

To import a split image, enter the following command:

    incus image import <metadata_tarball_path> <rootfs_tarball_path> [<target_remote>:]

In both cases, you can assign an alias with the `--alias` flag.
See [`incus image import --help`](incus_image_import.md) for all available flags.

### Import from a file on a remote web server

You can import image files from a remote web server by URL.
This method is an alternative to running a Incus server for the sole purpose of distributing an image to users.
It only requires a basic web server with support for custom headers (see {ref}`images-copy-http-headers`).

The image files must be provided as unified images (see {ref}`image-format-unified`).

To import an image file from a remote web server, enter the following command:

    incus image import <URL>

You can assign an alias to the local image with the `--alias` flag.

(images-copy-http-headers)=
#### Custom HTTP headers

Incus requires the following custom HTTP headers to be set by the web server:

`Incus-Image-Hash`
: The SHA256 of the image that is being downloaded.

`Incus-Image-URL`
: The URL from which to download the image.

Incus sets the following headers when querying the server:

`Incus-Server-Architectures`
: A comma-separated list of architectures that the client supports.

`Incus-Server-Version`
: The version of Incus in use.
