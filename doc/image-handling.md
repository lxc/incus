(about-images)=
# About images

Incus uses an image-based workflow.
Each instance is based on an image, which contains a basic operating system (for example, a Linux distribution) and some Incus-related information.

Images are available from remote image stores (see {ref}`remote-image-servers` for an overview), but you can also create your own images, either based on an existing instances or a rootfs image.

You can copy images from remote servers to your local image store, or copy local images to remote servers.
You can also use a local image to create a remote instance.

Each image is identified by a fingerprint (SHA256).
To make it easier to manage images, Incus allows defining one or more aliases for each image.

## Caching

When you create an instance using a remote image, Incus downloads the image and caches it locally.
It is stored in the local image store with the cached flag set.
The image is kept locally as a private image until either:

- The image has not been used to create a new instance for the number of days set in {config:option}`server-images:images.remote_cache_expiry`.
- The image's expiry date (one of the image properties; see {ref}`images-manage-edit` for information on how to change it) is reached.

Incus keeps track of the image usage by updating the `last_used_at` image property every time a new instance is spawned from the image.

## Auto-update

Incus can automatically keep images that come from a remote server up to date.

```{note}
Only images that are requested through an alias can be updated.
If you request an image through a fingerprint, you request an exact image version.
```

Whether auto-update is enabled for an image depends on how the image was downloaded:

- If the image was downloaded and cached when creating an instance, it is automatically updated if {config:option}`server-images:images.auto_update_cached` was set to `true` (the default) at download time.
- If the image was copied from a remote server using the [`incus image copy`](incus_image_copy.md) command, it is automatically updated only if the `--auto-update` flag was specified.

You can change this behavior for an image by [editing the `auto_update` property](images-manage-edit).

On startup and after every {config:option}`server-images:images.auto_update_interval` (by default, every six hours), the Incus daemon checks for more recent versions of all the images in the store that are marked to be auto-updated and have a recorded source server.

When a new version of an image is found, it is downloaded into the image store.
Then any aliases pointing to the old image are moved to the new one, and the old image is removed from the store.

To not delay instance creation, Incus does not check if a new version is available when creating an instance from a cached image.
This means that the instance might use an older version of an image for the new instance until the image is updated at the next update interval.

## Special image properties

Image properties that begin with the prefix `requirements` (for example, `requirements.XYZ`) are used by Incus to determine the compatibility of the host system and the instance that is created based on the image.
If these are incompatible, Incus does not start the instance.

The following requirements are supported:

Key                                         | Type      | Default      | Description
:--                                         | :---      | :------      | :----------
`requirements.secureboot`                   | string    | -            | If set to `false`, indicates that the image cannot boot under secure boot.
`requirements.cgroup`                       | string    | -            | If set to `v1`, indicates that the image requires the host to run cgroup v1.
