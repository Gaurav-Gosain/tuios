{ inputs, ... }:
{
  imports = [
    inputs.flake-root.flakeModule
    inputs.treefmt-nix.flakeModule
  ];
  perSystem =
    { config, pkgs, ... }:
    {
      treefmt.config = {
        package = pkgs.treefmt;
        inherit (config.flake-root) projectRootFile;

        programs = {
          # Nix
          nixfmt.enable = true;
          nixfmt.package = pkgs.nixfmt-rfc-style; # nix standard formatter according to rfc 166
          deadnix.enable = true; # removes dead nix code https://github.com/astro/deadnix
          statix.enable = true; # prevents use of nix anti-patterns https://github.com/nerdypepper/statix
          # Bash
          shellcheck.enable = true; # lints shell scripts https://github.com/koalaman/shellcheck
          # Golang
          gofmt.enable = true;
        };

        settings.global.excludes = [
          ".git/*"
          ".github/*"
          ".direnv/*"
          ".envrc"
        ];
      };

      formatter = config.treefmt.build.wrapper;
    };
}
