test_template() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  # Import a template which only triggers on create
  deps/import-busybox --alias template-test --template create
  incus init template-test template

  # Confirm that template application is delayed to first start
  ! incus file pull template/template - || false

  # Validate that the template is applied
  incus start template
  incus file pull template/template - | grep "^name: template$"

  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
    incus stop template --force
  fi

  # Confirm it's not applied on copies
  incus copy template template1
  incus file pull template1/template - | grep "^name: template$"

  # Cleanup
  incus image delete template-test
  incus delete template template1 --force


  # Import a template which only triggers on copy
  deps/import-busybox --alias template-test --template copy
  incus launch template-test template

  # Confirm that the template doesn't trigger on create
  ! incus file pull template/template - || false
  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
    incus stop template --force
  fi

  # Copy the container
  incus copy template template1

  # Confirm that template application is delayed to first start
  ! incus file pull template1/template - || false

  # Validate that the template is applied
  incus start template1
  incus file pull template1/template - | grep "^name: template1$"

  # Cleanup
  incus image delete template-test
  incus delete template template1 --force


  # Import a template which only triggers on start
  deps/import-busybox --alias template-test --template start
  incus launch template-test template

  # Validate that the template is applied
  incus file pull template/template - | grep "^name: template$"
  incus file pull template/template - | grep "^user.foo: _unset_$"

  # Confirm it's re-run at every start
  incus config set template user.foo bar
  incus restart template --force
  incus file pull template/template - | grep "^user.foo: bar$"

  # Cleanup
  incus image delete template-test
  incus delete template --force


  # Import a template which triggers on both create and copy
  deps/import-busybox --alias template-test --template create,copy
  incus init template-test template

  # Confirm that template application is delayed to first start
  ! incus file pull template/template - || false

  # Validate that the template is applied
  incus start template
  incus file pull template/template - | grep "^name: template$"

  # Confirm it's also applied on copies
  incus copy template template1
  incus start template1
  incus file pull template1/template - | grep "^name: template1$"
  incus file pull template1/template - | grep "^user.foo: _unset_$"

  # But doesn't change on restart
  incus config set template1 user.foo bar
  incus restart template1 --force
  incus file pull template1/template - | grep "^user.foo: _unset_$"

  # Cleanup
  incus image delete template-test
  incus delete template template1 --force
}
