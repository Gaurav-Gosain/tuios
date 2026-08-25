{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.7.0";

  src = ./.;

  # Only build the main binary; e2e/ and other packages carry
  # build tags (//go:build e2e) and are not standalone binaries.
  subPackages = [ "cmd/tuios" "cmd/tuios-web" ];

  # Allow Go to download the required toolchain version if the
  # nixpkgs Go is older than what go.mod specifies.
  env.GOTOOLCHAIN = "auto";

  # This has to be updated each time dependencies are updated.
  vendorHash = "sha256-p1zm7qI5Xc9TBN2fcQuSH9N+sP0zYUr1xA9Idlt4StE=";
}
