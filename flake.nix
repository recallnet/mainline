{
  description = "mainline packaging outputs";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version =
          if self ? shortRev then self.shortRev
          else if self ? dirtyShortRev then "${self.dirtyShortRev}-dirty"
          else "dev";
        mainlinePkg = pkgs.callPackage ./nix/package.nix {
          inherit version;
          src = ./.;
        };
      in {
        packages = {
          default = mainlinePkg;
          mainline = mainlinePkg;
        };

        apps = {
          default = {
            type = "app";
            program = "${mainlinePkg}/bin/mainline";
          };
          mainline = {
            type = "app";
            program = "${mainlinePkg}/bin/mainline";
          };
          mq = {
            type = "app";
            program = "${mainlinePkg}/bin/mq";
          };
          mainlined = {
            type = "app";
            program = "${mainlinePkg}/bin/mainlined";
          };
        };
      });
}
