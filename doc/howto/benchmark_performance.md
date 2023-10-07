(benchmark-performance)=
# How to benchmark performance

The performance of your Incus server or cluster depends on a lot of different factors, ranging from the hardware, the server configuration, the selected storage driver and the network bandwidth to the overall usage patterns.

To find the optimal configuration, you should run benchmark tests to evaluate different setups.

Incus provides a benchmarking tool for this purpose.
This tool allows you to initialize or launch a number of containers and measure the time it takes for the system to create the containers.
If you run this tool repeatedly with different configurations, you can compare the performance and evaluate which is the ideal configuration.

## Get the tool

If the `incus-benchmark` tool isn't provided with your installation, you can build it from source.
Make sure that you have `go` (version 1.20 or later) installed and install the tool with the following command:

    go install github.com/lxc/incus/incus-benchmark@latest

## Run the tool

Run `incus-benchmark [action]` to measure the performance of your Incus setup.

The benchmarking tool uses the current Incus configuration.
If you want to use a different project, specify it with `--project`.

For all actions, you can specify the number of parallel threads to use (default is to use a dynamic batch size).
You can also choose to append the results to a CSV report file and label them in a certain way.

See `incus-benchmark help` for all available actions and flags.

### Select an image

Before you run the benchmark, select what kind of image you want to use.

Local image
: If you want to measure the time it takes to create a container and ignore the time it takes to download the image, you should copy the image to your local image store before you run the benchmarking tool.

  To do so, run a command similar to the following and specify the fingerprint (for example, `2d21da400963`) of the image when you run `incus-benchmark`:

      incus image copy images:ubuntu/22.04 local:

  You can also assign an alias to the image and specify that alias (for example, `ubuntu`) when you run `incus-benchmark`:

      incus image copy images:ubuntu/22.04 local: --alias ubuntu

Remote image
: If you want to include the download time in the overall result, specify a remote image (for example, `images:ubuntu/22.04`).
  The default image that `incus-benchmark` uses is the latest Ubuntu image (`images:ubuntu`), so if you want to use this image, you can leave out the image name when running the tool.

### Create and launch containers

Run the following command to create a number of containers:

    incus-benchmark init --count <number> <image>

Add `--privileged` to the command to create privileged containers.

For example:

```{list-table}
   :header-rows: 1

* - Command
  - Description
* - `incus-benchmark init --count 10 --privileged`
  - Create ten privileged containers that use the latest Ubuntu image.
* - `incus-benchmark init --count 20 --parallel 4 images:alpine/edge`
  - Create 20 containers that use the Alpine Edge image, using four parallel threads.
* - `incus-benchmark init 2d21da400963`
  - Create one container that uses the local image with the fingerprint `2d21da400963`.
* - `incus-benchmark init --count 10 ubuntu`
  - Create ten containers that use the image with the alias `ubuntu`.

```

If you use the `init` action, the benchmarking containers are created but not started.
To start the containers that you created, run the following command:

    incus-benchmark start

Alternatively, use the `launch` action to both create and start the containers:

    incus-benchmark launch --count 10 <image>

For this action, you can add the `--freeze` flag to freeze each container right after it starts.
Freezing a container pauses its processes, so this flag allows you to measure the pure launch times without interference of the processes that run in each container after startup.

### Delete containers

To delete the benchmarking containers that you created, run the following command:

    incus-benchmark delete

```{note}
You must delete all existing benchmarking containers before you can run a new benchmark.
```
