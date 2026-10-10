(systemd-report)=
# Report local Incus health to systemd

On systems running `systemd-report` v260 or later, Incus can publish a small set of local health metrics to systemd.

Enable reporting with:

```bash
incus config set core.metrics.systemd_report=true
```

View the current report with:

```bash
systemd-report metrics org.linuxcontainers.Incus
```

View metric descriptions with:

```bash
systemd-report describe org.linuxcontainers.Incus
```

The systemd report is independent of the `/1.0/metrics` endpoint and its network configuration. Unlike the Prometheus metrics endpoint, it reports a small snapshot of Incus's operational health rather than per-instance resource usage.

The following metrics are reported:

* `databaseReadAvailable`
  Is `1` when Incus can successfully perform a read-only database query and `0` otherwise. This does not verify database writes.

* `instanceTypeAvailable`
  Reports the result of Incus's cached support probe for each instance type. It does not attempt to start an instance.

* `unresolvedWarnings`
  Reports the number of new and acknowledged warnings associated with the local node. It counts warning records rather than recurrence counts. Resolved and cluster-wide warnings are excluded.

  If the database cannot be read, this metric is omitted rather than reported as `0`.

Systemd itself reports whether the Incus service is running. Incus therefore does not attempt to report whether its own daemon is stopped or unresponsive.

To investigate reported warnings, use:

```bash
incus warning list --all
```

Also check the Incus daemon logs for additional context.

The report socket is available at:

```text
/run/systemd/report/org.linuxcontainers.Incus
```

It is accessible to local users.
