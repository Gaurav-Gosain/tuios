{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.0.16";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-4w+XFFCrr/XakF3KTA6oKiseEDtGrgUAXaeaI6qWdys=";
}
