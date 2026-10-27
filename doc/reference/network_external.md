(network-external)=
# External networks

<!-- Include start external intro -->
External networks use network interfaces that already exist.
Therefore, Incus has limited possibility to control them, and Incus features like network ACLs, network forwards and network zones are not supported.

The main purpose for using external networks is to provide an uplink network through a parent interface.
This external network specifies the presets to use when connecting instances or other networks to a parent interface.

Incus supports the following external network types:
<!-- Include end external intro -->

```{toctree}
:maxdepth: 1
/reference/network_macvlan
/reference/network_sriov
/reference/network_physical
```
