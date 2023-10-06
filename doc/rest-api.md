# REST API

All communication between Incus and its clients happens using a RESTful API over HTTP.
This API is encapsulated over either TLS (for remote operations) or a Unix socket (for local operations).

See {ref}`authentication` for information about how to access the API remotely.

```{tip}
- For examples on how the API is used, run any command of the Incus client ([`incus`](incus.md)) with the `--debug` flag.
The debug information displays the API calls and the return values.
- For quickly querying the API, the Incus client provides a [`incus query`](incus_query.md) command.
```

## API versioning

The list of supported major API versions can be retrieved using `GET /`.

The reason for a major API bump is if the API breaks backward compatibility.

Feature additions done without breaking backward compatibility only
result in addition to `api_extensions` which can be used by the client
to check if a given feature is supported by the server.

## Return values

There are three standard return types:

* Standard return value
* Background operation
* Error

### Standard return value

For a standard synchronous operation, the following JSON object is returned:

```js
{
    "type": "sync",
    "status": "Success",
    "status_code": 200,
    "metadata": {}                          // Extra resource/action specific metadata
}
```

HTTP code must be 200.

### Background operation

When a request results in a background operation, the HTTP code is set to 202 (Accepted)
and the Location HTTP header is set to the operation URL.

The body is a JSON object with the following structure:

```js
{
    "type": "async",
    "status": "OK",
    "status_code": 100,
    "operation": "/1.0/instances/<id>",                     // URL to the background operation
    "metadata": {}                                          // Operation metadata (see below)
}
```

The operation metadata structure looks like:

```js
{
    "id": "a40f5541-5e98-454f-b3b6-8a51ef5dbd3c",           // UUID of the operation
    "class": "websocket",                                   // Class of the operation (task, websocket or token)
    "created_at": "2015-11-17T22:32:02.226176091-05:00",    // When the operation was created
    "updated_at": "2015-11-17T22:32:02.226176091-05:00",    // Last time the operation was updated
    "status": "Running",                                    // String version of the operation's status
    "status_code": 103,                                     // Integer version of the operation's status (use this rather than status)
    "resources": {                                          // Dictionary of resource types (container, snapshots, images) and affected resources
      "containers": [
        "/1.0/instances/test"
      ]
    },
    "metadata": {                                           // Metadata specific to the operation in question (in this case, exec)
      "fds": {
        "0": "2a4a97af81529f6608dca31f03a7b7e47acc0b8dc6514496eb25e325f9e4fa6a",
        "control": "5b64c661ef313b423b5317ba9cb6410e40b705806c28255f601c0ef603f079a7"
      }
    },
    "may_cancel": false,                                    // Whether the operation can be canceled (DELETE over REST)
    "err": ""                                               // The error string should the operation have failed
}
```

The body is mostly provided as a user friendly way of seeing what's
going on without having to pull the target operation, all information in
the body can also be retrieved from the background operation URL.

### Error

There are various situations in which something may immediately go
wrong, in those cases, the following return value is used:

```js
{
    "type": "error",
    "error": "Failure",
    "error_code": 400,
    "metadata": {}                      // More details about the error
}
```

HTTP code must be one of of 400, 401, 403, 404, 409, 412 or 500.

## Status codes

The Incus REST API often has to return status information, be that the
reason for an error, the current state of an operation or the state of
the various resources it exports.

To make it simple to debug, all of those are always doubled. There is a
numeric representation of the state which is guaranteed never to change
and can be relied on by API clients. Then there is a text version meant
to make it easier for people manually using the API to figure out what's
happening.

In most cases, those will be called status and `status_code`, the former
being the user-friendly string representation and the latter the fixed
numeric value.

The codes are always 3 digits, with the following ranges:

* 100 to 199: resource state (started, stopped, ready, ...)
* 200 to 399: positive action result
* 400 to 599: negative action result
* 600 to 999: future use

### List of current status codes

Code  | Meaning
:---  | :------
100   | Operation created
101   | Started
102   | Stopped
103   | Running
104   | Canceling
105   | Pending
106   | Starting
107   | Stopping
108   | Aborting
109   | Freezing
110   | Frozen
111   | Thawed
112   | Error
113   | Ready
200   | Success
400   | Failure
401   | Canceled

(rest-api-recursion)=
## Recursion

To optimize queries of large lists, recursion is implemented for collections.
A `recursion` argument can be passed to a GET query against a collection.

The default value is 0 which means that collection member URLs are
returned. Setting it to 1 will have those URLs be replaced by the object
they point to (typically another JSON object).

Recursion is implemented by simply replacing any pointer to an job (URL)
by the object itself.

(rest-api-filtering)=
## Filtering

To filter your results on certain values, filter is implemented for collections.
A `filter` argument can be passed to a GET query against a collection.

Filtering is available for the instance, image and storage volume endpoints.

There is no default value for filter which means that all results found will
be returned. The following is the language used for the filter argument:

    ?filter=field_name eq desired_field_assignment

The language follows the OData conventions for structuring REST API filtering
logic. Logical operators are also supported for filtering: not (`not`), equals (`eq`),
not equals (`ne`), and (`and`), or (`or`). Filters are evaluated with left associativity.
Values with spaces can be surrounded with quotes. Nesting filtering is also supported.
For instance, to filter on a field in a configuration you would pass:

    ?filter=config.field_name eq desired_field_assignment

For filtering on device attributes you would pass:

    ?filter=devices.device_name.field_name eq desired_field_assignment

Here are a few GET query examples of the different filtering methods mentioned above:

    containers?filter=name eq "my container" and status eq Running

    containers?filter=config.image.os eq ubuntu or devices.eth0.nictype eq bridged

    images?filter=Properties.os eq Centos and not UpdateSource.Protocol eq simplestreams

## Asynchronous operations

Any operation which may take more than a second to be done must be done
in the background, returning a background operation ID to the client.

The client will then be able to either poll for a status update or wait
for a notification using the long-poll API.

## Notifications

A WebSocket-based API is available for notifications, different notification
types exist to limit the traffic going to the client.

It's recommended that the client always subscribes to the operations
notification type before triggering remote operations so that it doesn't
have to then poll for their status.

## PUT vs PATCH

The Incus API supports both PUT and PATCH to modify existing objects.

PUT replaces the entire object with a new definition, it's typically
called after the current object state was retrieved through GET.

To avoid race conditions, the ETag header should be read from the GET
response and sent as If-Match for the PUT request. This will cause Incus
to fail the request if the object was modified between GET and PUT.

PATCH can be used to modify a single field inside an object by only
specifying the property that you want to change. To unset a key, setting
it to empty will usually do the trick, but there are cases where PATCH
won't work and PUT needs to be used instead.

## Instances, containers and virtual-machines

The documentation shows paths such as `/1.0/instances/...`, which were introduced with Incus 3.19.
Older releases that supported only containers and not virtual machines supply the exact same API at `/1.0/containers/...`.

For backward compatibility reasons, Incus does still expose and support
that `/1.0/containers` API, though for the sake of brevity, we decided
not to double-document everything.

An additional endpoint at `/1.0/virtual-machines` is also present and
much like `/1.0/containers` will only show you instances of that type.

## API structure

Incus has an auto-generated [Swagger](https://swagger.io/) specification describing its API endpoints.
The YAML version of this API specification can be found in [`rest-api.yaml`](https://github.com/lxc/incus/blob/main/doc/rest-api.yaml).
See {doc}`api` for a convenient web rendering of it.
