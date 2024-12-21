host.start()
host.wait_for_unit("network.target")
host.succeed(
    "sudo -u ceph ceph-authtool"
    " --create-keyring /tmp/ceph.mon.keyring"
    " --gen-key"
    " --name mon."
    " --cap mon 'allow *'",
    "sudo -u ceph ceph-authtool"
    " --create-keyring /etc/ceph/ceph.client.admin.keyring"
    " --gen-key"
    " --name client.admin"
    " --cap osd 'allow *'"
    " --cap mon 'allow *'"
    " --cap mds 'allow *'"
    " --cap mgr 'allow *'",
    "sudo -u ceph ceph-authtool /tmp/ceph.mon.keyring"
    " --import-keyring /etc/ceph/ceph.client.admin.keyring",
    "monmaptool --create"
    " --add mon0 192.168.1.1"
    " --fsid e8f34a4a-4c96-4afe-b535-ceae0f70339d"
    " /var/lib/ceph/monmap",
    "sudo -u ceph ceph-mon"
    " --mkfs"
    " --id mon0"
    " --monmap /var/lib/ceph/monmap"
    " --keyring /tmp/ceph.mon.keyring",
    "systemctl start ceph-mon-mon0.service"
)
host.wait_for_unit("ceph-mon-mon0.service")
host.succeed("ceph mon enable-msgr2")
host.succeed("ceph config set mon auth_allow_insecure_global_id_reclaim false")

host.succeed(
    "sudo -u ceph mkdir -p /var/lib/ceph/mgr/ceph-mgr0",
    "ceph auth get-or-create mgr.mgr0"
    " mon 'allow profile mgr'"
    " osd 'allow *' mds 'allow *'"
    " > /var/lib/ceph/mgr/ceph-mgr0/keyring",
    "systemctl start ceph-mgr-mgr0",
)
host.wait_for_unit("ceph-mgr-mgr0.service")

host.succeed(
    "sudo -u ceph mkdir -p /var/lib/ceph/bootstrap-osd",
    "ceph auth get-or-create client.bootstrap-osd"
    " mon 'allow profile bootstrap-osd'"
    " > /var/lib/ceph/bootstrap-osd/ceph.keyring",
    "ceph-volume lvm prepare --data /dev/vdb",
    "ceph-volume lvm prepare --data /dev/vdc",
    "ceph-volume lvm prepare --data /dev/vdd",
    "systemctl start ceph-osd-0 ceph-osd-1 ceph-osd-2"
)
host.wait_for_unit("ceph-osd-0.service")
host.wait_for_unit("ceph-osd-1.service")
host.wait_for_unit("ceph-osd-2.service")

host.succeed(
    "sudo -u ceph mkdir -p /var/lib/ceph/mds/ceph-mds0",
    "sudo -u ceph ceph"
    " auth get-or-create mds.mds0"
    " mon 'profile mds'"
    " mgr 'profile mds'"
    " mds 'allow *'"
    " osd 'allow *'"
    " > /var/lib/ceph/mds/ceph-mds0/keyring",
    "systemctl start ceph-mds-mds0.service"
)
host.wait_for_unit("ceph-mds-mds0.service")

host.succeed(
    "ceph osd pool create cephfs_meta 32",
    "ceph osd pool create cephfs_data 32",
    "ceph fs new cephfs cephfs_meta cephfs_data",
    "ceph fs ls"
)
