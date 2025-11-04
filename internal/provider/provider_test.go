package provider

import (
	"strings"
	"testing"
)

func TestInstanceIDFromProviderID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		tt := []struct {
			name       string
			providerID string
			expected   string
		}{
			{
				name:       "valid provider ID with UUID",
				providerID: "oxide://12345678-1234-1234-1234-123456789abc",
				expected:   "12345678-1234-1234-1234-123456789abc",
			},
			{
				name:       "valid provider ID with uppercase UUID",
				providerID: "oxide://12345678-1234-1234-1234-123456789ABC",
				expected:   "12345678-1234-1234-1234-123456789ABC",
			},
		}

		for _, tc := range tt {
			t.Run(tc.name, func(t *testing.T) {
				result, err := InstanceIDFromProviderID(tc.providerID)
				if err != nil {
					t.Errorf("TestInstanceIDFromProviderID(%s) returned non-nil error %v, want nil error", tc.providerID, err)
				}

				if result != tc.expected {
					t.Errorf("TestInstanceIDFromProviderID(%s) returned %s, want %s", tc.providerID, result, tc.expected)
				}
			})
		}
	})

	t.Run("Error", func(t *testing.T) {
		tt := []struct {
			name       string
			providerID string
			errorMsg   string
		}{
			{
				name:       "empty provider ID",
				providerID: "",
				errorMsg:   "provider id is empty",
			},
			{
				name:       "provider ID without oxide:// prefix",
				providerID: "12345678-1234-1234-1234-123456789abc",
				errorMsg:   "provider id does not have 'oxide://' prefix",
			},
			{
				name:       "provider ID with invalid UUID",
				providerID: "oxide://not-a-valid-uuid",
				errorMsg:   "provider id contains invalid uuid",
			},
			{
				name:       "provider ID with empty UUID",
				providerID: "oxide://",
				errorMsg:   "provider id contains invalid uuid",
			},
			{
				name:       "provider ID with partial UUID",
				providerID: "oxide://12345678-1234",
				errorMsg:   "provider id contains invalid uuid",
			},
			{
				name:       "provider ID with wrong prefix",
				providerID: "aws://12345678-1234-1234-1234-123456789abc",
				errorMsg:   "provider id does not have 'oxide://' prefix",
			},
		}

		for _, tc := range tt {
			t.Run(tc.name, func(t *testing.T) {
				_, err := InstanceIDFromProviderID(tc.providerID)
				if err == nil {
					t.Errorf("TestInstanceIDFromProviderID(%s) returned nil error, want non-nil error", tc.providerID)
				}

				if !strings.Contains(err.Error(), tc.errorMsg) {
					t.Errorf("TestInstanceIDFromProviderID(%s) returned error %v, want %s", tc.providerID, err.Error(), tc.errorMsg)
				}
			})
		}
	})
}

func TestNewProviderID(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		expected   string
	}{
		{
			name:       "valid instance ID",
			instanceID: "12345678-1234-1234-1234-123456789abc",
			expected:   "oxide://12345678-1234-1234-1234-123456789abc",
		},
		{
			name:       "empty instance ID",
			instanceID: "",
			expected:   "oxide://",
		},
		{
			name:       "instance ID with uppercase UUID",
			instanceID: "12345678-1234-1234-1234-123456789ABC",
			expected:   "oxide://12345678-1234-1234-1234-123456789ABC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewProviderID(tt.instanceID)
			if result != tt.expected {
				t.Errorf("NewProviderID(%s) = %s, want %s", tt.instanceID, result, tt.expected)
			}
		})
	}
}
