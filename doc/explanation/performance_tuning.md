(performance-tuning)=
# About performance tuning

When you are ready to move your Incus setup to production, you should take some time to optimize the performance of your system.
There are different aspects that impact performance.
The following steps help you to determine the choices and settings that you should tune to improve your Incus setup.

## Run benchmarks

Incus provides a benchmarking tool to evaluate the performance of your system.
You can use the tool to initialize or launch a number of containers and measure the time it takes for the system to create the containers.
By running the tool repeatedly with different Incus configurations, system settings or even hardware setups, you can compare the performance and evaluate which is the ideal configuration.

See {ref}`benchmark-performance` for instructions on running the tool.

## Monitor instance metrics

% Include content from [../metrics.md](../metrics.md)
```{include} ../metrics.md
    :start-after: <!-- Include start metrics intro -->
    :end-before: <!-- Include end metrics intro -->
```

You should regularly monitor the metrics to evaluate the resources that your instances use.
The numbers help you to determine if there are any spikes or bottlenecks, or if usage patterns change and require updates to your configuration.

See {ref}`metrics` for more information about metrics collection.

## Tune server settings

The default kernel settings for most Linux distributions are not optimized for running a large number of containers or virtual machines.
Therefore, you should check and modify the relevant server settings to avoid hitting limits caused by the default settings.

Typical errors that you might see when you encounter those limits are:

* `Failed to allocate directory watch: Too many open files`
* `<Error> <Error>: Too many open files`
* `failed to open stream: Too many open files in...`
* `neighbour: ndisc_cache: neighbor table overflow!`

See {ref}`server-settings` for a list of relevant server settings and suggested values.

## Tune the network bandwidth

If you have a lot of local activity between instances or between the Incus host and the instances, or if you have a fast internet connection, you should consider increasing the network bandwidth of your Incus setup.
You can do this by increasing the transmit and receive queue lengths.

See {ref}`network-increase-bandwidth` for instructions.
