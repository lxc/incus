---
relatedlinks: https://ubuntu.com/lxd, https://ubuntu.com/blog/open-source-for-beginners-dev-environment-with-lxd
---

# Incus

Incus (<a href="#" title="Listen" onclick="document.getElementById('player').play();return false;">`[lɛks'di:]`&#128264;</a>) is a modern, secure and powerful system container and virtual machine manager.

<audio id="player">  <source src="_static/lxd.mp3" type="audio/mpeg">  <source src="_static/lxd.ogg" type="audio/ogg">  <source src="_static/lxd.wav" type="audio/wav"></audio>

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start Incus intro -->
    :end-before: <!-- Include end Incus intro -->
```

## Security

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start security -->
    :end-before: <!-- Include end security -->
```

See [Security](security.md) for detailed information.

````{important}
% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
````

## Project and community

LXD is free software and developed under the [Apache 2 license](https://www.apache.org/licenses/LICENSE-2.0).
It’s an open source project that warmly welcomes community projects, contributions, suggestions, fixes and constructive feedback.

The LXD project is sponsored by [Canonical Ltd](https://www.canonical.com).

- [Code of Conduct](https://github.com/cyphar/incus/blob/main/CODE_OF_CONDUCT.md)
- [Contribute to the project](contributing.md)
- [Release announcements](https://discourse.ubuntu.com/c/lxd/news/)
- [Release tarballs](https://github.com/cyphar/incus/releases/)
- [Get support](support.md)
- [Watch tutorials and announcements on YouTube](https://www.youtube.com/c/LXDvideos)
- [Discuss on IRC](https://web.libera.chat/#lxd) (see [Getting started with IRC](https://discuss.linuxcontainers.org/t/getting-started-with-irc/11920) if needed)
- [Ask and answer questions on the forum](https://discourse.ubuntu.com/c/lxd/)

```{toctree}
:hidden:
:titlesonly:

self
getting_started
Server and client <operation>
security
instances
images
storage
networks
projects
clustering
production-setup
migration
restapi_landing
internals
external_resources
```
