package mirror

import (
	"reflect"
	"testing"
)

func TestApplyEnvironmentVariables(t *testing.T) {

	tests := []struct {
		name      string
		envVars   map[string]string
		config    *Config
		expected  *Config
		expectErr bool
	}{
		{
			name: "basic string and int overrides",
			envVars: map[string]string{
				"MIRRORCTL_DIR":       "/custom/path",
				"MIRRORCTL_MAX_CONNS": "20",
			},
			config: &Config{
				Dir:      "/original/path",
				MaxConns: 10,
			},
			expected: &Config{
				Dir:      "/custom/path",
				MaxConns: 20,
			},
		},
		{
			name: "log configuration overrides",
			envVars: map[string]string{
				"MIRRORCTL_LOG_LEVEL":  "debug",
				"MIRRORCTL_LOG_FORMAT": "json",
			},
			config: &Config{
				Log: LogConfig{
					Level:  "info",
					Format: "text",
				},
			},
			expected: &Config{
				Log: LogConfig{
					Level:  "debug",
					Format: "json",
				},
			},
		},
		{
			name: "TLS configuration overrides",
			envVars: map[string]string{
				"MIRRORCTL_TLS_MIN_VERSION":          "1.3",
				"MIRRORCTL_TLS_MAX_VERSION":          "1.3",
				"MIRRORCTL_TLS_INSECURE_SKIP_VERIFY": "true",
				"MIRRORCTL_TLS_CA_CERT_FILE":         "/custom/ca.pem",
				"MIRRORCTL_TLS_CLIENT_CERT_FILE":     "/custom/client.pem",
				"MIRRORCTL_TLS_CLIENT_KEY_FILE":      "/custom/client.key",
				"MIRRORCTL_TLS_SERVER_NAME":          "custom.example.com",
			},
			config: &Config{
				TLS: TLSConfig{
					MinVersion:         "1.2",
					MaxVersion:         "1.2",
					InsecureSkipVerify: false,
					CACertFile:         "/original/ca.pem",
					ClientCertFile:     "/original/client.pem",
					ClientKeyFile:      "/original/client.key",
					ServerName:         "original.example.com",
				},
			},
			expected: &Config{
				TLS: TLSConfig{
					MinVersion:         "1.3",
					MaxVersion:         "1.3",
					InsecureSkipVerify: true,
					CACertFile:         "/custom/ca.pem",
					ClientCertFile:     "/custom/client.pem",
					ClientKeyFile:      "/custom/client.key",
					ServerName:         "custom.example.com",
				},
			},
		},
		{
			name: "cipher suites array override",
			envVars: map[string]string{
				"MIRRORCTL_TLS_CIPHER_SUITES": "TLS_AES_256_GCM_SHA384, TLS_CHACHA20_POLY1305_SHA256",
			},
			config: &Config{
				TLS: TLSConfig{
					CipherSuites: []string{"TLS_AES_128_GCM_SHA256"},
				},
			},
			expected: &Config{
				TLS: TLSConfig{
					CipherSuites: []string{"TLS_AES_256_GCM_SHA384", "TLS_CHACHA20_POLY1305_SHA256"},
				},
			},
		},
		{
			name: "partial overrides - only some env vars set",
			envVars: map[string]string{
				"MIRRORCTL_DIR":             "/custom/path",
				"MIRRORCTL_TLS_MIN_VERSION": "1.3",
				"MIRRORCTL_LOG_LEVEL":       "debug",
			},
			config: &Config{
				Dir:      "/original/path",
				MaxConns: 15,
				Log: LogConfig{
					Level:  "info",
					Format: "text",
				},
				TLS: TLSConfig{
					MinVersion: "1.2",
					MaxVersion: "1.2",
					ServerName: "original.example.com",
				},
			},
			expected: &Config{
				Dir:      "/custom/path",
				MaxConns: 15, // unchanged
				Log: LogConfig{
					Level:  "debug", // changed
					Format: "text",  // unchanged
				},
				TLS: TLSConfig{
					MinVersion: "1.3",                  // changed
					MaxVersion: "1.2",                  // unchanged
					ServerName: "original.example.com", // unchanged
				},
			},
		},
		{
			name:    "no environment variables set",
			envVars: map[string]string{}, // no env vars
			config: &Config{
				Dir:      "/original/path",
				MaxConns: 10,
				Log: LogConfig{
					Level:  "info",
					Format: "text",
				},
			},
			expected: &Config{
				Dir:      "/original/path",
				MaxConns: 10,
				Log: LogConfig{
					Level:  "info",
					Format: "text",
				},
			},
		},
		{
			name: "invalid integer value",
			envVars: map[string]string{
				"MIRRORCTL_MAX_CONNS": "not-a-number",
			},
			config: &Config{
				MaxConns: 10,
			},
			expectErr: true,
		},
		{
			name: "invalid boolean value",
			envVars: map[string]string{
				"MIRRORCTL_TLS_INSECURE_SKIP_VERIFY": "not-a-bool",
			},
			config: &Config{
				TLS: TLSConfig{
					InsecureSkipVerify: false,
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set test env vars
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			// Apply environment variables
			err := tt.config.ApplyEnvironmentVariables()

			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Compare results
			if tt.config.Dir != tt.expected.Dir {
				t.Errorf("Dir = %q, expected %q", tt.config.Dir, tt.expected.Dir)
			}
			if tt.config.MaxConns != tt.expected.MaxConns {
				t.Errorf("MaxConns = %d, expected %d", tt.config.MaxConns, tt.expected.MaxConns)
			}
			if tt.config.Log.Level != tt.expected.Log.Level {
				t.Errorf("Log.Level = %q, expected %q", tt.config.Log.Level, tt.expected.Log.Level)
			}
			if tt.config.Log.Format != tt.expected.Log.Format {
				t.Errorf("Log.Format = %q, expected %q", tt.config.Log.Format, tt.expected.Log.Format)
			}
			if tt.config.TLS.MinVersion != tt.expected.TLS.MinVersion {
				t.Errorf("TLS.MinVersion = %q, expected %q", tt.config.TLS.MinVersion, tt.expected.TLS.MinVersion)
			}
			if tt.config.TLS.MaxVersion != tt.expected.TLS.MaxVersion {
				t.Errorf("TLS.MaxVersion = %q, expected %q", tt.config.TLS.MaxVersion, tt.expected.TLS.MaxVersion)
			}
			if tt.config.TLS.InsecureSkipVerify != tt.expected.TLS.InsecureSkipVerify {
				t.Errorf("TLS.InsecureSkipVerify = %v, expected %v", tt.config.TLS.InsecureSkipVerify, tt.expected.TLS.InsecureSkipVerify)
			}
			if tt.config.TLS.CACertFile != tt.expected.TLS.CACertFile {
				t.Errorf("TLS.CACertFile = %q, expected %q", tt.config.TLS.CACertFile, tt.expected.TLS.CACertFile)
			}
			if tt.config.TLS.ClientCertFile != tt.expected.TLS.ClientCertFile {
				t.Errorf("TLS.ClientCertFile = %q, expected %q", tt.config.TLS.ClientCertFile, tt.expected.TLS.ClientCertFile)
			}
			if tt.config.TLS.ClientKeyFile != tt.expected.TLS.ClientKeyFile {
				t.Errorf("TLS.ClientKeyFile = %q, expected %q", tt.config.TLS.ClientKeyFile, tt.expected.TLS.ClientKeyFile)
			}
			if tt.config.TLS.ServerName != tt.expected.TLS.ServerName {
				t.Errorf("TLS.ServerName = %q, expected %q", tt.config.TLS.ServerName, tt.expected.TLS.ServerName)
			}
			if !reflect.DeepEqual(tt.config.TLS.CipherSuites, tt.expected.TLS.CipherSuites) {
				t.Errorf("TLS.CipherSuites = %v, expected %v", tt.config.TLS.CipherSuites, tt.expected.TLS.CipherSuites)
			}
		})
	}
}

func TestSetFieldFromEnv(t *testing.T) {
	tests := []struct {
		name      string
		envVar    string
		envValue  string
		fieldType reflect.Type
		expected  any
		expectErr bool
	}{
		{
			name:      "string field",
			envVar:    "TEST_STRING",
			envValue:  "test-value",
			fieldType: reflect.TypeOf(""),
			expected:  "test-value",
		},
		{
			name:      "int field",
			envVar:    "TEST_INT",
			envValue:  "42",
			fieldType: reflect.TypeOf(0),
			expected:  42,
		},
		{
			name:      "bool field true",
			envVar:    "TEST_BOOL",
			envValue:  "true",
			fieldType: reflect.TypeOf(false),
			expected:  true,
		},
		{
			name:      "bool field false",
			envVar:    "TEST_BOOL",
			envValue:  "false",
			fieldType: reflect.TypeOf(false),
			expected:  false,
		},
		{
			name:      "string slice field",
			envVar:    "TEST_SLICE",
			envValue:  "item1, item2, item3",
			fieldType: reflect.TypeOf([]string{}),
			expected:  []string{"item1", "item2", "item3"},
		},
		{
			name:      "invalid int",
			envVar:    "TEST_INT",
			envValue:  "not-a-number",
			fieldType: reflect.TypeOf(0),
			expectErr: true,
		},
		{
			name:      "invalid bool",
			envVar:    "TEST_BOOL",
			envValue:  "not-a-bool",
			fieldType: reflect.TypeOf(false),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			t.Setenv(tt.envVar, tt.envValue)

			// Create a field of the specified type
			field := reflect.New(tt.fieldType).Elem()

			// Test setFieldFromEnv
			err := setFieldFromEnv(field, tt.envVar)

			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Check the result
			actual := field.Interface()
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("Expected %v (%T), got %v (%T)", tt.expected, tt.expected, actual, actual)
			}
		})
	}
}

func TestEmptyEnvironmentVariable(t *testing.T) {
	// Test that empty environment variables don't override existing values
	config := &Config{
		Dir:      "/original/path",
		MaxConns: 10,
	}

	// Environment variables should not be set by default
	err := config.ApplyEnvironmentVariables()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Values should remain unchanged
	if config.Dir != "/original/path" {
		t.Errorf("Dir = %q, expected %q", config.Dir, "/original/path")
	}
	if config.MaxConns != 10 {
		t.Errorf("MaxConns = %d, expected %d", config.MaxConns, 10)
	}
}
