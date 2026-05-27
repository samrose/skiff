{
  description = "Skiff — open-source npm hermetic packager";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    git-hooks = {
      url = "github:cachix/git-hooks.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      treefmt-nix,
      git-hooks,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };

        treefmtEval = treefmt-nix.lib.evalModule pkgs ./treefmt.nix;

        preCommitCheck = git-hooks.lib.${system}.run {
          src = ./.;
          hooks = {
            # Formatting — runs treefmt over Go, Nix, YAML, JSON, etc.
            treefmt = {
              enable = true;
              package = treefmtEval.config.build.wrapper;
              pass_filenames = false;
            };

            # Go static analysis — vet, staticcheck, ineffassign, etc., per .golangci.yml.
            golangci-lint = {
              enable = true;
              name = "golangci-lint";
              entry = "nix develop -c golangci-lint run --fix=false";
              language = "system";
              pass_filenames = false;
              stages = [ "pre-commit" ];
            };

            # Fast unit tests — `-short` skips long-running cases; SKIFF_INTEGRATION
            # is intentionally unset so integration tests against the live stack are
            # skipped here. Those run in CI.
            go-test-short = {
              enable = true;
              name = "go test -short";
              entry = "nix develop -c go test -short -count=1 ./...";
              language = "system";
              pass_filenames = false;
              stages = [ "pre-commit" ];
            };

            # go mod tidy diff — fails if go.mod / go.sum aren't tidy.
            go-mod-tidy = {
              enable = true;
              name = "go mod tidy (diff)";
              entry = "${./scripts/check-go-mod-tidy.sh}";
              language = "system";
              pass_filenames = false;
              stages = [ "pre-commit" ];
            };
          };
        };
      in
      {
        devShells.default = pkgs.mkShell {
          inherit (preCommitCheck) shellHook;

          packages =
            (with pkgs; [
              # Go toolchain (pinned via nixpkgs revision in flake.lock).
              go_1_26
              golangci-lint
              gopls
              gotools
              gofumpt

              # Infra CLIs the editor-side workflow needs.
              temporal-cli
              clickhouse
              awscli2
              jq
              xz

              # Convenience.
              git
            ])
            ++ preCommitCheck.enabledPackages;
        };

        # `nix flake check` runs the pre-commit check.
        checks.pre-commit = preCommitCheck;

        # `nix fmt` runs treefmt over the repo.
        formatter = treefmtEval.config.build.wrapper;

        # M5 reintroduces `packages.packager = pkgs.callPackage ./nix/packager.nix { };`
        # once nix/packager.nix exists.
      }
    );
}
