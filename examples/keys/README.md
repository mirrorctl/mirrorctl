# GPG Keys

This directory contains GPG public keys used for package verification in the example configurations.

## Keys Included

### microsoft.asc
- **Purpose**: Microsoft package signing key
- **Used by**:
  - `amlfs-noble` mirror
  - `openenclave` mirror
  - `slurm-ubuntu-noble` mirror
- **Source**: Microsoft's official GPG key for package repositories
- **Fingerprint**: Used to verify packages from Microsoft repositories

## Security Note

These are public keys only - they are safe to include in the repository. They are used to verify the authenticity of downloaded packages, not for signing or encryption.

## Usage

The example configurations reference these keys with relative paths like:
```toml
pgp_key_path = "examples/keys/microsoft.asc"
```

This ensures the examples work immediately after cloning the repository without requiring external key files.