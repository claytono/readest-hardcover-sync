{
  description = "readest-hardcover-sync development environment";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-darwin" "x86_64-darwin" "aarch64-linux" ];
      forEachSupportedSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
        pkgs = import nixpkgs { inherit system; };
      });

      # go-test-coverage package (not in nixpkgs)
      mkGoTestCoverage = pkgs: pkgs.buildGoModule rec {
        pname = "go-test-coverage";
        version = "2.18.3";

        src = pkgs.fetchFromGitHub {
          owner = "vladopajic";
          repo = "go-test-coverage";
          rev = "v${version}";
          hash = "sha256-8KPnufCLGR3beBjTJSGSkxZd+m3r1pYDtTBLhG/eSEg=";
        };

        vendorHash = "sha256-iJ3VFnzPYd0ovyK/QdCDolh5p8fe/aXulnHxAia5UuE=";

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
            go_1_26
            golangci-lint
            go-task
            pre-commit
            (mkGoTestCoverage pkgs)
          ];
        };
      });
    };
}
