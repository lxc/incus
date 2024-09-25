(instance-properties)=
# Instance properties

Instance properties are set when the instance is created.
They cannot be part of a {ref}`profile <profiles>`.

The following instance properties are available:

```{list-table}
   :header-rows: 1
   :widths: 2 1 4

* - Property
  - Read-only
  - Description
* - `name`
  - yes
  - Instance name (see {ref}`instance-name-requirements`)
* - `architecture`
  - no
  - Instance architecture
```

(instance-name-requirements)=
## Instance name requirements

The instance name can be changed only by renaming the instance with the [`incus rename`](incus_rename.md) command.

Valid instance names must fulfill the following requirements:

- The name must be between 1 and 63 characters long.
- The name must contain only letters, numbers and dashes from the ASCII table.
- The name must not start with a digit or a dash.
- The name must not end with a dash.

The purpose of these requirements is to ensure that the instance name can be used in DNS records, on the file system, in various security profiles and as the host name of the instance itself.
