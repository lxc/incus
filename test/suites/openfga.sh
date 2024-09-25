test_openfga() {
  if ! command -v openfga >/dev/null 2>&1 || ! command -v fga >/dev/null 2>&1; then
    echo "==> SKIP: Missing OpenFGA"
    return
  fi

  incus config set core.https_address "${INCUS_ADDR}"
  ensure_has_localhost_remote "${INCUS_ADDR}"
  ensure_import_testimage

  # Run OIDC server.
  spawn_oidc
  set_oidc user1

  incus config set "oidc.issuer=http://127.0.0.1:$(cat "${TEST_DIR}/oidc.port")/"
  incus config set "oidc.client.id=device"

  BROWSER=curl incus remote add --accept-certificate oidc-openfga "${INCUS_ADDR}" --auth-type oidc
  [ "$(incus info oidc-openfga: | grep ^auth_user_name | sed "s/.*: //g")" = "user1" ]

  # Run the openfga server.
  run_openfga

  # Create store and get store ID.
  OPENFGA_STORE_ID="$(fga store create --name "test" | jq -r '.store.id')"

  # Configure OpenFGA in Incus.
  incus config set openfga.api.url "$(fga_address)"
  incus config set openfga.api.token "$(fga_token)"
  incus config set openfga.store.id "${OPENFGA_STORE_ID}"

  # Wait for initial connection to OpenFGA.
  sleep 1s

  echo "==> Checking permissions for unknown user..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_admin
  user_is_not_project_operator

  # Give the user the `admin` entitlement on `server:incus`.
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 admin server:incus

  echo "==> Checking permissions for server admin..."
  user_is_server_admin
  user_is_server_operator
  user_is_project_admin
  user_is_project_operator

  # Give the user the `operator` entitlement on `server:incus`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 admin server:incus
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 operator server:incus

  echo "==> Checking permissions for server operator..."
  user_is_not_server_admin
  user_is_server_operator
  user_is_not_project_admin
  user_is_project_operator

  # Give the user the `admin` entitlement on `project:default`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 operator server:incus
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 admin project:default

  echo "==> Checking permissions for project admin..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_project_admin
  user_is_project_operator

  # Give the user the `operator` entitlement on `project:default`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 admin project:default
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 operator project:default

  echo "==> Checking permissions for project operator..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_admin
  user_is_project_operator

  # Create an instance for testing the "instance -> user" relation.
  incus launch testimage user-foo

  # Change permission to "user" for instance "user-foo"
  # We need to do this after the instance is created, otherwise the instance tuple won't exist in OpenFGA.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 operator project:default
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 user instance:default/user-foo

  echo "==> Checking permissions for instance user..."
  user_is_instance_user user-foo # Pass instance name into test as we don't have permission to create one.
  incus delete user-foo --force # Must clean this up now as subsequent tests assume a clean project.
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_admin
  user_is_not_project_operator

  # Unset config keys.
  kill_oidc
  incus config unset oidc.issuer
  incus config unset oidc.client.id
  incus config unset openfga.api.url
  incus config unset openfga.api.token
  incus config unset openfga.store.id
  incus remote remove oidc-openfga

  shutdown_openfga
}

user_is_not_server_admin() {
  # Can always see server info (type-bound public access https://openfga.dev/docs/modeling/public-access).
  incus info oidc-openfga: > /dev/null

  # Cannot see any config.
  ! incus info oidc-openfga: | grep -Fq 'core.https_address' || false

  # Cannot set any config.
  ! incus config set oidc-openfga: core.proxy_https=https://example.com || false

  # Should still be able to list storage pools but not be able to see any storage pool config or delete.
  [ "$(incus storage list oidc-openfga: -f csv | wc -l)" = 1 ]
  incus storage create test-pool dir
  ! incus storage set oidc-openfga:test-pool rsync.compression=true || false
  ! incus storage show oidc-openfga:test-pool | grep -Fq 'source:' || false
  ! incus storage delete oidc-openfga:test-pool || false
  incus storage delete test-pool

  # Should not be able to create a storage pool.
  ! incus storage create oidc-openfga:test dir || false

  # Should still be able to list certificates.
  [ "$(incus config trust list oidc-openfga: -f csv -cf | wc -l)" = 1 ]

  # Cannot edit certificates.
  fingerprint="$(incus config trust list -f csv -cf)"
  ! incus config trust show "${fingerprint}" | sed -e "s/restricted: false/restricted: true/" | incus config trust edit "oidc-openfga:${fingerprint}" || false
}

user_is_not_server_operator() {
  # Should not be able to create a project.
  ! incus project create oidc-openfga:new-project || false
}

user_is_server_admin() {
  # Should be able to see server config.
  incus info oidc-openfga: | grep -Fq 'core.https_address'

  # Should be able to add/remove certificates.
  gen_cert openfga-test
  test_cert_fingerprint="$(cert_fingerprint "${INCUS_CONF}/openfga-test.crt")"
  certificate_add_token="$(incus config trust add oidc-openfga:test --quiet)"
  mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/client.crt.bak"
  mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/client.key.bak"
  mv "${INCUS_CONF}/openfga-test.crt" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/openfga-test.key" "${INCUS_CONF}/client.key"
  incus remote add test-remote "${certificate_add_token}"
  mv "${INCUS_CONF}/client.crt.bak" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/client.key.bak" "${INCUS_CONF}/client.key"
  incus config trust remove "oidc-openfga:${test_cert_fingerprint}"
  incus remote remove test-remote

  # Should be able to create/edit/delete a storage pool.
  incus storage create oidc-openfga:test-pool dir
  incus storage set oidc-openfga:test-pool rsync.compression=true
  incus storage show oidc-openfga:test-pool | grep -Fq 'rsync.compression:'
  incus storage delete oidc-openfga:test-pool
}

user_is_server_operator() {
  # Should be able to see projects.
  incus project list oidc-openfga: -f csv | grep -Fq 'default'

  # Should be able to create/edit/delete resources within a project.
  incus init --empty oidc-openfga:foo
  incus delete oidc-openfga:foo
}

user_is_project_admin() {
  incus project set oidc-openfga:default user.foo bar
  incus project unset oidc-openfga:default user.foo
}

user_is_not_project_admin() {
  ! incus project set oidc-openfga:default user.foo bar || false
  ! incus project unset oidc-openfga:default user.foo || false
}

user_is_project_operator() {
    # Should be able to create/edit/delete project level resources
    incus profile create oidc-openfga:test-profile
    incus profile device add oidc-openfga:test-profile eth0 none
    incus profile delete oidc-openfga:test-profile
    incus network create oidc-openfga:test-network
    incus network set oidc-openfga:test-network bridge.mtu=1500
    incus network delete oidc-openfga:test-network
    incus network acl create oidc-openfga:test-network-acl
    incus network acl delete oidc-openfga:test-network-acl
    incus network zone create oidc-openfga:test-network-zone
    incus network zone delete oidc-openfga:test-network-zone
    pool_name="$(incus storage list oidc-openfga: -f csv | cut -d, -f1)"
    incus storage volume create "oidc-openfga:${pool_name}" test-volume
    incus storage volume delete "oidc-openfga:${pool_name}" test-volume
    incus launch testimage oidc-openfga:operator-foo
    INC_LOCAL='' incus_remote exec oidc-openfga:operator-foo -- echo "bar"
    incus delete oidc-openfga:operator-foo --force
}

user_is_not_project_operator() {

  # Project list will not fail but there will be no output.
  [ "$(incus project list oidc-openfga: -f csv | wc -l)" = 0 ]
  ! incus project show oidc-openfga:default || false

  # Should not be able to see or create any instances.
  incus init testimage c1
  [ "$(incus list oidc-openfga: -f csv | wc -l)" = 0 ]
  [ "$(incus list oidc-openfga: -f csv --all-projects | wc -l)" = 0 ]
  ! incus init testimage oidc-openfga:test-instance || false
  incus delete c1 -f

  # Should not be able to see network allocations.
  [ "$(incus network list-allocations oidc-openfga: -f csv | wc -l)" = 0 ]
  [ "$(incus network list-allocations oidc-openfga: --all-projects -f csv | wc -l)" = 0 ]

  # Should not be able to see or create networks.
  [ "$(incus network list oidc-openfga: -f csv | wc -l)" = 0 ]
  ! incus network create oidc-openfga:test-network || false

  # Should not be able to see or create network ACLs.
  incus network acl create acl1
  [ "$(incus network acl list oidc-openfga: -f csv | wc -l)" = 0 ]
  ! incus network acl create oidc-openfga:test-acl || false
  incus network acl delete acl1

  # Should not be able to see or create network zones.
  incus network zone create zone1
  [ "$(incus network zone list oidc-openfga: -f csv | wc -l)" = 0 ]
  ! incus network zone create oidc-openfga:test-zone || false
  incus network zone delete zone1

  # Should not be able to see or create profiles.
  [ "$(incus profile list oidc-openfga: -f csv | wc -l)" = 0 ]
  ! incus profile create oidc-openfga:test-profile || false

  # Should not be able to see or create image aliases
  test_image_fingerprint="$(incus image info testimage | awk '/^Fingerprint/ {print $2}')"
  [ "$(incus image alias list oidc-openfga: -f csv | wc -l)" = 0 ]
  ! incus image alias create oidc-openfga:testimage2 "${test_image_fingerprint}" || false

  # Should not be able to see or create storage pool volumes.
  pool_name="$(incus storage list oidc-openfga: -f csv | cut -d, -f1)"
  incus storage volume create "${pool_name}" vol1
  [ "$(incus storage volume list "oidc-openfga:${pool_name}" -f csv | wc -l)" = 0 ]
  [ "$(incus storage volume list "oidc-openfga:${pool_name}" --all-projects -f csv | wc -l)" = 0 ]
  ! incus storage volume create "oidc-openfga:${pool_name}" test-volume || false
  incus storage volume delete "${pool_name}" vol1

  # Should not be able to see any operations.
  [ "$(incus operation list oidc-openfga: -f csv | wc -l)" = 0 ]
  [ "$(incus operation list oidc-openfga: --all-projects -f csv | wc -l)" = 0 ]

  # Image list will still work but none will be shown because none are public.
  [ "$(incus image list oidc-openfga: -f csv | wc -l)" = 0 ]

  # Image edit will fail. Note that this fails with "not found" because we fail to resolve the alias (image is not public
  # so it is not returned from the DB).
  ! incus image set-property oidc-openfga:testimage requirements.secureboot true || false
  test_image_fingerprint_short="$(echo "${test_image_fingerprint}" | cut -c1-12)"
  ! incus image set-property "oidc-openfga:${test_image_fingerprint_short}" requirements.secureboot true || false

  # Should be able to list public images.
  incus image show testimage | sed -e "s/public: false/public: true/" | incus image edit testimage
  incus image list oidc-openfga: -f csv | grep -Fq "${test_image_fingerprint_short}"
  incus image show testimage | sed -e "s/public: true/public: false/" | incus image edit testimage
}

user_is_instance_user() {
  instance_name="${1}"

  # Check we can still interact with the instance.
  touch "${TEST_DIR}/tmp"
  incus file push "${TEST_DIR}/tmp" "oidc-openfga:${instance_name}/root/tmpfile.txt"
  INC_LOCAL='' incus_remote exec "oidc-openfga:${instance_name}" -- rm /root/tmpfile.txt
  rm "${TEST_DIR}/tmp"

  # We can't edit the instance though
  ! incus config set "oidc-openfga:${instance_name}" user.fizz=buzz || false
}
