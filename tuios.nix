{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.0.26";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-0hxj6EUTCV7R59XJheHj9PR/oWQH+2uzYOPhVQWa0hU=";
}
