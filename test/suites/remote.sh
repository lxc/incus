test_remote_url() {
  # shellcheck disable=2153
  for url in "${INCUS_ADDR}" "https://${INCUS_ADDR}"; do
    token="$(incus config trust add foo -q)"
    incus_remote remote add test "${url}" --accept-certificate --token "${token}"
    incus_remote info test:
    incus_remote config trust list | awk '/@/ {print $8}' | while read -r line ; do
      incus_remote config trust remove "\"${line}\""
    done
    incus_remote remote remove test
  done

  # shellcheck disable=2153
  urls="${INCUS_DIR}/unix.socket unix:${INCUS_DIR}/unix.socket unix://${INCUS_DIR}/unix.socket"
  if [ -z "${INCUS_OFFLINE:-}" ]; then
    urls="https://images.linuxcontainers.org ${urls}"
  fi

  for url in ${urls}; do
    # an invalid protocol returns an error
    ! incus_remote remote add test "${url}" --accept-certificate --token foo --protocol foo || false

    if echo "${url}" | grep -q linuxcontainers.org; then
      incus_remote remote add test "${url}" --protocol=simplestreams
    else
      incus_remote remote add test "${url}"
    fi

    incus_remote remote remove test
  done
}

test_remote_url_with_token() {
  # Try adding remote using a correctly constructed but invalid token
  invalid_token="eyJjbGllbnRfbmFtZSI6IiIsImZpbmdlcnByaW50IjoiMWM0MmMzOTgxOWIyNGJiYjQxNGFhYTY2NDUwNzlmZGY2NDQ4MTUzMDcxNjA0YTFjODJjMjVhN2JhNjBkZmViMCIsImFkZHJlc3NlcyI6WyIxOTIuMTY4LjE3OC4yNDo4NDQzIiwiWzIwMDM6Zjc6MzcxMToyMzAwOmQ5ZmY6NWRiMDo3ZTA2OmQ1ODldOjg0NDMiLCIxMC41OC4yLjE6ODQ0MyIsIjEwLjAuMy4xOjg0NDMiLCIxOTIuMTY4LjE3OC43MDo4NDQzIiwiWzIwMDM6Zjc6MzcxMToyMzAwOjQwMTY6ODVkNDo2M2FlOjNhYWVdOjg0NDMiLCIxMC4xMjQuODYuMTo4NDQzIiwiW2ZkNDI6ZTY5Zjo3OTczOjIyMjU6OjFdOjg0NDMiXSwic2VjcmV0IjoiODVlMGU5YmViODk0ZTFhMTU3YmYxODI4YTk0Y2IwYTdjY2YxMzQ4NzMyN2ZjMTY3MDcyY2JlNjQ3NmVmOGJkMiJ9"
  ! incus_remote remote add test "${invalid_token}" || false

  # Generate token for client foo
  incus config trust add foo -q

  # Listing all tokens should show only a single one
  [ "$(incus config trust list-tokens -f json | jq '[.[] | select(.ClientName == "foo")] |  length')" -eq 1 ]

  # Extract token
  token="$(incus config trust list-tokens -f json | jq '.[].Token')"

  # Invalidate token so that it cannot be used again
  incus config trust revoke-token foo

  # Ensure the token is invalidated
  [ "$(incus config trust list-tokens -f json | jq 'length')" -eq 0 ]

  # Try adding the remote using the invalidated token
  ! incus_remote remote add test "${token}" || false

  # Generate token for client foo
  incus project create foo
  incus config trust add -q --projects foo --restricted foo

  # Extract the token
  token="$(incus config trust list-tokens -f json | jq -r '.[].Token')"

  # Add the valid token
  incus_remote remote add test "${token}"

  # Ensure the token is invalidated
  [ "$(incus config trust list-tokens -f json | jq 'length')" -eq 0 ]

  # List instances as the remote has been added
  incus_remote ls test:

  # Clean up
  incus_remote remote remove test
  incus config trust rm "$(incus config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  incus config trust add -q foo

  # Extract token
  token="$(incus config trust list-tokens -f json | jq '.[].Token')"

  # create new certificate
  gen_cert_and_key "${TEST_DIR}/token-client.key" "${TEST_DIR}/token-client.crt" "incus.local"

  # Try accessing instances (this should fail)
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances" | jq '.error_code')" -eq 403 ]

  # Add valid token
  curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" -X POST -d "{\"token\": ${token}}" "https://${INCUS_ADDR}/1.0/certificates"

  # Check if we can see instances
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances" | jq '.status_code')" -eq 200 ]

  incus config trust rm "$(incus config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  incus config trust add -q --projects foo --restricted foo

  # Extract token
  token="$(incus config trust list-tokens -f json | jq '.[].Token')"

  # Add valid token but override projects
  curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" -X POST -d "{\"token\":${token},\"projects\":[\"default\",\"foo\"],\"restricted\":false}" "https://${INCUS_ADDR}/1.0/certificates"

  # Check if we can see instances in the foo project
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances?project=foo" | jq '.status_code')" -eq 200 ]

  # Check if we can see instances in the default project (this should fail)
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances" | jq '.error_code')" -eq 403 ]

  incus config trust rm "$(incus config trust list -f json | jq -r '.[].fingerprint')"

  # Set token expiry to 5 seconds
  incus config set core.remote_token_expiry 5S

  # Generate new token
  token="$(incus config trust add foo | tail -n1)"

  # Try adding remote. This should succeed.
  incus_remote remote add test "${token}"

  # Remove all trusted clients
  incus config trust rm "$(incus config trust list -f json | jq -r '.[].fingerprint')"

  # Remove remote
  incus_remote remote rm test

  # Generate new token
  token="$(incus config trust add foo | tail -n1)"

  # This will cause the token to expire
  sleep 5

  # Try adding remote. This should fail.
  ! incus_remote remote add test "${token}" || false

  # Unset token expiry
  incus config unset core.remote_token_expiry
}

test_remote_admin() {
  ! incus_remote remote add badpass "${INCUS_ADDR}" --accept-certificate --token badtoken || false
  ! incus_remote list badpass: || false

  token="$(incus config trust add foo -q)"
  incus_remote remote add foo "${INCUS_ADDR}" --accept-certificate --token "${token}"
  incus_remote remote list | grep 'foo'

  incus_remote remote set-default foo
  [ "$(incus_remote remote get-default)" = "foo" ]

  incus_remote remote rename foo bar
  incus_remote remote list | grep 'bar'
  incus_remote remote list | grep -v 'foo'
  [ "$(incus_remote remote get-default)" = "bar" ]

  ! incus_remote remote remove bar || false
  incus_remote remote set-default local
  incus_remote remote remove bar

  # This is a test for #91, we expect this to block asking for a token if we
  # tried to re-add our cert.
  echo y | incus_remote remote add foo "${INCUS_ADDR}"
  incus_remote remote remove foo

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_cert client2

  # Test for #623
  token="$(incus config trust add foo -q)"
  incus_remote remote add test-623 "${INCUS_ADDR}" --accept-certificate --token "${token}"
  incus_remote remote remove test-623

  # now re-add under a different alias
  incus_remote config trust add-certificate "${INCUS_CONF}/client2.crt"
  if [ "$(incus_remote config trust list | wc -l)" -ne 7 ]; then
    echo "wrong number of certs"
    false
  fi

  # Check that we can add domains with valid certs without confirmation:
  if [ -z "${INCUS_OFFLINE:-}" ]; then
    incus_remote remote add images1 https://images.linuxcontainers.org --protocol=simplestreams
    incus_remote remote remove images1
  fi
}

test_remote_usage() {
  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR
  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  token="$(INCUS_DIR=${INCUS2_DIR} incus config trust add foo -q)"
  incus_remote remote add incus2 "${INCUS2_ADDR}" --accept-certificate --token "${token}"

  # we need a public image on localhost

  incus_remote image export localhost:testimage "${INCUS_DIR}/foo"
  incus_remote image delete localhost:testimage
  sum=$(sha256sum "${INCUS_DIR}/foo.tar.xz" | cut -d' ' -f1)
  incus_remote image import "${INCUS_DIR}/foo.tar.xz" localhost: --public
  incus_remote image alias create localhost:testimage "${sum}"

  incus_remote image delete "incus2:${sum}" || true

  incus_remote image copy localhost:testimage incus2: --copy-aliases --public
  incus_remote image delete "localhost:${sum}"
  incus_remote image copy "incus2:${sum}" local: --copy-aliases --public
  incus_remote image info localhost:testimage
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2:
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:$(echo "${sum}" | colrm 3)" incus2:
  incus_remote image delete "incus2:${sum}"

  # test a private image
  incus_remote image copy "localhost:${sum}" incus2:
  incus_remote image delete "localhost:${sum}"
  incus_remote init "incus2:${sum}" localhost:c1
  incus_remote delete localhost:c1

  incus_remote image alias create localhost:testimage "${sum}"

  # test remote publish
  incus_remote init testimage pub
  incus_remote publish pub incus2: --alias bar --public a=b
  incus_remote image show incus2:bar | grep -q "a: b"
  incus_remote image show incus2:bar | grep -q "public: true"
  ! incus_remote image show bar || false
  incus_remote delete pub

  # test spawn from public server
  incus_remote remote add incus2-public "${INCUS2_ADDR}" --public --accept-certificate
  incus_remote init incus2-public:bar pub
  incus_remote image delete incus2:bar
  incus_remote delete pub

  # Double launch to test if the image downloads only once.
  incus_remote init localhost:testimage incus2:c1 &
  C1PID=$!

  incus_remote init localhost:testimage incus2:c2
  incus_remote delete incus2:c2

  wait "${C1PID}"
  incus_remote delete incus2:c1

  # launch testimage stored on localhost as container c1 on incus2
  incus_remote launch localhost:testimage incus2:c1

  # make sure it is running
  incus_remote list incus2: | grep c1 | grep RUNNING
  incus_remote info incus2:c1
  incus_remote stop incus2:c1 --force
  incus_remote delete incus2:c1

  # Test that local and public servers can be accessed without a client cert
  mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/client.crt.bak"
  mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/client.key.bak"

  # testimage should still exist on the local server.
  incus_remote image list local: | grep -q testimage

  # Skip the truly remote servers in offline mode.  There should always be
  # Ubuntu images in the results for the remote servers.
  if [ -z "${INCUS_OFFLINE:-}" ]; then
    incus_remote image list images: | grep -i -c ubuntu
  fi

  mv "${INCUS_CONF}/client.crt.bak" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/client.key.bak" "${INCUS_CONF}/client.key"

  incus_remote image delete "incus2:${sum}"

  incus_remote image alias create localhost:foo "${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --mode=push
  incus_remote image show incus2:"${sum}"
  incus_remote image show incus2:"${sum}" | grep -q 'public: false'
  ! incus_remote image show incus2:foo || false
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --mode=push --copy-aliases --public
  incus_remote image show incus2:"${sum}"
  incus_remote image show incus2:"${sum}" | grep -q 'public: true'
  incus_remote image show incus2:foo
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --mode=push --copy-aliases --alias=bar
  incus_remote image show incus2:"${sum}"
  incus_remote image show incus2:foo
  incus_remote image show incus2:bar
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --mode=relay
  incus_remote image show incus2:"${sum}"
  incus_remote image show incus2:"${sum}" | grep -q 'public: false'
  ! incus_remote image show incus2:foo || false
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --mode=relay --copy-aliases --public
  incus_remote image show incus2:"${sum}"
  incus_remote image show incus2:"${sum}" | grep -q 'public: true'
  incus_remote image show incus2:foo
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --mode=relay --copy-aliases --alias=bar
  incus_remote image show incus2:"${sum}"
  incus_remote image show incus2:foo
  incus_remote image show incus2:bar
  incus_remote image delete "incus2:${sum}"

  # Test image copy between projects
  incus_remote project create incus2:foo
  incus_remote image copy "localhost:${sum}" incus2: --target-project foo
  incus_remote image show incus2:"${sum}" --project foo
  incus_remote image delete "incus2:${sum}" --project foo
  incus_remote image copy "localhost:${sum}" incus2: --target-project foo --mode=push
  incus_remote image show incus2:"${sum}" --project foo
  incus_remote image delete "incus2:${sum}" --project foo
  incus_remote image copy "localhost:${sum}" incus2: --target-project foo --mode=relay
  incus_remote image show incus2:"${sum}" --project foo
  incus_remote image delete "incus2:${sum}" --project foo
  incus_remote project delete incus2:foo

  # Test image copy with --profile option
  incus_remote profile create incus2:foo
  incus_remote image copy "localhost:${sum}" incus2: --profile foo
  incus_remote image show incus2:"${sum}" | grep -q '\- foo'
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --profile foo --mode=push
  incus_remote image show incus2:"${sum}" | grep -q '\- foo'
  incus_remote image delete "incus2:${sum}"

  incus_remote image copy "localhost:${sum}" incus2: --profile foo --mode=relay
  incus_remote image show incus2:"${sum}" | grep -q '\- foo'
  incus_remote image delete "incus2:${sum}"
  incus_remote profile delete incus2:foo

  incus_remote image alias delete localhost:foo

  incus_remote remote remove incus2
  incus_remote remote remove incus2-public

  kill_incus "$INCUS2_DIR"
}
