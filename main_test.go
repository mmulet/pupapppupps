package main

import (
	"testing"
)

func TestArgs(t *testing.T) {
	args := &Args{DisplayName: "test-display"}
	if args.WaylandDisplayName() != "test-display" {
		t.Errorf("Expected 'test-display', got '%s'", args.WaylandDisplayName())
	}
}

func TestCreateIcon(t *testing.T) {
	icon := createIcon()
	if len(icon) == 0 {
		t.Error("createIcon returned empty bytes")
	}
	// Check header for PNG
	if len(icon) < 8 {
		t.Error("Icon too small to be PNG")
	}
	// PNG signature
	expected := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	for i, b := range expected {
		if icon[i] != b {
			t.Errorf("Byte %d: expected %d, got %d", i, b, icon[i])
		}
	}
}
