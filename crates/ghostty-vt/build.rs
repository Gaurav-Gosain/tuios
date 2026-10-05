//! Links the static libghostty-vt archive.
//!
//! The archive must come from the same ghostty commit as `src/ffi.rs`, which
//! bindgen generated from that commit's headers. tuios pins the commit in
//! `scripts/ghostty-lib.sh` and builds the archive into `.ghostty-vt/native`.
//! Point `GHOSTTY_VT_DIR` at such a directory, or create the symlink
//! `<workspace>/.ghostty-vt/native` to it.
use std::env;
use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-env-changed=GHOSTTY_VT_DIR");
    let manifest = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let dir = env::var_os("GHOSTTY_VT_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| manifest.join("../../.ghostty-vt/native"));
    let lib = dir.join("lib");
    if !lib.join("libghostty-vt.a").exists() {
        panic!(
            "libghostty-vt.a not found in {}. Set GHOSTTY_VT_DIR to a directory with lib/libghostty-vt.a (tuios: scripts/ghostty-lib.sh).",
            lib.display()
        );
    }
    println!("cargo:rerun-if-changed={}", lib.join("libghostty-vt.a").display());
    println!("cargo:rustc-link-search=native={}", lib.display());
    println!("cargo:rustc-link-lib=static=ghostty-vt");
}
