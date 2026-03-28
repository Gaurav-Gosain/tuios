{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.7.0";

  src = ./.;

  # Allow Go to download the required toolchain version if the
  # nixpkgs Go is older than what go.mod specifies.
  env.GOTOOLCHAIN = "auto";

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash to get the correct hash from a failed build.
  vendorHash = pkgs.lib.fakeHash;
}
