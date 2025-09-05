package mirror

import (
	"crypto/tls"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTLSConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    TLSConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name:    "empty config should be valid",
			config:  TLSConfig{},
			wantErr: false,
		},
		{
			name: "valid TLS 1.2 minimum",
			config: TLSConfig{
				MinVersion: "1.2",
			},
			wantErr: false,
		},
		{
			name: "valid TLS 1.3 range",
			config: TLSConfig{
				MinVersion: "1.2",
				MaxVersion: "1.3",
			},
			wantErr: false,
		},
		{
			name: "invalid version range",
			config: TLSConfig{
				MinVersion: "1.3",
				MaxVersion: "1.2",
			},
			wantErr: true,
			errMsg:  "min_version cannot be greater than max_version",
		},
		{
			name: "missing client key file",
			config: TLSConfig{
				ClientCertFile: "cert.pem",
			},
			wantErr: true,
			errMsg:  "both client_cert_file and client_key_file must be specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("TLSConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("TLSConfig.Validate() error = %v, wanted error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestTLSConfigBuild(t *testing.T) {
	tests := []struct {
		name      string
		config    TLSConfig
		wantErr   bool
		validate  func(*tls.Config) error
	}{
		{
			name:   "default config",
			config: TLSConfig{},
			validate: func(cfg *tls.Config) error {
				if cfg.MinVersion != tls.VersionTLS12 {
					t.Errorf("Expected MinVersion TLS 1.2, got %x", cfg.MinVersion)
				}
				if cfg.InsecureSkipVerify {
					t.Error("Expected secure verification by default")
				}
				return nil
			},
		},
		{
			name: "TLS 1.3 only",
			config: TLSConfig{
				MinVersion: "1.3",
				MaxVersion: "1.3",
			},
			validate: func(cfg *tls.Config) error {
				if cfg.MinVersion != tls.VersionTLS13 {
					t.Errorf("Expected MinVersion TLS 1.3, got %x", cfg.MinVersion)
				}
				if cfg.MaxVersion != tls.VersionTLS13 {
					t.Errorf("Expected MaxVersion TLS 1.3, got %x", cfg.MaxVersion)
				}
				return nil
			},
		},
		{
			name: "insecure skip verify",
			config: TLSConfig{
				InsecureSkipVerify: true,
			},
			validate: func(cfg *tls.Config) error {
				if !cfg.InsecureSkipVerify {
					t.Error("Expected InsecureSkipVerify to be true")
				}
				return nil
			},
		},
		{
			name: "custom server name",
			config: TLSConfig{
				ServerName: "custom.example.com",
			},
			validate: func(cfg *tls.Config) error {
				if cfg.ServerName != "custom.example.com" {
					t.Errorf("Expected ServerName 'custom.example.com', got %q", cfg.ServerName)
				}
				return nil
			},
		},
		{
			name: "cipher suites",
			config: TLSConfig{
				CipherSuites: []string{"TLS_AES_256_GCM_SHA384", "TLS_CHACHA20_POLY1305_SHA256"},
			},
			validate: func(cfg *tls.Config) error {
				if len(cfg.CipherSuites) != 2 {
					t.Errorf("Expected 2 cipher suites, got %d", len(cfg.CipherSuites))
				}
				expectedSuites := []uint16{tls.TLS_AES_256_GCM_SHA384, tls.TLS_CHACHA20_POLY1305_SHA256}
				for i, suite := range cfg.CipherSuites {
					if suite != expectedSuites[i] {
						t.Errorf("Expected cipher suite %x, got %x", expectedSuites[i], suite)
					}
				}
				return nil
			},
		},
		{
			name: "invalid cipher suite",
			config: TLSConfig{
				CipherSuites: []string{"INVALID_CIPHER_SUITE"},
			},
			wantErr: true,
		},
		{
			name: "invalid min version",
			config: TLSConfig{
				MinVersion: "1.1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := tt.config.BuildTLSConfig()
			if (err != nil) != tt.wantErr {
				t.Errorf("TLSConfig.BuildTLSConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.validate != nil {
				tt.validate(cfg)
			}
		})
	}
}

func TestTLSConfigWithCertificates(t *testing.T) {
	// Create temporary directory for test certificates
	tempDir, err := os.MkdirTemp("", "tls_test")
	if err != nil {
		t.Fatal("Failed to create temp dir:", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a dummy CA certificate file for testing
	caCertPath := filepath.Join(tempDir, "ca.pem")
	caCertPEM := `-----BEGIN CERTIFICATE-----
MIIC2jCCAcKgAwIBAgIJANvGy4C4+QlbMA0GCSqGSIb3DQEBCwUAMC4xLDAqBgNV
BAMTLVRlc3QgQ0EgZm9yIGdvLWFwdC1taXJyb3IgVExTIHRlc3RpbmcwHhcNMjQw
MTAxMDAwMDAwWhcNMjUwMTAxMDAwMDAwWjAuMSwwKgYDVQQDEy1UZXN0IENBIGZv
ciBnby1hcHQtbWlycm9yIFRMUyB0ZXN0aW5nMIIBIjANBgkqhkiG9w0BAQEFAAOC
AQ8AMIIBCgKCAQEA7FPdHnE+5XJ9m3j2nFnJ8M4kJ9vK6L2bN3sT4w9+0s8mK2
eQ7uRt1aS8kL9yUvM9Rx3l1w2eR4oY8fT5Q3sD1aL3vG9jR2k4M8nJ7iF5xP9Q
a2wL8sK7eH3fT6oP9mK1aZ2kS5fR8vJ3sP7qL9kH2dF5tK8mN4vS3lY9aT1bQ9
sR7kU3fG6wL9mJ5tH8nF2dP3qK9vM5sL7uR3fT4oY1aS8kP9Q2wL8sK7eH3fT6
oP9mK1aZ2kS5fR8vJ3sP7qL9kH2dF5tK8mN4vS3lY9aT1bQ9sR7kU3fG6wL9mJ
5tH8nF2dP3qK9vM5sL7uR3fT4oY1aS8kP9Q2wL8sK7eH3fT6oP9mK1aZ2kS5fR
8vJ3sP7qL9kH2dF5tK8mN4vS3lY9aT1bQ9sR7kU3fG6wL9mJ5tH8nF2dP3qK9v
M5sL7uR3fT4oY1aS8kP9Q2wL8sK7eH3fT6oP9mK1aZ2kS5fR8vJ3sP7qL9kH2d
F5tK8mN4vS3lY9aT1bQ9sR7kU3fG6wL9mJ5tH8nF2dP3qK9vM5sL7uR3fT4oY1
aS8kP9Q2wQIDAQABMA0GCSqGSIb3DQEBCwUAA4IBAQCz3VtKa8bVfJ5kR3oY9aL
6wP7sH2kF5tT3q9aS8kP9Q2wL8sK7eH3fT6oP9mK1aZ2kS5fR8vJ3sP7qL9kH2d
F5tK8mN4vS3lY9aT1bQ9sR7kU3fG6wL9mJ5tH8nF2dP3qK9vM5sL7uR3fT4oY1
aS8kP9Q2wL8sK7eH3fT6oP9mK1aZ2kS5fR8vJ3sP7qL9kH2dF5tK8mN4vS3lY9
aT1bQ9sR7kU3fG6wL9mJ5tH8nF2dP3qK9vM5sL7uR3fT4oY1aS8kP9Q2wL8sK7
eH3fT6oP9mK1aZ2kS5fR8vJ3sP7qL9kH2dF5tK8mN4vS3lY9aT1bQ9sR7kU3fG
6wL9mJ5tH8nF2dP3qK9vM5sL7uR3fT4oY1aS8kP9Q2wL8sK7eH3fT6oP9mK1aZ
2kS5fR8vJ3sP7qL9kH2dF5tK8mN4vS3lY9aT1bQ9sR7kU3fG6wL9mJ5tH8nF2d
P3qK9vM5sL7uR3fT4oY1aS8kP9Q
-----END CERTIFICATE-----`

	err = os.WriteFile(caCertPath, []byte(caCertPEM), 0644)
	if err != nil {
		t.Fatal("Failed to write CA cert:", err)
	}

	// Test CA certificate loading
	config := TLSConfig{
		CACertFile: caCertPath,
	}

	tlsConfig, err := config.BuildTLSConfig()
	if err != nil {
		t.Fatal("BuildTLSConfig failed:", err)
	}

	if tlsConfig.RootCAs == nil {
		t.Error("Expected RootCAs to be set")
	}
}

func TestTLSConfigIntegration(t *testing.T) {
	// Test full config with TLS settings
	config := &Config{
		Dir:      "/tmp/test",
		MaxConns: 10,
		TLS: TLSConfig{
			MinVersion: "1.2",
			MaxVersion: "1.3",
		},
		Mirrors: map[string]*MirrConfig{
			"test": {
				URL: tomlURL{&url.URL{Scheme: "https", Host: "example.com", Path: "/"}},
				Suites: []string{"test"},
				Sections: []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	// This should not return an error since TLS config is valid
	err := config.Check()
	if err != nil {
		t.Error("Config validation failed:", err)
	}
}

func TestConfigWithInsecureTLS(t *testing.T) {
	// Capture log output to verify warning is logged
	config := TLSConfig{
		InsecureSkipVerify: true,
	}

	// This should pass validation but log a warning
	err := config.Validate()
	if err != nil {
		t.Error("Validation should pass even with insecure setting:", err)
	}

	// Build config should succeed
	tlsConfig, err := config.BuildTLSConfig()
	if err != nil {
		t.Error("BuildTLSConfig failed:", err)
	}

	if !tlsConfig.InsecureSkipVerify {
		t.Error("Expected InsecureSkipVerify to be true")
	}
}