{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.4.0";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-nrQO+X8L066vVhHMOU+J5g9y4qykOLuK3aON/VNdLjI=";
}
