{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.7.0";

  src = ./.;

  # Only build the main binaries; e2e/ and other packages carry
  # build tags (//go:build e2e) and are not standalone binaries.
  subPackages = [
    "cmd/tuios"
    "cmd/tuios-web"
  ];

  # Allow Go to download the required toolchain version if the
  # nixpkgs Go is older than what go.mod specifies.
  env.GOTOOLCHAIN = "auto";

  # This has to be updated each time dependencies are updated.
  vendorHash = "sha256-Wp2SE3I1rJyR2XmEWXYDU0Q91Hl34XySzAgawWl5Y9I=";
}
