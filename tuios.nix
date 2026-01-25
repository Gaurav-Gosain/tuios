{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.6.0";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-kDZRT/Ua+SaxyZ6RI9ZY2tqBgQBWo755fvQVRupBsUc=";
}
