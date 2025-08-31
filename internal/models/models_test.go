package models

import "testing"

func TestUser_GetImpersonationEnabled(t *testing.T) {
	tests := []struct {
		name     string
		user     *User
		expected bool
	}{
		{
			name: "nil pointer defaults to true",
			user: &User{
				ImpersonationEnabled: nil,
			},
			expected: true,
		},
		{
			name: "explicit true value",
			user: &User{
				ImpersonationEnabled: &[]bool{true}[0],
			},
			expected: true,
		},
		{
			name: "explicit false value",
			user: &User{
				ImpersonationEnabled: &[]bool{false}[0],
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.user.GetImpersonationEnabled()
			if got != tt.expected {
				t.Errorf("GetImpersonationEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}
