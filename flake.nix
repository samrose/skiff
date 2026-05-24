{
  description = "Skiff — open-source npm hermetic packager";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            # Go toolchain (pinned via nixpkgs revision in flake.lock).
            go_1_26
            golangci-lint
            gopls
            gotools

            # Infra CLIs the editor-side workflow needs.
            temporal-cli
            clickhouse
            awscli2
            jq
            xz

            # Convenience.
            git
          ];

          shellHook = ''
            export GOFLAGS="-mod=mod"
            export CGO_ENABLED=0
            echo "skiff dev shell — $(go version)"
            echo "  pipeline runtime lives in docker-compose; this shell is for editing + go test."
          '';
        };

        # The npm-package packager derivation lands in milestone 5 (nix/packager.nix).
        # Re-add `packages.packager = pkgs.callPackage ./nix/packager.nix { };`
        # at that point.
      });
}
