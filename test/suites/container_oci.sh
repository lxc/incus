test_container_oci() {
  if [ -n "${INCUS_OFFLINE:-}" ]; then
    echo "==> SKIP: Skipping OCI tests as running offline"
    return
  fi

  ensure_has_localhost_remote "${INCUS_ADDR}"
  incus network create inct$$

  incus remote add docker https://docker.io --protocol=oci
  incus launch docker:hello-world --console --ephemeral --network=inct$$

  incus launch docker:caddy caddy --network=inct$$
  incus info caddy | grep -q RUNNING
  incus delete -f caddy

  incus network delete inct$$
  incus remote remove docker
}
