get_static_bpf_tool() {
  if [ -e "${INCUS_BPFTOOL_STATIC_BINARY:-}" ]; then
    cp "$INCUS_BPFTOOL_STATIC_BINARY" "${TEST_DIR}/bpftool"
  else
    bpftool_dir="$(mktemp -d -p "${TEST_DIR}" bpftool-XXX)"
    git clone --depth=1 --revision=53c1852920c8a8f8ccedb7a64e3d9852949792c7 --recurse-submodules https://github.com/libbpf/bpftool "${bpftool_dir}"

    cd "${bpftool_dir}/src" || return 1
    EXTRA_LDFLAGS=-static make
    cp -L bpftool "${TEST_DIR}"
    cd - || return 1
  fi
}

test_container_bpf_token() {
  ensure_import_testimage

  get_static_bpf_tool

  incus launch testimage foo
  incus file push "${TEST_DIR}/bpftool" foo/bin/bpftool
  incus exec foo -- chmod +x /bin/bpftool
  incus stop foo

  echo "all delegates configured"
  bpf_token_test "" "map_create,prog_attach" "hash,array" "socket_filter,xdp,kprobe" "cgroup_inet_ingress,sk_skb_stream_parser"

  echo "all delegates configured to any"
  bpf_token_test "" "any" "any" "any" "any"

  echo "only delegate map"
  bpf_token_test "" "map_create" "" "" ""

  echo "only delegate cmd"
  bpf_token_test "" "" "hash" "" ""

  echo "only delegate prog"
  bpf_token_test "" "" "" "socket_filter" ""

  echo "only delegate attach"
  bpf_token_test "" "" "" "" "cgroup_inet_ingress"

  echo "custom mount path"
  bpf_token_test "/mnt" "map_create" "" "" ""

  incus delete -f foo
}

bpf_token_test() {
  path="$1"
  cmds="$2"
  maps="$3"
  progs="$4"
  attachs="$5"

  incus config set foo security.bpffs.path="$path"
  incus config set foo security.bpffs.delegate_cmds="$cmds"
  incus config set foo security.bpffs.delegate_maps="$maps"
  incus config set foo security.bpffs.delegate_progs="$progs"
  incus config set foo security.bpffs.delegate_attachs="$attachs"

  incus start foo
  bpftool_output="$(incus exec foo -- /bin/bpftool --json token list  | jq --sort-keys)"
  incus stop foo
  expected_path="${path:-/sys/fs/bpf}"
  expected_cmds="$(echo "$cmds" | tr ',' '\n' | sort)"
  expected_maps="$(echo "$maps" | tr ',' '\n' | sort)"
  expected_progs="$(echo "$progs" | tr ',' '\n' | sort)"
  expected_attachs="$(echo "$attachs" | tr ',' '\n' | sort)"

  got_path="$(echo "$bpftool_output" | jq -r '.[0].token_info')"
  got_cmds="$(echo "$bpftool_output" | jq -r '.[0].allowed_cmds.[]' | sort)"
  got_maps="$(echo "$bpftool_output" | jq -r '.[0].allowed_maps.[]' | sort)"
  got_progs="$(echo "$bpftool_output" | jq -r '.[0].allowed_progs.[]' | sort)"
  got_attachs="$(echo "$bpftool_output" | jq -r '.[0].allowed_attachs.[]' | sort)"

  test "$expected_path" = "$got_path"
  test "$expected_cmds" = "$got_cmds"
  test "$expected_maps" = "$got_maps"
  test "$expected_progs" = "$got_progs"
  test "$expected_attachs" = "$got_attachs"
}
