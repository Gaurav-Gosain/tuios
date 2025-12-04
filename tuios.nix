{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.4.3";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-uhqa850dHRHNZLXUMGg9Hb8skEY/5CrGmxSmnBytW/s=";
}
