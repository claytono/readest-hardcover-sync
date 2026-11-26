{
  description = "readest-hardcover-sync development environment";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-darwin" "aarch64-linux" ];
      forEachSupportedSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
        pkgs = import nixpkgs { inherit system; };
      });

      # One Go toolchain for the dev shell and every Go build. Renovate bumps
      # the attribute below; nixpkgs' default go lags new Go releases.
      goFor = pkgs: pkgs.go_1_27;
      buildGoModuleFor = pkgs: pkgs.buildGoModule.override { go = goFor pkgs; };

      # go-test-coverage package (not in nixpkgs)
      mkGoTestCoverage = pkgs: (buildGoModuleFor pkgs) rec {
        pname = "go-test-coverage";
        version = "2.23.0";

        src = pkgs.fetchFromGitHub {
          owner = "vladopajic";
          repo = "go-test-coverage";
          rev = "v${version}";
          name = "go-test-coverage-${version}-source";
          hash = "sha256-UCsgO041+1kFq2dkplYtzd2ml9BuAZR44UAbEIaBSQQ=";
        };

        vendorHash = "sha256-bhRprQCWk69p+PknwsKRHTAKnKoLO7nnaWLfmFu/0ZA=";

        # The vendored modules' store path depends only on their name and
        # vendorHash, so after a version bump a cached copy built from the old
        # source can satisfy a stale vendorHash and hide the mismatch. Naming
        # them after the source hash gives every new source a path that has
        # never been built, so a stale vendorHash fails and gets fixed.
        overrideModAttrs = _: prev: {
          name = "${prev.name}-${builtins.substring 0 8 (builtins.hashString "sha256" src.outputHash)}";
        };

        passthru.refreshSourceHash = true;

        # Skip tests - upstream has integration tests requiring GitHub credentials
        # and nil pointer bugs with newer Go versions
        doCheck = false;
        checkPhase = "";

        meta = {
          description = "Tool to report issues when test coverage falls below threshold";
          homepage = "https://github.com/vladopajic/go-test-coverage";
        };
      };
    in
    {
      devShells = forEachSupportedSystem ({ pkgs }: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            (goFor pkgs)
            golangci-lint
            go-task
            pre-commit
            (mkGoTestCoverage pkgs)
          ];
        };
      });
    };
}
