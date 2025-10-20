{ inputs, ... }:
{
  imports = [ inputs.git-hooks-nix.flakeModule ];
  perSystem =
    {
      config,
      pkgs,
      self',
      lib,
      ...
    }:
    {
      checks = {
        reuse = pkgs.runCommandLocal "reuse-lint" { buildInputs = [ pkgs.reuse ]; } ''
          cd ${../.}
          reuse lint
          touch $out
        '';
      }
      // (lib.mapAttrs' (n: lib.nameValuePair "package-${n}") self'.packages);

      pre-commit = {
        settings = {
          hooks = {
            gofmt.enable = true;
            govet.enable = true;
            golangci-lint.enable = true;
            gotest.enable = true;
            treefmt = {
              enable = true;
              package = config.treefmt.build.wrapper;
            };
          };
        };

      };
    };
}
