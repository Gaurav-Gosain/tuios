{ pkgs, ... }:

pkgs.buildGoModule rec {
  pname = "tuios";
  version = "v0.10.1";

  src = ./.;

  # Only build the main binaries; e2e/ and other packages carry
  # build tags (//go:build e2e) and are not standalone binaries.
  subPackages = [
    "cmd/tuios"
    "cmd/tuios-web"
  ];

  # The build sandbox has no network, so Go cannot fetch a newer toolchain
  # there: the nixpkgs Go must be at least the go line in go.mod (1.26.6).
  # The pinned nixpkgs has 1.26.7. "local" makes a Go that is too old fail
  # with that plain message instead of a failed download.
  env.GOTOOLCHAIN = "local";

  # The checks run the cmd/tuios tests, and some of them call git to check
  # branch names.
  nativeCheckInputs = [ pkgs.git ];

  # tuios --version reports the release rather than "dev". Release archives
  # spell the version without the v, and so does this.
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${pkgs.lib.removePrefix "v" version}"
    "-X main.builtBy=nix"
  ];

  # go.work makes the app and the Go libraries in this repository (such as
  # progstatus/) one workspace, so this build uses the library folders, as CI
  # does. go mod vendor refuses a workspace, so the module derivation runs go
  # work vendor. The e2e suite leaves the workspace first, so its test
  # dependencies stay out of the vendor directory and out of vendorHash.
  postPatch = ''
    go work edit -dropuse=./e2e/tui
  '';
  modBuildPhase = ''
    runHook preBuild
    go work vendor
    runHook postBuild
  '';

  # This has to be updated each time dependencies are updated.
  vendorHash = "sha256-cB7M6sNt/p2EEKbhm20Xb1+nEkIzNVIA2XPOPQgfuD0=";
}
