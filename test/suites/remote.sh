test_remote_url() {
  # shellcheck disable=2153
  for url in "${INCUS_ADDR}" "https://${INCUS_ADDR}"; do
    inc_remote remote add test "${url}" --accept-certificate --password foo
    inc_remote info test:
    inc_remote config trust list | awk '/@/ {print $8}' | while read -r line ; do
      inc_remote config trust remove "\"${line}\""
    done
    inc_remote remote remove test
  done

  # shellcheck disable=2153
  urls="${INCUS_DIR}/unix.socket unix:${INCUS_DIR}/unix.socket unix://${INCUS_DIR}/unix.socket"
  if [ -z "${INCUS_OFFLINE:-}" ]; then
    urls="https://images.linuxcontainers.org ${urls}"
  fi

  for url in ${urls}; do
    # an invalid protocol returns an error
    ! inc_remote remote add test "${url}" --accept-certificate --password foo --protocol foo || false

    if echo "${url}" | grep -q linuxcontainers.org; then
      inc_remote remote add test "${url}" --protocol=simplestreams
    else
      inc_remote remote add test "${url}"
    fi

    inc_remote remote remove test
  done
}

test_remote_url_with_token() {
  # Try adding remote using a correctly constructed but invalid token
  invalid_token="eyJjbGllbnRfbmFtZSI6IiIsImZpbmdlcnByaW50IjoiMWM0MmMzOTgxOWIyNGJiYjQxNGFhYTY2NDUwNzlmZGY2NDQ4MTUzMDcxNjA0YTFjODJjMjVhN2JhNjBkZmViMCIsImFkZHJlc3NlcyI6WyIxOTIuMTY4LjE3OC4yNDo4NDQzIiwiWzIwMDM6Zjc6MzcxMToyMzAwOmQ5ZmY6NWRiMDo3ZTA2OmQ1ODldOjg0NDMiLCIxMC41OC4yLjE6ODQ0MyIsIjEwLjAuMy4xOjg0NDMiLCIxOTIuMTY4LjE3OC43MDo4NDQzIiwiWzIwMDM6Zjc6MzcxMToyMzAwOjQwMTY6ODVkNDo2M2FlOjNhYWVdOjg0NDMiLCIxMC4xMjQuODYuMTo4NDQzIiwiW2ZkNDI6ZTY5Zjo3OTczOjIyMjU6OjFdOjg0NDMiXSwic2VjcmV0IjoiODVlMGU5YmViODk0ZTFhMTU3YmYxODI4YTk0Y2IwYTdjY2YxMzQ4NzMyN2ZjMTY3MDcyY2JlNjQ3NmVmOGJkMiJ9"
  ! inc_remote remote add test "${invalid_token}" || false

  # Generate token for client foo
  echo foo | inc config trust add -q

  # Listing all tokens should show only a single one
  [ "$(inc config trust list-tokens -f json | jq '[.[] | select(.ClientName == "foo")] |  length')" -eq 1 ]

  # Extract token
  token="$(inc config trust list-tokens -f json | jq '.[].Token')"

  # Invalidate token so that it cannot be used again
  inc config trust revoke-token foo

  # Ensure the token is invalidated
  [ "$(inc config trust list-tokens -f json | jq 'length')" -eq 0 ]

  # Try adding the remote using the invalidated token
  ! inc_remote remote add test "${token}" || false

  # Generate token for client foo
  inc project create foo
  echo foo | inc config trust add -q --projects foo --restricted

  # Extract the token
  token="$(inc config trust list-tokens -f json | jq -r '.[].Token')"

  # Add the valid token
  inc_remote remote add test "${token}"

  # Ensure the token is invalidated
  [ "$(inc config trust list-tokens -f json | jq 'length')" -eq 0 ]

  # List instances as the remote has been added
  inc_remote ls test:

  # Clean up
  inc_remote remote remove test
  inc config trust rm "$(inc config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  echo foo | inc config trust add -q

  # Extract token
  token="$(inc config trust list-tokens -f json | jq '.[].Token')"

  # create new certificate
  openssl req -x509 -newkey rsa:2048 -keyout "${TEST_DIR}/token-client.key" -nodes -out "${TEST_DIR}/token-client.crt" -subj "/CN=incus.local"

  # Try accessing instances (this should fail)
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances" | jq '.error_code')" -eq 403 ]

  # Add valid token
  curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" -X POST -d "{\"password\": ${token}}" "https://${INCUS_ADDR}/1.0/certificates"

  # Check if we can see instances
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances" | jq '.status_code')" -eq 200 ]

  inc config trust rm "$(inc config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  echo foo | inc config trust add -q --projects foo --restricted

  # Extract token
  token="$(inc config trust list-tokens -f json | jq '.[].Token')"

  # Add valid token but override projects
  curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" -X POST -d "{\"password\":${token},\"projects\":[\"default\",\"foo\"],\"restricted\":false}" "https://${INCUS_ADDR}/1.0/certificates"

  # Check if we can see instances in the foo project
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances?project=foo" | jq '.status_code')" -eq 200 ]

  # Check if we can see instances in the default project (this should fail)
  [ "$(curl -k -s --key "${TEST_DIR}/token-client.key" --cert "${TEST_DIR}/token-client.crt" "https://${INCUS_ADDR}/1.0/instances" | jq '.error_code')" -eq 403 ]

  inc config trust rm "$(inc config trust list -f json | jq -r '.[].fingerprint')"

  # Set token expiry to 5 seconds
  inc config set core.remote_token_expiry 5S

  # Generate new token
  token="$(inc config trust add --name foo | tail -n1)"

  # Try adding remote. This should succeed.
  inc_remote remote add test "${token}"

  # Remove all trusted clients
  inc config trust rm "$(inc config trust list -f json | jq -r '.[].fingerprint')"

  # Remove remote
  inc_remote remote rm test

  # Generate new token
  token="$(inc config trust add --name foo | tail -n1)"

  # This will cause the token to expire
  sleep 5

  # Try adding remote. This should fail.
  ! inc_remote remote add test "${token}" || false

  # Unset token expiry
  inc config unset core.remote_token_expiry
}

test_remote_admin() {
  ! inc_remote remote add badpass "${INCUS_ADDR}" --accept-certificate --password bad || false
  ! inc_remote list badpass: || false

  inc_remote remote add foo "${INCUS_ADDR}" --accept-certificate --password foo
  inc_remote remote list | grep 'foo'

  inc_remote remote set-default foo
  [ "$(inc_remote remote get-default)" = "foo" ]

  inc_remote remote rename foo bar
  inc_remote remote list | grep 'bar'
  inc_remote remote list | grep -v 'foo'
  [ "$(inc_remote remote get-default)" = "bar" ]

  ! inc_remote remote remove bar || false
  inc_remote remote set-default local
  inc_remote remote remove bar

  # This is a test for #91, we expect this to block asking for a password if we
  # tried to re-add our cert.
  echo y | inc_remote remote add foo "${INCUS_ADDR}"
  inc_remote remote remove foo

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_cert client2

  # Test for #623
  inc_remote remote add test-623 "${INCUS_ADDR}" --accept-certificate --password foo
  inc_remote remote remove test-623

  # now re-add under a different alias
  inc_remote config trust add "${INCUS_CONF}/client2.crt"
  if [ "$(inc_remote config trust list | wc -l)" -ne 7 ]; then
    echo "wrong number of certs"
    false
  fi

  # Check that we can add domains with valid certs without confirmation:
  if [ -z "${INCUS_OFFLINE:-}" ]; then
    inc_remote remote add images1 images.linuxcontainers.org
    inc_remote remote add images2 images.linuxcontainers.org:443
    inc_remote remote remove images1
    inc_remote remote remove images2
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

  inc_remote remote add incus2 "${INCUS2_ADDR}" --accept-certificate --password foo

  # we need a public image on localhost

  inc_remote image export localhost:testimage "${INCUS_DIR}/foo"
  inc_remote image delete localhost:testimage
  sum=$(sha256sum "${INCUS_DIR}/foo.tar.xz" | cut -d' ' -f1)
  inc_remote image import "${INCUS_DIR}/foo.tar.xz" localhost: --public
  inc_remote image alias create localhost:testimage "${sum}"

  inc_remote image delete "incus2:${sum}" || true

  inc_remote image copy localhost:testimage incus2: --copy-aliases --public
  inc_remote image delete "localhost:${sum}"
  inc_remote image copy "incus2:${sum}" local: --copy-aliases --public
  inc_remote image info localhost:testimage
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2:
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:$(echo "${sum}" | colrm 3)" incus2:
  inc_remote image delete "incus2:${sum}"

  # test a private image
  inc_remote image copy "localhost:${sum}" incus2:
  inc_remote image delete "localhost:${sum}"
  inc_remote init "incus2:${sum}" localhost:c1
  inc_remote delete localhost:c1

  inc_remote image alias create localhost:testimage "${sum}"

  # test remote publish
  inc_remote init testimage pub
  inc_remote publish pub incus2: --alias bar --public a=b
  inc_remote image show incus2:bar | grep -q "a: b"
  inc_remote image show incus2:bar | grep -q "public: true"
  ! inc_remote image show bar || false
  inc_remote delete pub

  # test spawn from public server
  inc_remote remote add incus2-public "${INCUS2_ADDR}" --public --accept-certificate
  inc_remote init incus2-public:bar pub
  inc_remote image delete incus2:bar
  inc_remote delete pub

  # Double launch to test if the image downloads only once.
  inc_remote init localhost:testimage incus2:c1 &
  C1PID=$!

  inc_remote init localhost:testimage incus2:c2
  inc_remote delete incus2:c2

  wait "${C1PID}"
  inc_remote delete incus2:c1

  # launch testimage stored on localhost as container c1 on incus2
  inc_remote launch localhost:testimage incus2:c1

  # make sure it is running
  inc_remote list incus2: | grep c1 | grep RUNNING
  inc_remote info incus2:c1
  inc_remote stop incus2:c1 --force
  inc_remote delete incus2:c1

  # Test that local and public servers can be accessed without a client cert
  mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/client.crt.bak"
  mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/client.key.bak"

  # testimage should still exist on the local server.
  inc_remote image list local: | grep -q testimage

  # Skip the truly remote servers in offline mode.  There should always be
  # Ubuntu images in the results for the remote servers.
  if [ -z "${INCUS_OFFLINE:-}" ]; then
    inc_remote image list images: | grep -i -c ubuntu
  fi

  mv "${INCUS_CONF}/client.crt.bak" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/client.key.bak" "${INCUS_CONF}/client.key"

  inc_remote image delete "incus2:${sum}"

  inc_remote image alias create localhost:foo "${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --mode=push
  inc_remote image show incus2:"${sum}"
  inc_remote image show incus2:"${sum}" | grep -q 'public: false'
  ! inc_remote image show incus2:foo || false
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --mode=push --copy-aliases --public
  inc_remote image show incus2:"${sum}"
  inc_remote image show incus2:"${sum}" | grep -q 'public: true'
  inc_remote image show incus2:foo
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --mode=push --copy-aliases --alias=bar
  inc_remote image show incus2:"${sum}"
  inc_remote image show incus2:foo
  inc_remote image show incus2:bar
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --mode=relay
  inc_remote image show incus2:"${sum}"
  inc_remote image show incus2:"${sum}" | grep -q 'public: false'
  ! inc_remote image show incus2:foo || false
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --mode=relay --copy-aliases --public
  inc_remote image show incus2:"${sum}"
  inc_remote image show incus2:"${sum}" | grep -q 'public: true'
  inc_remote image show incus2:foo
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --mode=relay --copy-aliases --alias=bar
  inc_remote image show incus2:"${sum}"
  inc_remote image show incus2:foo
  inc_remote image show incus2:bar
  inc_remote image delete "incus2:${sum}"

  # Test image copy between projects
  inc_remote project create incus2:foo
  inc_remote image copy "localhost:${sum}" incus2: --target-project foo
  inc_remote image show incus2:"${sum}" --project foo
  inc_remote image delete "incus2:${sum}" --project foo
  inc_remote image copy "localhost:${sum}" incus2: --target-project foo --mode=push
  inc_remote image show incus2:"${sum}" --project foo
  inc_remote image delete "incus2:${sum}" --project foo
  inc_remote image copy "localhost:${sum}" incus2: --target-project foo --mode=relay
  inc_remote image show incus2:"${sum}" --project foo
  inc_remote image delete "incus2:${sum}" --project foo
  inc_remote project delete incus2:foo

  # Test image copy with --profile option
  inc_remote profile create incus2:foo
  inc_remote image copy "localhost:${sum}" incus2: --profile foo
  inc_remote image show incus2:"${sum}" | grep -q '\- foo'
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --profile foo --mode=push
  inc_remote image show incus2:"${sum}" | grep -q '\- foo'
  inc_remote image delete "incus2:${sum}"

  inc_remote image copy "localhost:${sum}" incus2: --profile foo --mode=relay
  inc_remote image show incus2:"${sum}" | grep -q '\- foo'
  inc_remote image delete "incus2:${sum}"
  inc_remote profile delete incus2:foo

  inc_remote image alias delete localhost:foo

  inc_remote remote remove incus2
  inc_remote remote remove incus2-public

  kill_incus "$INCUS2_DIR"
}
