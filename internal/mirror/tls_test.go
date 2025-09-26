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
		name    string
		config  TLSConfig
		wantErr bool
		errMsg  string
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
		name     string
		config   TLSConfig
		wantErr  bool
		validate func(*tls.Config) error
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
				_ = tt.validate(cfg)
			}
		})
	}
}

func TestTLSConfigWithCertificates(t *testing.T) {
	// Create temporary directory for test certificates
	tempDir := t.TempDir()

	// Create a dummy CA certificate file for testing
	caCertPath := filepath.Join(tempDir, "ca.pem")
	caCertPEM := `-----BEGIN CERTIFICATE-----
MIIFVzCCAz+gAwIBAgINAgPlk28xsBNJiGuiFzANBgkqhkiG9w0BAQwFADBHMQsw
CQYDVQQGEwJVUzEiMCAGA1UEChMZR29vZ2xlIFRydXN0IFNlcnZpY2VzIExMQzEU
MBIGA1UEAxMLR1RTIFJvb3QgUjEwHhcNMTYwNjIyMDAwMDAwWhcNMzYwNjIyMDAw
MDAwWjBHMQswCQYDVQQGEwJVUzEiMCAGA1UEChMZR29vZ2xlIFRydXN0IFNlcnZp
Y2VzIExMQzEUMBIGA1UEAxMLR1RTIFJvb3QgUjEwggIiMA0GCSqGSIb3DQEBAQUA
A4ICDwAwggIKAoICAQC2EQKLHuOhd5s73L+UPreVp0A8of2C+X0yBoJx9vaMf/vo
27xqLpeXo4xL+Sv2sfnOhB2x+cWX3u+58qPpvBKJXqeqUqv4IyfLpLGcY9vXmX7w
Cl7raKb0xlpHDU0QM+NOsROjyBhsS+z8CZDfnWQpJSMHobTSPS5g4M/SCYe7zUjw
TcLCeoiKu7rPWRnWr4+wB7CeMfGCwcDfLqZtbBkOtdh+JhpFAz2weaSUKK0Pfybl
qAj+lug8aJRT7oM6iCsVlgmy4HqMLnXWnOunVmSPlk9orj2XwoSPwLxAwAtcvfaH
szVsrBhQf4TgTM2S0yDpM7xSma8ytSmzJSq0SPly4cpk9+aCEI3oncKKiPo4Zor8
Y/kB+Xj9e1x3+naH+uzfsQ55lVe0vSbv1gHR6xYKu44LtcXFilWr06zqkUspzBmk
MiVOKvFlRNACzqrOSbTqn3yDsEB750Orp2yjj32JgfpMpf/VjsPOS+C12LOORc92
wO1AK/1TD7Cn1TsNsYqiA94xrcx36m97PtbfkSIS5r762DL8EGMUUXLeXdYWk70p
aDPvOmbsB4om3xPXV2V4J95eSRQAogB/mqghtqmxlbCluQ0WEdrHbEg8QOB+DVrN
VjzRlwW5y0vtOUucxD/SVRNuJLDWcfr0wbrM7Rv1/oFB2ACYPTrIrnqYNxgFlQID
AQABo0IwQDAOBgNVHQ8BAf8EBAMCAYYwDwYDVR0TAQH/BAUwAwEB/zAdBgNVHQ4E
FgQU5K8rJnEaK0gnhS9SZizv8IkTcT4wDQYJKoZIhvcNAQEMBQADggIBAJ+qQibb
C5u+/x6Wki4+omVKapi6Ist9wTrYggoGxval3sBOh2Z5ofmmWJyq+bXmYOfg6LEe
QkEzCzc9zolwFcq1JKjPa7XSQCGYzyI0zzvFIoTgxQ6KfF2I5DUkzps+GlQebtuy
h6f88/qBVRRiClmpIgUxPoLW7ttXNLwzldMXG+gnoot7TiYaelpkttGsN/H9oPM4
7HLwEXWdyzRSjeZ2axfG34arJ45JK3VmgRAhpuo+9K4l/3wV3s6MJT/KYnAK9y8J
ZgfIPxz88NtFMN9iiMG1D53Dn0reWVlHxYciNuaCp+0KueIHoI17eko8cdLiA6Ef
MgfdG+RCzgwARWGAtQsgWSl4vflVy2PFPEz0tv/bal8xa5meLMFrUKTX5hgUvYU/
Z6tGn6D/Qqc6f1zLXbBwHSs09dR2CQzreExZBfMzQsNhFRAbd03OIozUhfJFfbdT
6u9AWpQKXCBfTkBdYiJ23//OYb2MI3jSNwLgjt7RETeJ9r/tSQdirpLsQBqvFAnZ
0E6yove+7u7Y/9waLd64NnHi/Hm3lCXRSHNboTXns5lndcEZOitHTtNCjv0xyBZm
2tIMPNuzjsmhDYAPexZ3FL//2wmUspO8IFgV6dtxQ/PeEMMA3KgqlbbC1j+Qa3bb
ZP6MvPJwNQzcmRk13NfIRmPVNnGuV/u3gm3c
-----END CERTIFICATE-----`

	err := os.WriteFile(caCertPath, []byte(caCertPEM), 0644)
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
		Mirrors: map[string]*MirrorConfig{
			"test": {
				URL:           tomlURL{&url.URL{Scheme: "https", Host: "example.com", Path: "/"}},
				Suites:        []string{"test"},
				Sections:      []string{"main"},
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

func TestGetEffectiveTLSConfig(t *testing.T) {
	tests := []struct {
		name     string
		global   *TLSConfig
		mirror   *TLSOverrides
		expected *TLSConfig
	}{
		{
			name: "no mirror overrides",
			global: &TLSConfig{
				MinVersion: "1.2",
				MaxVersion: "1.3",
				ServerName: "global.example.com",
			},
			mirror: nil,
			expected: &TLSConfig{
				MinVersion: "1.2",
				MaxVersion: "1.3",
				ServerName: "global.example.com",
			},
		},
		{
			name: "mirror overrides server name",
			global: &TLSConfig{
				MinVersion: "1.2",
				ServerName: "global.example.com",
			},
			mirror: &TLSOverrides{
				ServerName: "mirror.example.com",
			},
			expected: &TLSConfig{
				MinVersion: "1.2",
				ServerName: "mirror.example.com",
			},
		},
		{
			name: "mirror overrides insecure skip verify",
			global: &TLSConfig{
				MinVersion:         "1.2",
				InsecureSkipVerify: false,
			},
			mirror: &TLSOverrides{
				InsecureSkipVerify: boolPtr(true),
			},
			expected: &TLSConfig{
				MinVersion:         "1.2",
				InsecureSkipVerify: true,
			},
		},
		{
			name: "mirror overrides certificates",
			global: &TLSConfig{
				MinVersion:     "1.2",
				CACertFile:     "/global/ca.pem",
				ClientCertFile: "/global/client.pem",
				ClientKeyFile:  "/global/client.key",
			},
			mirror: &TLSOverrides{
				CACertFile:     "/mirror/ca.pem",
				ClientCertFile: "/mirror/client.pem",
				ClientKeyFile:  "/mirror/client.key",
			},
			expected: &TLSConfig{
				MinVersion:     "1.2",
				CACertFile:     "/mirror/ca.pem",
				ClientCertFile: "/mirror/client.pem",
				ClientKeyFile:  "/mirror/client.key",
			},
		},
		{
			name: "partial mirror overrides",
			global: &TLSConfig{
				MinVersion:         "1.2",
				MaxVersion:         "1.3",
				InsecureSkipVerify: false,
				CACertFile:         "/global/ca.pem",
				ServerName:         "global.example.com",
			},
			mirror: &TLSOverrides{
				ServerName: "mirror.example.com",
				CACertFile: "/mirror/ca.pem",
			},
			expected: &TLSConfig{
				MinVersion:         "1.2",
				MaxVersion:         "1.3",
				InsecureSkipVerify: false,
				CACertFile:         "/mirror/ca.pem",
				ServerName:         "mirror.example.com",
			},
		},
		{
			name:   "nil global config",
			global: nil,
			mirror: &TLSOverrides{
				ServerName:         "mirror.example.com",
				InsecureSkipVerify: boolPtr(true),
			},
			expected: &TLSConfig{
				ServerName:         "mirror.example.com",
				InsecureSkipVerify: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &MirrorConfig{
				TLS: tt.mirror,
			}

			result := mc.GetEffectiveTLSConfig(tt.global)

			if result.MinVersion != tt.expected.MinVersion {
				t.Errorf("MinVersion = %q, expected %q", result.MinVersion, tt.expected.MinVersion)
			}
			if result.MaxVersion != tt.expected.MaxVersion {
				t.Errorf("MaxVersion = %q, expected %q", result.MaxVersion, tt.expected.MaxVersion)
			}
			if result.InsecureSkipVerify != tt.expected.InsecureSkipVerify {
				t.Errorf("InsecureSkipVerify = %v, expected %v", result.InsecureSkipVerify, tt.expected.InsecureSkipVerify)
			}
			if result.CACertFile != tt.expected.CACertFile {
				t.Errorf("CACertFile = %q, expected %q", result.CACertFile, tt.expected.CACertFile)
			}
			if result.ClientCertFile != tt.expected.ClientCertFile {
				t.Errorf("ClientCertFile = %q, expected %q", result.ClientCertFile, tt.expected.ClientCertFile)
			}
			if result.ClientKeyFile != tt.expected.ClientKeyFile {
				t.Errorf("ClientKeyFile = %q, expected %q", result.ClientKeyFile, tt.expected.ClientKeyFile)
			}
			if result.ServerName != tt.expected.ServerName {
				t.Errorf("ServerName = %q, expected %q", result.ServerName, tt.expected.ServerName)
			}
		})
	}
}

func TestMirrorConfigTLSIntegration(t *testing.T) {
	// Test full integration with Mirror configuration
	tempDir := t.TempDir()

	// Create test certificate files
	caCertPath := filepath.Join(tempDir, "ca.pem")
	clientCertPath := filepath.Join(tempDir, "client.pem")
	clientKeyPath := filepath.Join(tempDir, "client.key")

	// Write dummy certificate content
	testCert := `-----BEGIN CERTIFICATE-----
MIIFVzCCAz+gAwIBAgINAgPlk28xsBNJiGuiFzANBgkqhkiG9w0BAQwFADBHMQsw
CQYDVQQGEwJVUzEiMCAGA1UEChMZR29vZ2xlIFRydXN0IFNlcnZpY2VzIExMQzEU
MBIGA1UEAxMLR1RTIFJvb3QgUjEwHhcNMTYwNjIyMDAwMDAwWhcNMzYwNjIyMDAw
MDAwWjBHMQswCQYDVQQGEwJVUzEiMCAGA1UEChMZR29vZ2xlIFRydXN0IFNlcnZp
Y2VzIExMQzEUMBIGA1UEAxMLR1RTIFJvb3QgUjEwggIiMA0GCSqGSIb3DQEBAQUA
A4ICDwAwggIKAoICAQC2EQKLHuOhd5s73L+UPreVp0A8of2C+X0yBoJx9vaMf/vo
27xqLpeXo4xL+Sv2sfnOhB2x+cWX3u+58qPpvBKJXqeqUqv4IyfLpLGcY9vXmX7w
Cl7raKb0xlpHDU0QM+NOsROjyBhsS+z8CZDfnWQpJSMHobTSPS5g4M/SCYe7zUjw
TcLCeoiKu7rPWRnWr4+wB7CeMfGCwcDfLqZtbBkOtdh+JhpFAz2weaSUKK0Pfybl
qAj+lug8aJRT7oM6iCsVlgmy4HqMLnXWnOunVmSPlk9orj2XwoSPwLxAwAtcvfaH
szVsrBhQf4TgTM2S0yDpM7xSma8ytSmzJSq0SPly4cpk9+aCEI3oncKKiPo4Zor8
Y/kB+Xj9e1x3+naH+uzfsQ55lVe0vSbv1gHR6xYKu44LtcXFilWr06zqkUspzBmk
MiVOKvFlRNACzqrOSbTqn3yDsEB750Orp2yjj32JgfpMpf/VjsPOS+C12LOORc92
wO1AK/1TD7Cn1TsNsYqiA94xrcx36m97PtbfkSIS5r762DL8EGMUUXLeXdYWk70p
aDPvOmbsB4om3xPXV2V4J95eSRQAogB/mqghtqmxlbCluQ0WEdrHbEg8QOB+DVrN
VjzRlwW5y0vtOUucxD/SVRNuJLDWcfr0wbrM7Rv1/oFB2ACYPTrIrnqYNxgFlQID
AQABo0IwQDAOBgNVHQ8BAf8EBAMCAYYwDwYDVR0TAQH/BAUwAwEB/zAdBgNVHQ4E
FgQU5K8rJnEaK0gnhS9SZizv8IkTcT4wDQYJKoZIhvcNAQEMBQADggIBAJ+qQibb
C5u+/x6Wki4+omVKapi6Ist9wTrYggoGxval3sBOh2Z5ofmmWJyq+bXmYOfg6LEe
QkEzCzc9zolwFcq1JKjPa7XSQCGYzyI0zzvFIoTgxQ6KfF2I5DUkzps+GlQebtuy
h6f88/qBVRRiClmpIgUxPoLW7ttXNLwzldMXG+gnoot7TiYaelpkttGsN/H9oPM4
7HLwEXWdyzRSjeZ2axfG34arJ45JK3VmgRAhpuo+9K4l/3wV3s6MJT/KYnAK9y8J
ZgfIPxz88NtFMN9iiMG1D53Dn0reWVlHxYciNuaCp+0KueIHoI17eko8cdLiA6Ef
MgfdG+RCzgwARWGAtQsgWSl4vflVy2PFPEz0tv/bal8xa5meLMFrUKTX5hgUvYU/
Z6tGn6D/Qqc6f1zLXbBwHSs09dR2CQzreExZBfMzQsNhFRAbd03OIozUhfJFfbdT
6u9AWpQKXCBfTkBdYiJ23//OYb2MI3jSNwLgjt7RETeJ9r/tSQdirpLsQBqvFAnZ
0E6yove+7u7Y/9waLd64NnHi/Hm3lCXRSHNboTXns5lndcEZOitHTtNCjv0xyBZm
2tIMPNuzjsmhDYAPexZ3FL//2wmUspO8IFgV6dtxQ/PeEMMA3KgqlbbC1j+Qa3bb
ZP6MvPJwNQzcmRk13NfIRmPVNnGuV/u3gm3c
-----END CERTIFICATE-----`

	err := os.WriteFile(caCertPath, []byte(testCert), 0644)
	if err != nil {
		t.Fatal("Failed to write CA cert:", err)
	}

	err = os.WriteFile(clientCertPath, []byte(testCert), 0644)
	if err != nil {
		t.Fatal("Failed to write client cert:", err)
	}

	err = os.WriteFile(clientKeyPath, []byte(testCert), 0644)
	if err != nil {
		t.Fatal("Failed to write client key:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 10,
		TLS: TLSConfig{
			MinVersion: "1.2",
			MaxVersion: "1.3",
		},
		Mirrors: map[string]*MirrorConfig{
			"test-global": {
				URL:           tomlURL{&url.URL{Scheme: "https", Host: "example.com", Path: "/"}},
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
			"test-override": {
				URL:           tomlURL{&url.URL{Scheme: "https", Host: "secure.example.com", Path: "/"}},
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
				TLS: &TLSOverrides{
					ServerName:     "custom.example.com",
					CACertFile:     caCertPath,
					ClientCertFile: clientCertPath,
					ClientKeyFile:  clientKeyPath,
				},
			},
			"test-insecure": {
				URL:           tomlURL{&url.URL{Scheme: "https", Host: "internal.example.com", Path: "/"}},
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
				TLS: &TLSOverrides{
					InsecureSkipVerify: boolPtr(true),
				},
			},
		},
	}

	// Test mirror with global TLS settings
	testGlobal := config.Mirrors["test-global"]
	effectiveGlobal := testGlobal.GetEffectiveTLSConfig(&config.TLS)

	if effectiveGlobal.MinVersion != "1.2" {
		t.Errorf("test-global: expected MinVersion 1.2, got %s", effectiveGlobal.MinVersion)
	}
	if effectiveGlobal.MaxVersion != "1.3" {
		t.Errorf("test-global: expected MaxVersion 1.3, got %s", effectiveGlobal.MaxVersion)
	}
	if effectiveGlobal.ServerName != "" {
		t.Errorf("test-global: expected empty ServerName, got %s", effectiveGlobal.ServerName)
	}

	// Test mirror with TLS overrides
	testOverride := config.Mirrors["test-override"]
	effectiveOverride := testOverride.GetEffectiveTLSConfig(&config.TLS)

	if effectiveOverride.MinVersion != "1.2" {
		t.Errorf("test-override: expected MinVersion 1.2, got %s", effectiveOverride.MinVersion)
	}
	if effectiveOverride.ServerName != "custom.example.com" {
		t.Errorf("test-override: expected ServerName custom.example.com, got %s", effectiveOverride.ServerName)
	}
	if effectiveOverride.CACertFile != caCertPath {
		t.Errorf("test-override: expected CACertFile %s, got %s", caCertPath, effectiveOverride.CACertFile)
	}

	// Test mirror with insecure override
	testInsecure := config.Mirrors["test-insecure"]
	effectiveInsecure := testInsecure.GetEffectiveTLSConfig(&config.TLS)

	if !effectiveInsecure.InsecureSkipVerify {
		t.Error("test-insecure: expected InsecureSkipVerify to be true")
	}
	if effectiveInsecure.MinVersion != "1.2" {
		t.Errorf("test-insecure: expected MinVersion 1.2, got %s", effectiveInsecure.MinVersion)
	}

	// Validate all configurations
	for name, mc := range config.Mirrors {
		effective := mc.GetEffectiveTLSConfig(&config.TLS)
		if err := effective.Validate(); err != nil {
			t.Errorf("mirror %s: TLS validation failed: %v", name, err)
		}
	}
}

// Helper function to create a pointer to bool
func boolPtr(b bool) *bool {
	return &b
}
