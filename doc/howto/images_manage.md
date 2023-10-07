(images-manage)=
# How to manage images

When working with images, you can inspect various information about the available images, view and edit their properties and configure aliases to refer to specific images.
You can also export an image to a file, which can be useful to {ref}`copy or import it <images-copy>` on another machine.

## List available images

To list all images on a server, enter the following command:

    incus image list [<remote>:]

If you do not specify a remote, the {ref}`default remote <images-remote-default>` is used.

(images-manage-filter)=
### Filter available images

To filter the results that are displayed, specify a part of the alias or fingerprint after the command.
For example, to show all Ubuntu 22.04 images, enter the following command:

    incus image list images: 22.04

You can specify several filters as well.
For example, to show all Arm 64-bit Ubuntu 22.04 images, enter the following command:

    incus image list images: 22.04 arm64

To filter for properties other than alias or fingerprint, specify the filter in `<key>=<value>` format.
For example:

    incus image list images: 22.04 architecture=x86_64

## View image information

To view information about an image, enter the following command:

    incus image info <image_ID>

As the image ID, you can specify either the image's alias or its fingerprint.
For a remote image, remember to include the remote server (for example, `images:ubuntu/22.04`).

To display only the image properties, enter the following command:

    incus image show <image_ID>

You can also display a specific image property (located under the `properties` key) with the following command:

    incus image get-property <image_ID> <key>

For example, to show the release name of the official Ubuntu 22.04 image, enter the following command:

    incus image get-property images:ubuntu/22.04 release

(images-manage-edit)=
## Edit image properties

To set a specific image property that is located under the `properties` key, enter the following command:

    incus image set-property <image_ID> <key>

```{note}
These properties can be used to convey information about the image.
They do not configure Incus' behavior in any way.
```

To edit the full image properties, including the top-level properties, enter the following command:

    incus image edit <image_ID>

## Configure image aliases

Configuring an alias for an image can be useful to make it easier to refer to an image, since remembering an alias is usually easier than remembering a fingerprint.
Most importantly, however, you can change an alias to point to a different image, which allows creating an alias that always provides a current image (for example, the latest version of a release).

You can see some of the existing aliases in the image list.
To see the full list, enter the following command:

    incus image alias list

You can directly assign an alias to an image when you {ref}`copy or import <images-copy>` or {ref}`publish <images-create-publish>` it.
Alternatively, enter the following command:

    incus image alias create <alias_name> <image_fingerprint>

You can also delete an alias:

    incus image alias delete <alias_name>

To rename an alias, enter the following command:

    incus image alias rename <alias_name> <new_alias_name>

If you want to keep the alias name, but point the alias to a different image (for example, a newer version), you must delete the existing alias and then create a new one.

(images-manage-export)=
## Export an image to a file

Images are located in the image store of your local server or a remote Incus server.
You can export them to a file though.
This method can be useful to back up image files or to transfer them to an air-gapped environment.

To export a container image to a file, enter the following command:

    incus image export [<remote>:]<image> [<output_directory_path>]

To export a virtual machine image to a file, add the `--vm` flag:

    incus image export [<remote>:]<image> [<output_directory_path>] --vm

See {ref}`image-format` for a description of the file structure used for the image.
