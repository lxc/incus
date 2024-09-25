# How to run

To run all tests, including the Go tests, run from repository root:

    sudo -E make check

To run only the integration tests, run from the test directory:

    sudo -E ./main.sh

# Environment variables

Name                             | Default                   | Description
:--                              | :---                      | :----------
`INCUS_BACKEND`                  | dir                       | What backend to test against (btrfs, ceph, dir, lvm, zfs, or random)
`INCUS_CEPH_CLUSTER`             | ceph                      | The name of the ceph cluster to create osd pools in
`INCUS_CEPH_CEPHFS`              | ""                        | Enables the CephFS tests using the specified cephfs filesystem for `cephfs` pools
`INCUS_CEPH_CEPHOBJECT_RADOSGW`  | ""                        | Enables the Ceph Object tests using the specified radosgw HTTP endpoint for `cephobject` pools
`INCUS_CONCURRENT`               | 0                         | Run concurrency tests, very CPU intensive
`INCUS_DEBUG`                    | 0                         | Run incusd, incus and the shell in debug mode (very verbose)
`INCUS_INSPECT`                  | 0                         | Don't teardown the test environment on failure
`INCUS_LOGS `                    | ""                        | Path to a directory to copy all the Incus logs to
`INCUS_OFFLINE`                  | 0                         | Skip anything that requires network access
`INCUS_SKIP_TESTS`               | ""                        | Space-delimited list of test names to skip
`INCUS_TEST_IMAGE`               | "" (busybox test image)   | Path to an image tarball to use instead of the default busybox image
`INCUS_TMPFS`                    | 0                         | Sets up a tmpfs for the whole testsuite to run on (fast but needs memory)
`INCUS_NIC_SRIOV_PARENT`         | ""                        | Enables SR-IOV NIC tests using the specified parent device
`INCUS_IB_PHYSICAL_PARENT`       | ""                        | Enables Infiniband physical tests using the specified parent device
`INCUS_IB_SRIOV_PARENT`          | ""                        | Enables Infiniband SR-IOV tests using the specified parent device
`INCUS_NIC_BRIDGED_DRIVER`       | ""                        | Specifies bridged NIC driver for tests (either native or openvswitch, defaults to native)
`INCUS_REQUIRED_TESTS`           | ""                        | Space-delimited list of test names that must not be skipped if their prerequisites are not met
`INCUS_VERBOSE`                  | 0                         | Run incusd, incus and the shell in verbose mode
