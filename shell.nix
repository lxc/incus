let
  pkgs = import <nixpkgs> { inherit overlays; };
  _xgettext-go =
    { buildGoModule
    , fetchFromGitHub
    , gettext
    }: buildGoModule rec {
      pname = "xgettext-go";
      version = "2.57.1";

      src = fetchFromGitHub {
        owner = "canonical";
        repo = "snapd";
        rev = version;
        hash = "sha256-icPEvK8jHuJO38q1n4sabWvdgt9tB5b5Lh5/QYjRBBQ=";
      };

      vendorHash = "sha256-e1QFZIleBVyNB0iPecfrPOg829EYD7d3KMHIrOYnA74=";
      subPackages = [
        "i18n/xgettext-go"
      ];
    };
  overlays = [
    (final: prev: {
      xgettext-go = final.callPackage _xgettext-go { };
    })
  ];

in
pkgs.mkShell {
  packages = with pkgs; [
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
  ];
  inputsFrom = [
    pkgs.incus
  ];
}
