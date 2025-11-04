{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.2.2";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-T3dy2qOpyqh0gB+TYpqiOq3cdKboR+74QBIgq9SXGTE=";
}
