{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.0.8";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-o1hX6CEG0RgeLfrYMANpwIQjsvuW8vz4irzQD2GqAYE=";
}
