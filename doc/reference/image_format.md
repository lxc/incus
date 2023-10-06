(image-format)=
# Image format

Images contain a root file system and a metadata file that describes the image.
They can also contain templates for creating files inside an instance that uses the image.

Images can be packaged as either a unified image (single file) or a split image (two files).

## Content

Images for containers have the following directory structure:

```
metadata.yaml
rootfs/
templates/
```

Images for VMs have the following directory structure:

```
metadata.yaml
rootfs.img
templates/
```

For both instance types, the `templates/` directory is optional.

### Metadata

The `metadata.yaml` file contains information that is relevant to running the image in Incus.
It includes the following information:

```yaml
architecture: x86_64
creation_date: 1424284563
properties:
  description: Ubuntu 22.04 LTS Intel 64bit
  os: Ubuntu
  release: jammy 22.04
templates:
  ...
```

The `architecture` and `creation_date` fields are mandatory.
The `properties` field contains a set of default properties for the image.
The `os`, `release`, `name` and `description` fields are commonly used, but are not mandatory.

The `templates` field is optional.
See {ref}`image_format_templates` for information on how to configure templates.

### Root file system

For containers, the `rootfs/` directory contains a full file system tree of the root directory (`/`) in the container.

Virtual machines use a `rootfs.img` `qcow2` file instead of a `rootfs/` directory.
This file becomes the main disk device.

(image_format_templates)=
### Templates (optional)

You can use templates to dynamically create files inside an instance.
To do so, configure template rules in the `metadata.yaml` file and place the template files in a `templates/` directory.

As a general rule, you should never template a file that is owned by a package or is otherwise expected to be overwritten by normal operation of an instance.

#### Template rules

For each file that should be generated, create a rule in the `metadata.yaml` file.
For example:

```yaml
templates:
  /etc/hosts:
    when:
      - create
      - rename
    template: hosts.tpl
    properties:
      foo: bar
  /etc/hostname:
    when:
      - start
    template: hostname.tpl
  /etc/network/interfaces:
    when:
      - create
    template: interfaces.tpl
    create_only: true
```

The `when` key can be one or more of:

- `create` - run at the time a new instance is created from the image
- `copy` - run when an instance is created from an existing one
- `start` - run every time the instance is started

The `template` key points to the template file in the `templates/` directory.

You can pass user-defined template properties to the template file through the `properties` key.

Set the `create_only` key if you want Incus to create the file if it doesn't exist, but not overwrite an existing file.

#### Template files

Template files use the [Pongo2](https://www.schlachter.tech/solutions/pongo2-template-engine/) format.

They always receive the following context:

| Variable     | Type                           | Description                                                                         |
|--------------|--------------------------------|-------------------------------------------------------------------------------------|
| `trigger`    | `string`                       | Name of the event that triggered the template                                       |
| `path`       | `string`                       | Path of the file that uses the template                                             |
| `instance`   | `map[string]string`            | Key/value map of instance properties (name, architecture, privileged and ephemeral) |
| `config`     | `map[string]string`            | Key/value map of the instance's configuration                                       |
| `devices`    | `map[string]map[string]string` | Key/value map of the devices assigned to the instance                               |
| `properties` | `map[string]string`            | Key/value map of the template properties specified in `metadata.yaml`               |

For convenience, the following functions are exported to the Pongo2 templates:

- `config_get("user.foo", "bar")` - Returns the value of `user.foo`, or `"bar"` if not set.

## Image tarballs

Incus supports two Incus-specific image formats: a unified tarball and split tarballs.

These tarballs can be compressed.
Incus supports a wide variety of compression algorithms for tarballs.
However, for compatibility purposes, you should use `gzip` or `xz`.

(image-format-unified)=
### Unified tarball

A unified tarball is a single tarball (usually `*.tar.xz`) that contains the full content of the image, including the metadata, the root file system and optionally the template files.

This is the format that Incus itself uses internally when publishing images.
It is usually easier to work with; therefore, you should use the unified format when creating Incus-specific images.

The image identifier for such images is the SHA-256 of the tarball.

(image-format-split)=
### Split tarballs

A split image consists of two separate tarballs.
One tarball contains the metadata and optionally the template files (usually `*.tar.xz`), and the other contains the root file system (usually `*.squashfs` for containers or `*.qcow2` for virtual machines).

For containers, the root file system tarball can be SquashFS-formatted.
For virtual machines, the `rootfs.img` file always uses the `qcow2` format.
It can optionally be compressed using `qcow2`'s native compression.

This format is designed to allow for easy image building from existing non-Incus rootfs tarballs that are already available.
You should also use this format if you want to create images that can be consumed by both Incus and other tools.

The image identifier for such images is the SHA-256 of the concatenation of the metadata and root file system tarball (in that order).
