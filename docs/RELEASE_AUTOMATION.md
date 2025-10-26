# Release Automation Guide

This document explains the automated release process for TUIOS, including Homebrew tap and Nix package updates.

## Overview

When you create a new release tag, the following happens automatically:

1. **GoReleaser** builds binaries for all platforms
2. **AUR package** is updated with the new version
3. **Homebrew formula** is published to your tap
4. **Nix package** is updated with the new version and vendorHash
5. **GitHub release** is created with all assets

## Setup Requirements

### 1. Create Homebrew Tap Repository

First, create a new GitHub repository for your Homebrew tap:

1. Go to https://github.com/new
2. Name it `homebrew-tap` (required naming convention)
3. Make it **public** (Homebrew requires public taps)
4. Initialize with a README
5. Clone it locally (optional, GoReleaser will manage it)

**Repository URL**: `https://github.com/Gaurav-Gosain/homebrew-tap`

### 2. Create GitHub Personal Access Token

You need a GitHub token with `repo` permissions for the Homebrew tap:

1. Go to https://github.com/settings/tokens/new
2. Name: `TUIOS Homebrew Tap Token`
3. Expiration: Choose `No expiration` or set a long expiration
4. Select scopes:
   -  `repo` (Full control of private repositories)
     - This includes `public_repo` for public repositories
5. Click **Generate token**
6. **Copy the token immediately** (you won't see it again)

### 3. Add GitHub Secret

Add the token as a repository secret:

1. Go to https://github.com/Gaurav-Gosain/tuios/settings/secrets/actions
2. Click **New repository secret**
3. Name: `HOMEBREW_TAP_GITHUB_TOKEN`
4. Value: Paste the token you created
5. Click **Add secret**

### 4. Verify Existing Secrets

Make sure you also have these secrets configured:

- `AUR_SSH_KEY` - For AUR package publishing (already configured)

## How It Works

### Homebrew Release Flow

When you tag a release:

```bash
git tag v0.0.24
git push origin v0.0.24
```

The release workflow (`release.yml`) will:

1. Run GoReleaser
2. GoReleaser builds all binaries
3. GoReleaser creates a Homebrew formula in `homebrew-tap/Formula/tuios.rb`
4. GoReleaser commits and pushes to `Gaurav-Gosain/homebrew-tap`

Users can then install with:

```bash
brew tap Gaurav-Gosain/tap
brew install tuios
```

### Nix Package Update Flow

After the release is published, the `update-nix.yml` workflow runs:

1. Extracts the version from the release tag
2. Updates `tuios.nix` with the new version
3. Builds with a fake hash to get the real vendorHash
4. Updates `tuios.nix` with the correct hash
5. Verifies the build succeeds
6. Commits and pushes the changes to `main`

This happens automatically, no manual intervention needed!

## Testing

### Test Homebrew Formula Locally

Before releasing, you can test the formula generation:

```bash
goreleaser release --snapshot --clean --skip=publish
```

This will:
- Build all binaries
- Generate the Homebrew formula
- NOT publish anything

Check `.goreleaser/dist/` for the generated files.

### Test Nix Build

After the workflow updates `tuios.nix`, verify it works:

```bash
# Build the package
nix build .#tuios

# Run it
./result/bin/tuios --version

# Test in a shell
nix shell .#tuios -c tuios --version
```

## Release Checklist

Before creating a new release:

1.  Update version in code if needed
2.  Update CHANGELOG.md
3.  Ensure all tests pass
4.  Commit all changes
5.  Create and push tag:
   ```bash
   git tag v0.0.24
   git push origin v0.0.24
   ```
6.  Wait for release workflow to complete
7.  Wait for nix update workflow to complete
8.  Verify GitHub release is created
9.  Verify Homebrew formula is in tap repo
10.  Verify `tuios.nix` is updated on main branch
11.  Test installation:
    ```bash
    brew tap Gaurav-Gosain/tap
    brew install tuios
    tuios --version
    ```

## Troubleshooting

### Homebrew Formula Fails to Publish

**Problem**: GoReleaser can't push to homebrew-tap

**Solutions**:
1. Verify `HOMEBREW_TAP_GITHUB_TOKEN` secret is set correctly
2. Ensure the token has `repo` scope
3. Verify the tap repository exists and is public
4. Check the release workflow logs for specific errors

### Nix Hash Update Fails

**Problem**: The update-nix workflow can't determine the correct hash

**Solutions**:
1. Check the workflow logs for build errors
2. Manually update the hash:
   ```bash
   # Set fake hash in tuios.nix
   sed -i 's/vendorHash = "sha256-.*";/vendorHash = pkgs.lib.fakeHash;/' tuios.nix

   # Build to get real hash
   nix build .#tuios 2>&1 | grep "got:"

   # Update with real hash
   sed -i 's/vendorHash = pkgs.lib.fakeHash;/vendorHash = "sha256-XXXXX";/' tuios.nix
   ```
3. Commit and push manually if needed

### Workflow Creates PR Instead of Direct Commit

**Problem**: The nix update creates a PR instead of pushing directly

**Cause**: This happens when direct push fails (permissions, conflicts, etc.)

**Solution**:
1. Review the automatically created PR
2. Merge it if everything looks correct
3. For future releases, investigate why direct push failed

## Manual Release Process

If automation fails, you can release manually:

### Homebrew

1. Clone the tap repo:
   ```bash
   git clone https://github.com/Gaurav-Gosain/homebrew-tap
   cd homebrew-tap
   ```

2. Create/update `Formula/tuios.rb`:
   ```ruby
   class Tuios < Formula
     desc "Terminal UI Operating System"
     homepage "https://github.com/Gaurav-Gosain/tuios"
     url "https://github.com/Gaurav-Gosain/tuios/archive/v0.0.24.tar.gz"
     sha256 "CHECKSUM"
     license "MIT"

     depends_on "go" => :build

     def install
       system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/tuios"
     end

     test do
       system "#{bin}/tuios", "--version"
     end
   end
   ```

3. Commit and push

### Nix

1. Update `tuios.nix`:
   ```nix
   version = "v0.0.24";
   vendorHash = "sha256-NEWHASH";
   ```

2. Test the build:
   ```bash
   nix build .#tuios
   ```

3. Commit and push

## Additional Resources

- [GoReleaser Documentation](https://goreleaser.com/)
- [Homebrew Tap Documentation](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)
- [Nix Flakes](https://nixos.wiki/wiki/Flakes)
- [GitHub Actions](https://docs.github.com/en/actions)
