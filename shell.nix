let
  pkgs = import <nixpkgs> { };
in
pkgs.callPackage ./.flake/shell.nix { }
