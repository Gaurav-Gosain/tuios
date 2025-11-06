{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.3.3";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-tu8GXE/wMq2i61gTlgdbfL38ehVppa/fz1WVXrsX+vk=";
}
