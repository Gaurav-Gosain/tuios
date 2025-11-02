{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.2.1";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-9FXltae1oNiciUY3EjS3+xwtmrB25TP4ajeo1MH1L7k=";
}
