{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.0.22";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-h3MRdo03okkPBDdB8/RCVaKhOGdGQP3tPkXI6V6Hn2g=";
}
