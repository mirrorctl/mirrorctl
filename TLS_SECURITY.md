# TLS Security Hardening Guide for go-apt-mirror

This document describes the TLS/HTTPS security features and hardening options available in go-apt-mirror.

## Overview

go-apt-mirror now includes comprehensive TLS security configuration to protect against man-in-the-middle attacks, certificate validation bypasses, and other TLS-related vulnerabilities when mirroring APT repositories over HTTPS.

## Security Improvements Implemented

### 1. TLS Configuration Options

The `[tls]` section in the configuration file provides extensive control over TLS behavior:

```toml
[tls]
# Minimum TLS version (1.2 or 1.3) - defaults to 1.2
min_version = "1.2"

# Maximum TLS version (1.2 or 1.3) 
max_version = "1.3"

# Certificate verification control (default: false - secure)
insecure_skip_verify = false

# Custom CA certificate for private repositories
ca_cert_file = "/path/to/ca.pem"

# Client certificates for mutual TLS
client_cert_file = "/path/to/client.pem"
client_key_file = "/path/to/client.key"

# Custom cipher suites (empty = secure Go defaults)
cipher_suites = [
    "TLS_AES_256_GCM_SHA384",
    "TLS_CHACHA20_POLY1305_SHA256"
]

# Custom server name for SNI
server_name = "mirror.example.com"
```

### 2. Security Features

#### **Mandatory TLS 1.2 Minimum**
- Default minimum TLS version is 1.2 (overriding Go's default)
- Prevents downgrade attacks to TLS 1.1 or earlier
- Configurable to TLS 1.3 only for maximum security

#### **Certificate Validation Hardening**
- Strict certificate validation by default
- Custom CA certificate support for private repositories
- Warning logging when certificate validation is disabled
- Proper hostname verification

#### **Cipher Suite Control**
- Support for modern, secure cipher suites
- TLS 1.3 cipher suites: AES-GCM, ChaCha20-Poly1305
- TLS 1.2 cipher suites: ECDHE with AES-GCM
- Excludes weak or deprecated cipher suites

#### **Mutual TLS Support**
- Client certificate authentication
- Proper validation of client cert/key pairs
- Support for private repository authentication

## Security Best Practices

### 1. Repository URLs
**Always use HTTPS URLs** in mirror configurations:

```toml
# ✅ SECURE - Use HTTPS
[mirrors.ubuntu]
url = "https://archive.ubuntu.com/ubuntu"

# ❌ INSECURE - Avoid HTTP
[mirrors.ubuntu-insecure]
url = "http://archive.ubuntu.com/ubuntu"  # Vulnerable to MITM
```

### 2. TLS Version Configuration
**Recommended TLS settings** for maximum security:

```toml
[tls]
# Force TLS 1.3 only for maximum security (if supported by repositories)
min_version = "1.3"
max_version = "1.3"

# Or maintain compatibility with TLS 1.2+
min_version = "1.2"
max_version = "1.3"
```

### 3. Certificate Validation
**Never disable certificate validation** in production:

```toml
[tls]
# ❌ NEVER do this in production
insecure_skip_verify = true

# ✅ Use custom CA if needed instead
ca_cert_file = "/etc/ssl/certs/private-ca.pem"
```

### 4. Cipher Suite Hardening
**Modern cipher suites only**:

```toml
[tls]
cipher_suites = [
    # TLS 1.3 - Strongest
    "TLS_AES_256_GCM_SHA384",
    "TLS_CHACHA20_POLY1305_SHA256",
    "TLS_AES_128_GCM_SHA256",
    
    # TLS 1.2 - ECDHE with AEAD ciphers only
    "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
    "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
]
```

## Threat Model & Mitigations

### 1. Man-in-the-Middle (MITM) Attacks
**Threats:**
- Malicious repository content injection
- Credential harvesting
- Traffic interception

**Mitigations:**
- Mandatory HTTPS with certificate validation
- TLS 1.2+ minimum version requirement
- Strong cipher suite enforcement
- Proper certificate chain validation

### 2. Downgrade Attacks
**Threats:**
- Forcing use of weak TLS versions
- Cipher suite downgrade attacks

**Mitigations:**
- Configurable minimum TLS version (default 1.2)
- Cipher suite restrictions
- No support for legacy protocols (SSLv3, TLS 1.0, TLS 1.1)

### 3. Certificate Validation Bypass
**Threats:**
- Accepting invalid/expired certificates
- Missing hostname verification
- Accepting self-signed certificates

**Mitigations:**
- Strict certificate validation by default
- Hostname verification enforcement
- Custom CA support for legitimate private repositories
- Clear warnings when validation is disabled

### 4. Private Repository Security
**Features:**
- Custom CA certificate support
- Mutual TLS authentication
- SNI (Server Name Indication) support
- Per-repository TLS configuration (future enhancement)

## Configuration Examples

### Public Repository (Standard Security)
```toml
[tls]
min_version = "1.2"
max_version = "1.3"

[mirrors.ubuntu]
url = "https://archive.ubuntu.com/ubuntu"
suites = ["jammy", "jammy-updates"]
sections = ["main", "restricted", "universe"]
architectures = ["amd64"]
```

### Private Repository with Custom CA
```toml
[tls]
min_version = "1.2"
ca_cert_file = "/etc/ssl/certs/corporate-ca.pem"

[mirrors.corporate]
url = "https://mirror.corp.example.com/ubuntu"
suites = ["jammy"]
sections = ["main"]
architectures = ["amd64"]
```

### Maximum Security Configuration
```toml
[tls]
min_version = "1.3"        # TLS 1.3 only
max_version = "1.3"
cipher_suites = [
    "TLS_AES_256_GCM_SHA384",
    "TLS_CHACHA20_POLY1305_SHA256"
]

[mirrors.secure]
url = "https://secure-mirror.example.com/ubuntu"
suites = ["jammy"]
sections = ["main"]
architectures = ["amd64"]
```

### Mutual TLS Authentication
```toml
[tls]
min_version = "1.2"
client_cert_file = "/etc/ssl/certs/mirror-client.pem"
client_key_file = "/etc/ssl/private/mirror-client.key"
ca_cert_file = "/etc/ssl/certs/mirror-ca.pem"

[mirrors.authenticated]
url = "https://authenticated-mirror.example.com/ubuntu"
suites = ["jammy"]
sections = ["main"]  
architectures = ["amd64"]
```

## Validation and Testing

The TLS configuration includes built-in validation:

1. **Configuration Validation**: Ensures TLS settings are consistent and secure
2. **Certificate File Validation**: Verifies certificate files exist and are readable
3. **Version Compatibility**: Validates TLS version constraints
4. **Cipher Suite Verification**: Ensures only supported cipher suites are configured

Run configuration validation:
```bash
go-apt-mirror validate -c /path/to/config.toml
```

## Migration from HTTP to HTTPS

When migrating existing HTTP configurations to HTTPS:

1. **Update URLs**: Change `http://` to `https://` in mirror configurations
2. **Test Connectivity**: Verify HTTPS repositories are accessible
3. **Add TLS Config**: Configure appropriate TLS settings
4. **Validate Certificates**: Ensure certificate validation works
5. **Monitor Logs**: Check for TLS-related warnings or errors

## Security Considerations

### 1. Performance Impact
- TLS adds computational overhead (usually minimal)
- Certificate validation requires network round-trips
- Consider connection pooling and keep-alive settings

### 2. Compatibility
- Some older repositories may not support TLS 1.3
- Custom CA certificates may be required for corporate environments
- Client certificates are rarely needed for public repositories

### 3. Monitoring
- Monitor for TLS handshake failures
- Watch for certificate expiration warnings
- Log TLS version and cipher suite negotiation (debug mode)

## Troubleshooting

### Common TLS Issues

**Certificate Validation Errors:**
```
Failed to verify certificate: x509: certificate signed by unknown authority
```
Solution: Add custom CA certificate or verify repository certificate

**TLS Version Mismatch:**
```
TLS handshake failed: protocol version not supported
```
Solution: Adjust min_version/max_version settings

**Cipher Suite Issues:**
```
TLS handshake failed: no cipher suite supported by both client and server
```
Solution: Remove cipher_suites configuration to use Go defaults

### Debug Mode
Enable debug logging to see TLS negotiation details:
```toml
[log]
level = "debug"
```

This will show TLS version, cipher suite, and certificate details in logs.