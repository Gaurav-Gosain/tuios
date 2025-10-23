{ pkgs, ... }:

pkgs.buildGoModule {
  pname = "tuios";
  version = "v0.0.17";

  src = ./.;

  # This has to be updated each time dependencies are updated.
  # Use pkgs.lib.fakeHash
  vendorHash = "sha256-yF7sYqAvrajrQllJXU0VZwxuXtSJZPuCl/cvZTD2WJA=";
}
