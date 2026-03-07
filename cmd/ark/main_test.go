package main

import (
	"reflect"
	"testing"
)

// Test: test-Config.md — reorderArgs puts flags before positional args
func TestReorderArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "flag after positional",
			in:   []string{"*.md", "--source", "/path"},
			want: []string{"--source", "/path", "*.md"},
		},
		{
			name: "flag already first",
			in:   []string{"--source", "/path", "*.md"},
			want: []string{"--source", "/path", "*.md"},
		},
		{
			name: "positional only",
			in:   []string{"*.md"},
			want: []string{"*.md"},
		},
		{
			name: "flag only",
			in:   []string{"--source", "/path"},
			want: []string{"--source", "/path"},
		},
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reorderArgs(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("reorderArgs(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
