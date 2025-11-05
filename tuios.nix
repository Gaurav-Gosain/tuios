{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.3.0";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-OPN1wzW8wmJLyVdtMtZq4jHpUv3ye/xtBVtWbmyqF3M=";
}
