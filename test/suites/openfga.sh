test_openfga() {
  echo "==> SKIP: OpenFGA tests require a functional OIDC backend"
  return

  incus config set core.https_address "${INCUS_ADDR}"
  ensure_has_localhost_remote "${INCUS_ADDR}"
  ensure_import_testimage

  # Run the openfga server.
  run_openfga

  # Create store and get store ID.
  OPENFGA_STORE_ID="$(fga store create --name "test" | jq -r '.store.id')"

  # Configure OpenFGA in LXD.
  incus config set openfga.api.url "$(fga_address)"
  incus config set openfga.api.token "$(fga_token)"
  incus config set openfga.store.id "${OPENFGA_STORE_ID}"

  echo "==> Checking permissions for unknown user..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
  user_is_not_project_operator

  # Give the user the `admin` entitlement on `server:lxd`.
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 admin server:lxd

  echo "==> Checking permissions for server admin..."
  user_is_server_admin
  user_is_server_operator
  user_is_project_manager
  user_is_project_operator

  # Give the user the `operator` entitlement on `server:lxd`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 admin server:lxd
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 operator server:lxd

  echo "==> Checking permissions for server operator..."
  user_is_not_server_admin
  user_is_server_operator
  user_is_project_manager
  user_is_project_operator

  # Give the user the `manager` entitlement on `project:default`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 operator server:lxd
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 manager project:default

  echo "==> Checking permissions for project manager..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_project_manager
  user_is_project_operator

  # Give the user the `operator` entitlement on `project:default`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 manager project:default
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 operator project:default

  echo "==> Checking permissions for project operator..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
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
  user_is_not_project_manager
  user_is_not_project_operator

  # Unset config keys.
  incus config unset candid.api.url
  incus config unset candid.api.key
  incus config unset openfga.api.url
  incus config unset openfga.api.token
  incus config unset openfga.store.id
  incus remote remove candid-openfga

  shutdown_openfga
}

user_is_not_server_admin() {
  # Can always see server info (type-bound public access https://openfga.dev/docs/modeling/public-access).
  incus info candid-openfga: > /dev/null

  # Cannot see any config.
  ! incus info candid-openfga: | grep -Fq 'core.https_address' || false

  # Cannot set any config.
  ! incus config set candid-openfga: core.proxy_https=https://example.com || false

  # Should still be able to list storage pools but not be able to see any storage pool config or delete.
  [ "$(incus storage list candid-openfga: -f csv | wc -l)" = 1 ]
  incus storage create test-pool dir
  ! incus storage set candid-openfga:test-pool rsync.compression=true || false
  ! incus storage show candid-openfga:test-pool | grep -Fq 'source:' || false
  ! incus storage delete candid-openfga:test-pool || false
  incus storage delete test-pool

  # Should not be able to create a storage pool.
  ! incus storage create candid-openfga:test dir || false

  # Should still be able to list certificates.
  [ "$(incus config trust list candid-openfga: -f csv | wc -l)" = 1 ]

  # Cannot edit certificates.
  fingerprint="$(incus config trust list -f csv | cut -d, -f4)"
  ! incus config trust show "${fingerprint}" | sed -e "s/restricted: false/restricted: true/" | incus config trust edit "candid-openfga:${fingerprint}" || false
}

user_is_not_server_operator() {
  # Should not be able to create a project.
  ! incus project create candid-openfga:new-project || false
}

user_is_server_admin() {
  # Should be able to see server config.
  incus info candid-openfga: | grep -Fq 'core.https_address'

  # Should be able to add/remove certificates.
  gen_cert openfga-test
  test_cert_fingerprint="$(cert_fingerprint "${INCUS_CONF}/openfga-test.crt")"
  certificate_add_token="$(incus config trust add candid-openfga: --name test --quiet)"
  mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/client.crt.bak"
  mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/client.key.bak"
  mv "${INCUS_CONF}/openfga-test.crt" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/openfga-test.key" "${INCUS_CONF}/client.key"
  incus remote add test-remote "${certificate_add_token}"
  mv "${INCUS_CONF}/client.crt.bak" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/client.key.bak" "${INCUS_CONF}/client.key"
  incus config trust remove "candid-openfga:${test_cert_fingerprint}"
  incus remote remove test-remote

  # Should be able to create/edit/delete a storage pool.
  incus storage create candid-openfga:test-pool dir
  incus storage set candid-openfga:test-pool rsync.compression=true
  incus storage show candid-openfga:test-pool | grep -Fq 'rsync.compression:'
  incus storage delete candid-openfga:test-pool
}

user_is_server_operator() {
  # Should be able to see projects.
  incus project list candid-openfga: -f csv | grep -Fq 'default'

  # Should be able to create/edit/delete a project.
  incus project create candid-openfga:test-project
  incus project show candid-openfga:test-project | sed -e 's/description: ""/description: "Test Project"/' | incus project edit candid-openfga:test-project
  incus project delete candid-openfga:test-project
}

user_is_project_manager() {
  incus project set candid-openfga:default user.foo bar
  incus project unset candid-openfga:default user.foo
}

user_is_not_project_manager() {
  ! incus project set candid-openfga:default user.foo bar || false
  ! incus project unset candid-openfga:default user.foo || false
}

user_is_project_operator() {
    # Should be able to create/edit/delete project level resources
    incus profile create candid-openfga:test-profile
    incus profile device add candid-openfga:test-profile eth0 none
    incus profile delete candid-openfga:test-profile
    incus network create candid-openfga:test-network
    incus network set candid-openfga:test-network bridge.mtu=1500
    incus network delete candid-openfga:test-network
    incus network acl create candid-openfga:test-network-acl
    incus network acl delete candid-openfga:test-network-acl
    incus network zone create candid-openfga:test-network-zone
    incus network zone delete candid-openfga:test-network-zone
    pool_name="$(incus storage list candid-openfga: -f csv | cut -d, -f1)"
    incus storage volume create "candid-openfga:${pool_name}" test-volume
    incus storage volume delete "candid-openfga:${pool_name}" test-volume
    incus launch testimage candid-openfga:operator-foo
    INC_LOCAL='' incus_remote exec candid-openfga:operator-foo -- echo "bar"
    incus delete candid-openfga:operator-foo --force
}

user_is_not_project_operator() {

  # Project list will not fail but there will be no output.
  [ "$(incus project list candid-openfga: -f csv | wc -l)" = 0 ]
  ! incus project show candid-openfga:default || false

  # Should not be able to see or create any instances.
  incus init testimage c1
  [ "$(incus list candid-openfga: -f csv | wc -l)" = 0 ]
  [ "$(incus list candid-openfga: -f csv --all-projects | wc -l)" = 0 ]
  ! incus init testimage candid-openfga:test-instance || false
  incus delete c1 -f

  # Should not be able to see network allocations.
  [ "$(incus network list-allocations candid-openfga: -f csv | wc -l)" = 0 ]
  [ "$(incus network list-allocations candid-openfga: --all-projects -f csv | wc -l)" = 0 ]

  # Should not be able to see or create networks.
  [ "$(incus network list candid-openfga: -f csv | wc -l)" = 0 ]
  ! incus network create candid-openfga:test-network || false

  # Should not be able to see or create network ACLs.
  incus network acl create acl1
  [ "$(incus network acl list candid-openfga: -f csv | wc -l)" = 0 ]
  ! incus network acl create candid-openfga:test-acl || false
  incus network acl delete acl1

  # Should not be able to see or create network zones.
  incus network zone create zone1
  [ "$(incus network zone list candid-openfga: -f csv | wc -l)" = 0 ]
  ! incus network zone create candid-openfga:test-zone || false
  incus network zone delete zone1

  # Should not be able to see or create profiles.
  [ "$(incus profile list candid-openfga: -f csv | wc -l)" = 0 ]
  ! incus profile create candid-openfga:test-profile || false

  # Should not be able to see or create image aliases
  test_image_fingerprint="$(incus image info testimage | awk '/^Fingerprint/ {print $2}')"
  [ "$(incus image alias list candid-openfga: -f csv | wc -l)" = 0 ]
  ! incus image alias create candid-openfga:testimage2 "${test_image_fingerprint}" || false

  # Should not be able to see or create storage pool volumes.
  pool_name="$(incus storage list candid-openfga: -f csv | cut -d, -f1)"
  incus storage volume create "${pool_name}" vol1
  [ "$(incus storage volume list "candid-openfga:${pool_name}" -f csv | wc -l)" = 0 ]
  [ "$(incus storage volume list "candid-openfga:${pool_name}" --all-projects -f csv | wc -l)" = 0 ]
  ! incus storage volume create "candid-openfga:${pool_name}" test-volume || false
  incus storage volume delete "${pool_name}" vol1

  # Should not be able to see any operations.
  [ "$(incus operation list candid-openfga: -f csv | wc -l)" = 0 ]
  [ "$(incus operation list candid-openfga: --all-projects -f csv | wc -l)" = 0 ]

  # Image list will still work but none will be shown because none are public.
  [ "$(incus image list candid-openfga: -f csv | wc -l)" = 0 ]

  # Image edit will fail. Note that this fails with "not found" because we fail to resolve the alias (image is not public
  # so it is not returned from the DB).
  ! incus image set-property candid-openfga:testimage requirements.secureboot true || false
  test_image_fingerprint_short="$(echo "${test_image_fingerprint}" | cut -c1-12)"
  ! incus image set-property "candid-openfga:${test_image_fingerprint_short}" requirements.secureboot true || false

  # Should be able to list public images.
  incus image show testimage | sed -e "s/public: false/public: true/" | incus image edit testimage
  incus image list candid-openfga: -f csv | grep -Fq "${test_image_fingerprint_short}"
  incus image show testimage | sed -e "s/public: true/public: false/" | incus image edit testimage
}

user_is_instance_user() {
  instance_name="${1}"

  # Check we can still interact with the instance.
  touch "${TEST_DIR}/tmp"
  incus file push "${TEST_DIR}/tmp" "candid-openfga:${instance_name}/root/tmpfile.txt"
  INC_LOCAL='' incus_remote exec "candid-openfga:${instance_name}" -- rm /root/tmpfile.txt
  rm "${TEST_DIR}/tmp"

  # We can't edit the instance though
  ! incus config set "candid-openfga:${instance_name}" user.fizz=buzz || false
}
