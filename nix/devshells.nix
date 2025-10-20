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
              inherit (pkgs)
                go
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
