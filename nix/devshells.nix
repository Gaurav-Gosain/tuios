{ inputs, lib, ... }:
{
  imports = [ inputs.devshell.flakeModule ];
  perSystem =
    {
      config,
      pkgs,
      inputs',
      ...
    }:
    {
      devshells.default = {
        devshell = {
          name = "Golang devshell";
          meta.description = "Golang development environment";
          packages =
            builtins.attrValues {
              # The toolchain line in go.mod. The default go in nixpkgs is
              # still 1.26.
              go = pkgs.go_1_27;
              inherit (pkgs)
                gomodifytags
                gopls
                gore
                gosec
                gotests
                gotools
                go-tools
                golangci-lint
                ;
            }
            ++ [
              inputs'.nix-fast-build.packages.default
              config.treefmt.build.wrapper
            ]
            ++ lib.attrValues config.treefmt.build.programs;
        };

        commands = [
          {
            help = "Check golang vulnerabilities";
            name = "go-checksec";
            command = "gosec ./...";
          }
          {
            help = "Update go dependencies";
            name = "go-update";
            command = "go get -u ./... && go mod tidy && go mod vendor";
          }
          {
            help = "golang linter";
            package = "golangci-lint";
            category = "linters";
          }
        ];
      };
    };
}
