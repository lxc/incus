test_tls_restrictions() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  FINGERPRINT=$(inc config trust list --format csv | cut -d, -f4)

  # Validate admin rights with no restrictions
  inc_remote project create localhost:blah

  # Validate normal view with no restrictions
  inc_remote project list localhost: | grep -q default
  inc_remote project list localhost: | grep -q blah

  # Apply restrictions
  inc config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | inc config trust edit "${FINGERPRINT}"

  # Confirm no project visible when none listed
  [ "$(inc_remote project list localhost: --format csv | wc -l)" = 0 ]

  # Allow access to project blah
  inc config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: ['blah']/" -e "s/restricted: false/restricted: true/" | inc config trust edit "${FINGERPRINT}"

  # Validate restricted view
  ! inc_remote project list localhost: | grep -q default || false
  inc_remote project list localhost: | grep -q blah

  ! inc_remote project create localhost:blah1 || false

  # Cleanup
  inc config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | inc config trust edit "${FINGERPRINT}"
  inc project delete blah
}

test_certificate_edit() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Generate a certificate
  openssl req -x509 -newkey ec \
    -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -nodes \
    -keyout "${INCUS_CONF}/client.key.new" -out "${INCUS_CONF}/client.crt.new" \
    -days 3650 -subj "/CN=test.local"

  FINGERPRINT=$(inc config trust list --format csv | cut -d, -f4)

  # Try replacing the own certificate with a new one.
  # This should succeed as the user is listed as an admin.
  curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${INCUS_CONF}/client.crt.new")\"}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}"

  # Record new fingerprint
  FINGERPRINT=$(inc config trust list --format csv | cut -d, -f4)

  # Move new certificate and key to INCUS_CONF and back up old files.
  mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/client.crt.bak"
  mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/client.key.bak"
  mv "${INCUS_CONF}/client.crt.new" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/client.key.new" "${INCUS_CONF}/client.key"

  inc_remote project create localhost:blah

  # Apply restrictions
  inc config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | inc config trust edit "${FINGERPRINT}"

  # Add created project to the list of restricted projects. This way, the user will be listed as
  # a normal user instead of an admin.
  inc config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: \[blah\]/" | inc config trust edit "${FINGERPRINT}"

  # Try replacing the own certificate with the old one.
  # This should succeed as well as the own certificate may be changed.
  curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${INCUS_CONF}/client.crt.bak")\"}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}"

  # Move new certificate and key to INCUS_CONF and back up old ones.
  mv "${INCUS_CONF}/client.crt.bak" "${INCUS_CONF}/client.crt"
  mv "${INCUS_CONF}/client.key.bak" "${INCUS_CONF}/client.key"

  # Record new fingerprint
  FINGERPRINT=$(inc config trust list --format csv | cut -d, -f4)

  # Trying to change other fields should fail as a non-admin.
  ! inc_remote config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | inc_remote config trust edit localhost:"${FINGERPRINT}" || false

  curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"restricted\": false}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}"

  ! inc_remote config trust show "${FINGERPRINT}" | sed -e "s/name:.*/name: foo/" | inc_remote config trust edit localhost:"${FINGERPRINT}" || false

  curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"name\": \"bar\"}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}"

  ! inc_remote config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n- blah/projects: \[\]/' | inc_remote config trust edit localhost:"${FINGERPRINT}" || false

  curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"projects\": []}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}"

  # Cleanup
  inc config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | inc config trust edit "${FINGERPRINT}"

  inc config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n- blah/projects: \[\]/' | inc config trust edit "${FINGERPRINT}"

  inc project delete blah
}
