{
  description = "Incus is a modern, secure and powerful system container and virtual machine manager.";

  inputs = {
    nixpkgs.url = "nixpkgs/nixos-unstable";
    flake-parts.url = "flake-parts";
  };

  outputs = { self, flake-parts, nixpkgs, ... }@inputs:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
      ];
      perSystem = { pkgs, ... }: {
        devShells.default = pkgs.callPackage ./.flake/shell.nix { };
        formatter = pkgs.nixpkgs-fmt;
        checks = {
          ceph = pkgs.testers.runNixOSTest {
            name = "test-incus-ceph";
            nodes.host = ./.flake/tests-runner.nix;
            testScript = builtins.readFile ./.flake/test-incus-ceph.py;
          };
        };
      };
    };
}
