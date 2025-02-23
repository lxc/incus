#!/bin/bash
# test_network_address_set.sh
# A standalone test suite for Incus network address sets.
# This script exercises creation (CLI, API, from STDIN, with project scoping),
# listing, show, edit, patch, adding/removing addresses, renaming, custom keys, and deletion.
#
# Requirements:
#   - The "incus" CLI must be installed and in PATH.
#   - Optionally, INCUS_ADDR can be set (defaults to http://localhost:8443).
#
# I made this because I was unable to run test in the standard way so I made a workaround 
#

set -euo pipefail

# --- Helpers for colored output ---
function info() {
  echo -e "\033[1;34m[INFO]\033[0m $1"
}
function success() {
  echo -e "\033[1;32m[SUCCESS]\033[0m $1"
}
function error_msg() {
  echo -e "\033[1;31m[ERROR]\033[0m $1"
}

# --- Check that incus is installed ---
if ! command -v incus &> /dev/null; then
  error_msg "incus CLI could not be found. Please install it first."
  exit 1
fi

# --- Set default INCUS_ADDR if not provided ---
export INCUS_ADDR="${INCUS_ADDR:-http://localhost:8443}"

# --- Test functions ---

# Test 1: Creation using CLI (rejecting non‐hostname compatible names, then creating and deleting)
function test_creation_cli() {
  info "Test 1: Creation (CLI)"
  if incus network address-set create 2432; then
    error_msg "Non-hostname compatible name was accepted, expected rejection."
    exit 1
  else
    success "Non-hostname compatible name correctly rejected."
  fi

  incus network address-set create testAS
  success "Address set 'testAS' created."
  incus network address-set delete testAS
  success "Address set 'testAS' deleted."
}

# Test 2: Creation using project scoping
function test_creation_project() {
  info "Test 2: Creation (Project)"
  incus project create testproj -c features.networks=true
  incus network address-set create testAS --project testproj
  if incus network address-set ls --project testproj | grep -q "testAS"; then
    success "Address set 'testAS' exists in project 'testproj'."
  else
    error_msg "Address set 'testAS' not found in project 'testproj'."
    exit 1
  fi
  # Clean up
  incus network address-set delete testAS --project testproj
  incus project delete testproj
  success "Project 'testproj' and its address set cleaned up."
}

# Test 3: Creation from STDIN
function test_creation_stdin() {
  info "Test 3: Creation from STDIN"
  # Note: Ensure addresses is a YAML list (not a plain string)
  cat <<EOF | incus network address-set create testAS
description: Test Address set from STDIN
addresses:
  - 192.168.0.1
  - 192.168.0.254
external_ids:
  user.mykey: foo
EOF
  if incus network address-set show testAS | grep -q "description: Test Address set from STDIN"; then
    success "Address set created from STDIN with correct description."
  else
    error_msg "Failed: Address set created from STDIN did not have the expected description."
    exit 1
  fi
  incus network address-set delete testAS
}

# Test 4: Listing address sets
function test_listing() {
  info "Test 4: Listing"
  incus network address-set create testAS --description "Listing test"
  if incus network address-set ls | grep -q "testAS"; then
    success "Address set 'testAS' appears in listing."
  else
    error_msg "Address set 'testAS' does not appear in listing."
    exit 1
  fi
  incus network address-set delete testAS
}

# Test 5: Show command
function test_show() {
  info "Test 5: Show"
  incus network address-set create testAS --description "Show test"
  if incus network address-set show testAS | grep -q "description: Show test"; then
    success "Show command returns correct description."
  else
    error_msg "Show command did not return expected description."
    exit 1
  fi
  incus network address-set delete testAS
}

# Test 6: Edit command (using STDIN)
function test_edit() {
  info "Test 6: Edit"
  incus network address-set create testAS --description "Initial description"
  cat <<EOF | incus network address-set edit testAS
description: Updated address set
addresses:
  - 10.0.0.1
  - 10.0.0.2
external_ids:
  user.mykey: bar
EOF
  if incus network address-set show testAS | grep -q "Updated address set"; then
    success "Edit command updated the description correctly."
  else
    error_msg "Edit command failed to update the description."
    exit 1
  fi
  incus network address-set delete testAS
}

# Test 7: Patch command (update external IDs only)
function test_patch() {
  info "Test 7: Patch"
  incus network address-set create testAS --description "Patch test"
  incus query -X PATCH -d "{\"external_ids\": {\"user.myotherkey\": \"bah\"}}" /1.0/network-address-sets/testAS
  if incus network address-set show testAS | grep -q "user.myotherkey: bah"; then
    success "Patch command updated external IDs correctly."
  else
    error_msg "Patch command did not update external IDs as expected."
    exit 1
  fi
  incus network address-set delete testAS
}

# Test 8: Add and Remove addresses
function test_add_remove_addresses() {
  info "Test 8: Add/Remove Addresses"
  incus network address-set create testAS --description "Address add/remove test"
  # Add an address using the CLI subcommand "add-addr"
  incus network address-set add-addr testAS 192.168.1.100
  if incus network address-set show testAS | grep -q "192.168.1.100"; then
    success "Address 192.168.1.100 added."
  else
    error_msg "Failed to add address 192.168.1.100."
    exit 1
  fi
  incus network address-set remove-addr testAS 192.168.1.100
  if ! incus network address-set show testAS | grep -q "192.168.1.100"; then
    success "Address 192.168.1.100 removed."
  else
    error_msg "Failed to remove address 192.168.1.100."
    exit 1
  fi
  incus network address-set delete testAS
}

# Test 9: Rename command
function test_rename() {
  info "Test 9: Rename"
  incus network address-set create testAS --description "Rename test"
  incus network address-set rename testAS testAS-renamed
  if incus network address-set ls | grep -q "testAS-renamed"; then
    success "Rename succeeded: testAS-renamed found in listing."
  else
    error_msg "Rename failed: testAS-renamed not found."
    exit 1
  fi
  incus network address-set delete testAS-renamed
}

# Test 10: Custom keys (set/unset)
function test_custom_keys() {
  info "Test 10: Custom Keys"
  incus network address-set create testAS --description "Custom keys test"
  incus network address-set set testAS user.somekey foo
  if incus network address-set show testAS | grep -q "foo"; then
    success "Custom key 'user.somekey' set to foo."
  else
    error_msg "Failed to set custom key 'user.somekey'."
    exit 1
  fi
  incus network address-set unset testAS user.somekey
  if ! incus network address-set show testAS | grep -q "foo"; then
    success "Custom key 'user.somekey' successfully unset."
  else
    error_msg "Custom key 'user.somekey' was not unset."
    exit 1
  fi
  incus network address-set delete testAS
}

# Test 11: Deletion
function test_delete() {
  info "Test 11: Deletion"
  incus network address-set create testAS --description "Delete test"
  incus network address-set delete testAS
  if incus network address-set ls | grep -q "testAS"; then
    error_msg "Address set 'testAS' still exists after deletion."
    exit 1
  else
    success "Address set 'testAS' successfully deleted."
  fi
}

function test_inner_address_set() {
  info "Test 12: Inner working of address sets and ACLs"

  # Step 1: Launch a container (named testct) from a known image (e.g. testimage)
  info "Launching container 'testct'..."
  incus launch images:debian/12 testct

  # Step 2: Wait for container to get its IP address (loop up to 10 seconds)
  info "Waiting for container IP..."
  container_ip=""
  for i in {1..10}; do
    # Adjust the field number as needed; here we assume incus list returns a CSV where the third field is the IPv4 address.
    container_ip=$(incus list testct --format csv | cut -d',' -f3 | head -n1 | cut -d' ' -f1)
    if [ -n "$container_ip" ]; then
      break
    fi
    sleep 1
  done
  if [ -z "$container_ip" ]; then
    error_msg "Failed to retrieve container IP address."
    incus delete testct --force
    exit 1
  fi
  info "Container IP is: $container_ip"

  # Step 3: Ping the container – expect success.
  info "Pinging container (should succeed)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    success "Ping succeeded."
  else
    error_msg "Ping failed, but expected success."
    incus delete testct --force
    exit 1
  fi

  # Step 4: Create an ACL (named blockping) to block ICMP (ping) traffic to the container.
  info "Creating ACL 'blockping' to ban pings to container IP..."
  incus network acl create blockping
  # Create an ingress rule that drops ICMP4 traffic destined to container_ip/32.
  incus network acl rule add blockping ingress action=drop protocol=icmp4 destination="${container_ip}/32"
  incus network set incusbr0 security.acls="blockping"
  # Wait a moment for the ACL to take effect.
  sleep 2

  # Step 5: Ping the container – expect failure.
  info "Pinging container after ACL block (should fail)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    error_msg "Ping succeeded despite ACL block; expected failure."
    incus network set incusbr0 security.acls="" 
    sleep 2
    incus network acl delete blockping
    incus delete testct --force
    exit 1
  else
    success "Ping correctly blocked by ACL."
  fi

  # Step 6: Remove the ACL.
  info "Removing ACL 'blockping'..."
  incus network set incusbr0 security.acls=""
  incus network acl delete blockping
  sleep 1

  # Step 7: Ping the container again – expect success.
  info "Pinging container after ACL removal (should succeed)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    success "Ping succeeded after ACL removal."
  else
    error_msg "Ping still blocked after ACL removal."
    incus delete testct --force
    exit 1
  fi

  # Step 8: Create an address set (named testAS) and add the container’s IP.
  info "Creating address set 'testAS' with container IP..."
  incus network address-set create testAS --description "Test Address Set for inner test"
  incus network address-set add-addr testAS "$container_ip"
  sleep 1
  if incus network address-set show testAS | grep -q "$container_ip"; then
    success "Address set 'testAS' now contains the container IP."
  else
    error_msg "Address set 'testAS' does not list the container IP."
    incus delete testct --force
    exit 1
  fi

  # Step 9: Verify that ping still works.
  info "Step 9: Pinging container with address set present (should succeed)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    success "Ping succeeded as expected."
  else
    error_msg "Ping failed unexpectedly with address set present."
    incus delete testct --force
    exit 1
  fi

  # Step 10: Create an ACL (named blockpingAS) that uses the address set reference to block pings.
  info "Step 10: Creating ACL 'blockpingAS' to ban pings using address set 'testAS'..."
  incus network acl create blockpingAS
  # Here we assume that using "$testAS" in the rule will be converted (by the driver) into references to the
  # underlying named sets (testAS_ipv4, testAS_ipv6, testAS_eth). Adjust the syntax as needed.
  incus network acl rule add blockpingAS ingress action=drop protocol=icmp4 destination="\$testAS"
  incus network set incusbr0 security.acls="blockpingAS"
  sleep 2

  # Step 11: Ping the container – expect failure because of the ACL using the address set.
  info "Step 11: Pinging container after applying ACL referencing address set (should fail)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    error_msg "Ping succeeded despite ACL block using address set; expected failure."
    incus network set incusbr0 security.acls=""
    sleep 2
    incus network acl delete blockpingAS
    incus network address-set delete testAS
    incus delete testct --force
    exit 1
  else
    success "Ping correctly blocked by ACL referencing address set."
  fi

  incus launch images:debian/12 testct2
  info "Step 12: Adding a second container and updating address set to include both IPs (both pings should fail)..."

  # Wait for container testct2 to get an IP (loop up to 10 seconds)
  container2_ip=""
  for i in {1..10}; do
    container2_ip=$(incus list testct2 --format csv | cut -d',' -f3 | head -n1 | cut -d' ' -f1)
    if [ -n "$container2_ip" ]; then
      break
    fi
    sleep 1
  done
  if [ -z "$container2_ip" ]; then
    error_msg "Failed to retrieve container testct2 IP address."
    incus delete testct2 --force
    exit 1
  fi
  info "Container testct2 IP is: $container2_ip"

  # Add the second container’s IP to the address set "testAS"
  incus network address-set add-addr testAS "$container2_ip"
  sleep 1

  # Both container IPs are now in the address set used by the ACL.
  info "Pinging container testct (should fail)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    error_msg "Ping to container testct succeeded, expected failure."
    exit 1
  else
    success "Ping to container testct correctly blocked."
  fi

  info "Pinging container testct2 (should fail)..."
  if ping -c2 "$container2_ip" > /dev/null 2>&1; then
    error_msg "Ping to container testct2 succeeded, expected failure."
    exit 1
  else
    success "Ping to container testct2 correctly blocked."
  fi

  # Step 13: Remove first container’s IP from the address set.
  info "Step 13: Removing first container IP from address set and verifying ping outcomes..."
  incus network address-set remove-addr testAS "$container_ip"
  sleep 1

  info "Pinging container testct (should succeed now)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    success "Ping to container testct succeeded as expected."
  else
    error_msg "Ping to container testct still blocked, expected success."
    exit 1
  fi

  info "Pinging container testct2 (should remain blocked)..."
  if ping -c2 "$container2_ip" > /dev/null 2>&1; then
    error_msg "Ping to container testct2 succeeded, expected failure."
    exit 1
  else
    success "Ping to container testct2 correctly remains blocked."
  fi
  incus rm --force testct2
  incus network set incusbr0 security.acls=""
  incus network acl rm blockpingAS

  # --- Step 14: Set up a TCP listener on port 7896 in container testct and block TCP 7896 via ACL using the address set ---
    #info "Step 14: Setting up TCP listener on port 7896 in container testct and applying ACL to block TCP 7896 using address set 'testAS'..."
    ## Start a TCP server in testct on port 7896 (using netcat in background)
    #container_ip6=$(incus list testct --format csv | cut -d',' -f4 | tr ' ' '\n' | head -n1)
    #incus network address-set add-addr testAS "$container_ip"
    #incus network address-set add-addr testAS "$container_ip6"
    #incus exec testct -- sh -c "apt install netcat-openbsd -y"
    #incus exec testct -- sh -c "nohup nc -l -p 7896 >/dev/null 2>&1 &"
    #sleep 1
  #
    ## Create ACL 'blocktcp7896' to block TCP traffic destined to port 7896 via address set "testAS".
    #info "Creating ACL 'blocktcp7896' to block TCP traffic to port 7896 via address set 'testAS'..."
    #incus network acl create blocktcp7896
    ## Here, using "$testAS" should be converted by the driver into separate rules referencing testAS_ipv4 and testAS_ipv6.
    #incus network acl rule add blocktcp7896 ingress action=drop protocol=tcp destination_port="7896" destination="\$testAS"
    #incus network set incusbr0 security.acls="blocktcp7896"
    #sleep 2
  #
    #info "Testing TCP connection to port 7896 on container testct (should fail)..."
    #if nc -z -w 5 "$(incus list testct --format csv | cut -d',' -f3 | head -n1 | cut -d' ' -f1)" 7896; then
    #  error_msg "TCP connection to port 7896 on testct succeeded despite ACL block; expected failure."
    #  incus network set incusbr0 security.acls=""
    #  incus network acl delete blocktcp7896
    #  incus delete testct --force
    #  exit 1
    #else
    #  success "TCP connection to port 7896 on testct correctly blocked by ACL."
    #fi
  #
    ## Also test IPv6 TCP connection if available.
    #
    #if [ -n "$container_ip6" ]; then
    #  info "Testing TCP connection to port 7896 on container testct IPv6 (should fail)..."
    #  incus exec testct -- sh -c "nohup nc -6 -l -p 7896 >/dev/null 2>&1 &"
    #  if nc -z -w 5 "$container_ip6" 7896; then
    #    error_msg "TCP connection to port 7896 on testct IPv6 succeeded despite ACL block; expected failure."
    #    incus network set incusbr0 security.acls=""
    #    incus network acl delete blocktcp7896
    #    incus delete testct --force
    #    exit 1
    #  else
    #    success "TCP connection to port 7896 on testct IPv6 correctly blocked by ACL."
    #  fi
    #fi

  # --- Step 15: Remove testct's IP from the address set and verify TCP connectivity ---
    #info "Step 15: Removing testct's IP from address set 'testAS' and verifying TCP connectivity..."
    #incus network address-set remove-addr testAS "$container_ip6"
    #sleep 1
  #
    #info "Testing TCP connection to port 7896 on container testct (should succeed now)..."
    #incus exec testct -- sh -c "nohup nc -6 -l -p 7896 >/dev/null 2>&1 &"
    #info "Pinging testct at $container_ip6"
    #if nc -z -w 5 "$container_ip6" 7896; then
    #  success "TCP connection to port 7896 on testct succeeded as expected after removal from address set."
    #else
    #  error_msg "TCP connection to port 7896 on testct still blocked, expected success."
    #  incus network set incusbr0 security.acls=""
    #  incus network acl delete blocktcp7896
    #  #incus network address-set delete testAS
    #  #incus delete testct --force
    #  exit 1
    #fi
    #incus network set incusbr0 security.acls=""
    #incus network acl delete blocktcp7896
    #incus network address-set delete testAS

  # Step 16: Remove all addresses from the address set and add a CIDR representing the container's network.
  info "Step 16: Updating address set 'testAS' with container network CIDR..."
  incus network address-set rm testAS 
  sleep 3
  # Derive the container’s network CIDR from its IPv4 address.
  # For example, if container_ip is 10.158.174.140, assume subnet 10.158.174.0/24.
  subnet=$(echo "$container_ip" | awk -F. '{print $1"."$2"."$3".0/24"}')
  info "Adding subnet $subnet to address set 'testAS'..."
  incus network address-set create testAS 
  incus network address-set add-addr testAS "$subnet"
  sleep 3
  
  info "Creating ACL 'blockpingAS-cidr' to block pings using the CIDR in address set 'testAS'..."
  incus network acl create blockpingAS-cidr
  incus network acl rule add blockpingAS-cidr ingress action=drop protocol=icmp4 destination="\$testAS"
  incus network set incusbr0 security.acls="blockpingAS-cidr"
  sleep 3
  
  info "Pinging container (should fail due to CIDR ACL block)..."
  if ping -c2 "$container_ip" > /dev/null 2>&1; then
    error_msg "Ping succeeded despite ACL block using CIDR in address set; expected failure."
    incus network set incusbr0 security.acls=""
    sleep 3
    incus network acl delete blockpingAS-cidr
    incus network address-set delete testAS
    incus delete testct --force
    exit 1
  else
    success "Ping correctly blocked by ACL referencing CIDR in address set."
  fi

  # Step 17: Clean up: remove ACL and address set, then delete the container.
  info "Cleaning up: Removing ACL 'blockpingAS' and address set 'testAS'..."
  incus network set incusbr0 security.acls=""
  incus network acl delete blockpingAS-cidr
  incus network address-set delete testAS
  incus delete testct --force
  success "Clean up complete."
}

# --- Run all tests ---
info "Starting network address set tests..."
test_creation_cli
success "TEST 1 OK"
test_creation_project
success "TEST 2 OK"
test_creation_stdin
success "TEST 3 OK"
test_listing
success "TEST 4 OK"
test_show
success "TEST 5 OK"
test_edit
success "TEST 6 OK"
test_patch
success "TEST 7 OK"
test_add_remove_addresses
success "TEST 8 OK"
test_rename
success "TEST 9 OK"
test_custom_keys
success "TEST 10 OK"
test_delete
success "TEST 11 OK"
test_inner_address_set
success "TEST 12 OK"

success "All network address set tests completed successfully."
