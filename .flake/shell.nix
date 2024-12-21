{
  mkShell
, callPackage
, go
, golangci-lint
, gopls
, debianutils
, go-licenses
, go-swagger
, gettext
, shellcheck
, xgettext-go ? (callPackage ./xgettext-go.nix { })
, python3
, squashfsTools
, incus
}: mkShell {
  packages = [
    # dev environment
    go
    golangci-lint
    gopls

    # static-analysis
    debianutils
    go-licenses
    go-swagger
    gettext
    shellcheck
    xgettext-go
    (python3.withPackages (pyPkgs: with pyPkgs; [
      flake8
    ]))

    # running
    squashfsTools
  ];
  inputsFrom = [
    incus
  ];
}
