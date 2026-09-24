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

    # The OCI image labels are exposed as image properties.
    incus config get caddy image.oci.version | grep -q .

    # The image environment is exported without being copied into the instance config.
    [ "$(incus config get caddy environment.CADDY_VERSION)" = "" ]
    incus exec caddy -- printenv CADDY_VERSION | grep -q .

    # Instance config overrides the image environment.
    incus config set caddy environment.CADDY_VERSION=custom
    [ "$(incus exec caddy -- printenv CADDY_VERSION)" = "custom" ]

    # Rebuilding keeps the overrides and exports the rest from the image.
    incus stop -f caddy
    incus rebuild docker:caddy caddy
    [ "$(incus config get caddy environment.CADDY_VERSION)" = "custom" ]
    [ "$(incus config get caddy environment.XDG_CONFIG_HOME)" = "" ]
    incus start caddy
    [ "$(incus exec caddy -- printenv CADDY_VERSION)" = "custom" ]
    incus exec caddy -- printenv XDG_CONFIG_HOME | grep -q .
    incus delete -f caddy

    incus network delete inct$$
    incus remote remove docker
}
