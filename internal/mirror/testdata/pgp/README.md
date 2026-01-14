# PGP Test Data

This directory contains test fixtures for PGP signature verification in APT repository mirroring.

## Files

- InRelease - Valid cleartext-signed APT Release file with inline PGP signature
- InRelease.invalid - InRelease file with corrupted signature header; uses "-----BEGIN PGP SIGNATURE-CORRUPTED-----" instead of "-----BEGIN PGP SIGNATURE-----" (for testing parse failures)
- keygen-config - GPG batch configuration used to generate the test keypair
- private-key.asc - ASCII-armored private key for signing test files
- public-key.asc - ASCII-armored public key for verifying signatures
- Release - Unsigned APT Release file containing repository metadata
- Release.gpg - Detached PGP signature for the Release file
- Release.tampered - Modified Release file; Origin field changed from "Test Repository" to "Test Repository TAMPERED" so the content no longer matches the detached signature (for testing tamper detection)
- wrong-keygen-config - GPG batch configuration for generating a different keypair
- wrong-public-key.asc - Public key from a different keypair (for testing verification with wrong key)
