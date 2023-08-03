test_template() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  # Import a template which only triggers on create
  deps/import-busybox --alias template-test --template create
  inc init template-test template

  # Confirm that template application is delayed to first start
  ! inc file pull template/template - || false

  # Validate that the template is applied
  inc start template
  inc file pull template/template - | grep "^name: template$"

  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
    inc stop template --force
  fi

  # Confirm it's not applied on copies
  inc copy template template1
  inc file pull template1/template - | grep "^name: template$"

  # Cleanup
  inc image delete template-test
  inc delete template template1 --force


  # Import a template which only triggers on copy
  deps/import-busybox --alias template-test --template copy
  inc launch template-test template

  # Confirm that the template doesn't trigger on create
  ! inc file pull template/template - || false
  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
    inc stop template --force
  fi

  # Copy the container
  inc copy template template1

  # Confirm that template application is delayed to first start
  ! inc file pull template1/template - || false

  # Validate that the template is applied
  inc start template1
  inc file pull template1/template - | grep "^name: template1$"

  # Cleanup
  inc image delete template-test
  inc delete template template1 --force


  # Import a template which only triggers on start
  deps/import-busybox --alias template-test --template start
  inc launch template-test template

  # Validate that the template is applied
  inc file pull template/template - | grep "^name: template$"
  inc file pull template/template - | grep "^user.foo: _unset_$"

  # Confirm it's re-run at every start
  inc config set template user.foo bar
  inc restart template --force
  inc file pull template/template - | grep "^user.foo: bar$"

  # Cleanup
  inc image delete template-test
  inc delete template --force


  # Import a template which triggers on both create and copy
  deps/import-busybox --alias template-test --template create,copy
  inc init template-test template

  # Confirm that template application is delayed to first start
  ! inc file pull template/template - || false

  # Validate that the template is applied
  inc start template
  inc file pull template/template - | grep "^name: template$"

  # Confirm it's also applied on copies
  inc copy template template1
  inc start template1
  inc file pull template1/template - | grep "^name: template1$"
  inc file pull template1/template - | grep "^user.foo: _unset_$"

  # But doesn't change on restart
  inc config set template1 user.foo bar
  inc restart template1 --force
  inc file pull template1/template - | grep "^user.foo: _unset_$"

  # Cleanup
  inc image delete template-test
  inc delete template template1 --force
}
