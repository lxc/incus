(dev-incus)=
# Communication between instance and host

Communication between the hosted workload (instance) and its host while
not strictly needed is a pretty useful feature.

In Incus, this feature is implemented through a `/dev/incus/sock` node which is
created and set up for all Incus instances.

This file is a Unix socket which processes inside the instance can
connect to. It's multi-threaded so multiple clients can be connected at the
same time.

```{note}
{config:option}`instance-security:security.guestapi` must be set to `true` (which is the default) for an instance to allow access to the socket.
```

## Implementation details

Incus on the host binds `/var/lib/incus/guestapi/sock` and starts listening for new
connections on it.

This socket is then exposed into every single instance started by
Incus at `/dev/incus/sock`.

The single socket is required so we can exceed 4096 instances, otherwise,
Incus would have to bind a different socket for every instance, quickly
reaching the FD limit.

## Authentication

Queries on `/dev/incus/sock` will only return information related to the
requesting instance. To figure out where a request comes from, Incus will
extract the initial socket's user credentials and compare that to the list of
instances it manages.

## Protocol

The protocol on `/dev/incus/sock` is plain-text HTTP with JSON messaging, so very
similar to the local version of the Incus protocol.

Unlike the main Incus API, there is no background operation and no
authentication support in the `/dev/incus/sock` API.

## REST-API

### API structure

* `/`
   * `/1.0`
      * `/1.0/config`
         * `/1.0/config/{key}`
      * `/1.0/devices`
      * `/1.0/events`
      * `/1.0/images/{fingerprint}/export`
      * `/1.0/meta-data`

### API details

#### `/`

##### GET

* Description: List of supported APIs
* Return: list of supported API endpoint URLs (by default `['/1.0']`)

Return value:

```json
[
    "/1.0"
]
```

#### `/1.0`

##### GET

* Description: Information about the 1.0 API
* Return: JSON object

Return value:

```json
{
    "api_version": "1.0",
    "location": "foo.example.com",
    "instance_type": "container",
    "state": "Started",
}
```

#### PATCH

* Description: Update instance state (valid states are `Ready` and `Started`)
* Return: none

 Input:

 ```json
 {
    "state": "Ready"
 }
```

#### `/1.0/config`

##### GET

* Description: List of configuration keys
* Return: list of configuration keys URL

Note that the configuration key names match those in the instance
configuration, however not all configuration namespaces will be exported to
`/dev/incus/sock`.
Currently only the `cloud-init.*` and `user.*` keys are accessible to the instance.

At this time, there also aren't any instance-writable namespace.

Return value:

```json
[
    "/1.0/config/user.a"
]
```

#### `/1.0/config/<KEY>`

##### GET

* Description: Value of that key
* Return: Plain-text value

Return value:

    blah

#### `/1.0/devices`

##### GET

* Description: Map of instance devices
* Return: JSON object

Return value:

```json
{
    "eth0": {
        "name": "eth0",
        "network": "incusbr0",
        "type": "nic"
    },
    "root": {
        "path": "/",
        "pool": "default",
        "type": "disk"
    }
}
```

#### `/1.0/events`

##### GET

* Description: WebSocket upgrade
* Return: none (never ending flow of events)

Supported arguments are:

* type: comma-separated list of notifications to subscribe to (defaults to all)

The notification types are:

* `config` (changes to any of the `user.*` configuration keys)
* `device` (any device addition, change or removal)

This never returns. Each notification is sent as a separate JSON object:

```json
{
    "timestamp": "2017-12-21T18:28:26.846603815-05:00",
    "type": "device",
    "metadata": {
        "name": "kvm",
        "action": "added",
        "config": {
            "type": "unix-char",
            "path": "/dev/kvm"
        }
    }
}
```

```json
{
    "timestamp": "2017-12-21T18:28:26.846603815-05:00",
    "type": "config",
    "metadata": {
        "key": "user.foo",
        "old_value": "",
        "value": "bar"
    }
}
```

#### `/1.0/images/<FINGERPRINT>/export`

##### GET

* Description: Download a public/cached image from the host
* Return: raw image or error
* Access: Requires `security.guestapi.images` set to `true`

Return value:

    See /1.0/images/<FINGERPRINT>/export in the daemon API.

#### `/1.0/meta-data`

##### GET

* Description: Container meta-data compatible with cloud-init
* Return: cloud-init meta-data

Return value:

    #cloud-config
    instance-id: af6a01c7-f847-4688-a2a4-37fddd744625
    local-hostname: abc
