(ref-projects)=
# Project configuration

Projects can be configured through a set of key/value configuration options.
See {ref}`projects-configure` for instructions on how to set these options.

The key/value configuration is namespaced.
The following options are available:

- {ref}`project-features`
- {ref}`project-limits`
- {ref}`project-restrictions`
- {ref}`project-specific-config`

(project-features)=
## Project features

The project features define which entities are isolated in the project and which are inherited from the `default` project.

If a `feature.*` option is set to `true`, the corresponding entity is isolated in the project.

```{note}
When you create a project without explicitly configuring a specific option, this option is set to the initial value given in the following table.

However, if you unset one of the `feature.*` options, it does not go back to the initial value, but to the default value.
The default value for all `feature.*` options is `false`.
```

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group project-features start -->
    :end-before: <!-- config group project-features end -->
```

(project-limits)=
## Project limits

Project limits define a hard upper bound for the resources that can be used by the containers and VMs that belong to a project.

Depending on the `limits.*` option, the limit applies to the number of entities that are allowed in the project (for example, {config:option}`project-limits:limits.containers` or {config:option}`project-limits:limits.networks`) or to the aggregate value of resource usage for all instances in the project (for example, {config:option}`project-limits:limits.cpu` or {config:option}`project-limits:limits.processes`).
In the latter case, the limit usually applies to the {ref}`instance-options-limits` that are configured for each instance (either directly or via a profile), and not to the resources that are actually in use.

For example, if you set the project's {config:option}`project-limits:limits.memory` configuration to `50GiB`, the sum of the individual values of all {config:option}`instance-resource-limits:limits.memory` configuration keys defined on the project's instances will be kept under 50 GiB.

Similarly, setting the project's {config:option}`project-limits:limits.cpu` configuration key to `100` means that the sum of individual {config:option}`instance-resource-limits:limits.cpu` values will be kept below 100.

When using project limits, the following conditions must be fulfilled:

- When you set one of the `limits.*` configurations and there is a corresponding configuration for the instance, all instances in the project must have the corresponding configuration defined (either directly or via a profile).
  See {ref}`instance-options-limits` for the instance configuration options.
- The {config:option}`project-limits:limits.cpu` configuration cannot be used if {ref}`instance-options-limits-cpu` is enabled.
  This means that to use {config:option}`project-limits:limits.cpu` on a project, the {config:option}`instance-resource-limits:limits.cpu` configuration of each instance in the project must be set to a number of CPUs, not a set or a range of CPUs.
- The {config:option}`project-limits:limits.memory` configuration must be set to an absolute value, not a percentage.

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group project-limits start -->
    :end-before: <!-- config group project-limits end -->
```

(project-restrictions)=
## Project restrictions

To prevent the instances of a project from accessing security-sensitive features (such as container nesting or raw LXC configuration), set the {config:option}`project-restricted:restricted` configuration option to `true`.
You can then use the various `restricted.*` options to pick individual features that would normally be blocked by {config:option}`project-restricted:restricted` and allow them, so they can be used by the instances of the project.

For example, to restrict a project and block all security-sensitive features, but allow container nesting, enter the following commands:

    incus project set <project_name> restricted=true
    incus project set <project_name> restricted.containers.nesting=allow

Each security-sensitive feature has an associated `restricted.*` project configuration option.
If you want to allow the usage of a feature, change the value of its `restricted.*` option.
Most `restricted.*` configurations are binary switches that can be set to either `block` (the default) or `allow`.
However, some options support other values for more fine-grained control.

```{note}
You must set the `restricted` configuration to `true` for any of the `restricted.*` options to be effective.
If `restricted` is set to `false`, changing a `restricted.*` option has no effect.

Setting all `restricted.*` keys to `allow` is equivalent to setting `restricted` itself to `false`.
```

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group project-restricted start -->
    :end-before: <!-- config group project-restricted end -->
```

(project-specific-config)=
## Project-specific configuration

There are some {ref}`server` options that you can override for a project.
In addition, you can add user metadata for a project.

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group project-specific start -->
    :end-before: <!-- config group project-specific end -->
```
