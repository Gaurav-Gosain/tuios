{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.4.1";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-t4U0o1hKoCGu52ad/N3wfoOHl8pNYT0/zcsRaZ/fefA=";
}
