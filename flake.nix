{
  description = "Simple Vocabulary — lightweight Go vocab trainer";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        vocab = pkgs.buildGoModule {
          pname = "simple-vocabulary";
          version = "0.1.0";
          src = pkgs.lib.cleanSource ./.;
          # No third-party Go deps.
          vendorHash = null;
          subPackages = [ "cmd/server" "cmd/convertdict" ];
          ldflags = [ "-s" "-w" ];
          # ship dictionary + web next to the binary for nix run convenience
          postInstall = ''
            mkdir -p $out/share/simple-vocabulary
            cp -r data web $out/share/simple-vocabulary/
          '';
        };
      in {
        packages.default = vocab;
        packages.server = vocab;

        apps.default = {
          type = "app";
          program = "${pkgs.writeShellScript "simple-vocabulary" ''
            export APP_ROOT="${vocab}/share/simple-vocabulary"
            export WEB_DIR="$APP_ROOT/web"
            export DICT_PATH="$APP_ROOT/data/spa-deu.json"
            export CONJ_PATH="$APP_ROOT/data/spa-conj.json"
            exec ${vocab}/bin/server "$@"
          ''}";
        };

        apps.convertdict = {
          type = "app";
          program = "${vocab}/bin/convertdict";
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            golangci-lint
            docker-compose
            curl
            jq
            sqlite
          ];

          shellHook = ''
            export APP_ROOT="$PWD"
            export WEB_DIR="$PWD/web"
            export DICT_PATH="$PWD/data/spa-deu.json"
            export CONJ_PATH="$PWD/data/spa-conj.json"
            export PORT="''${PORT:-8080}"
            echo "simple-vocabulary dev shell"
            echo "  go run ./cmd/server          # http://localhost:$PORT"
            echo "  docker compose up --build"
          '';
        };
      });
}
