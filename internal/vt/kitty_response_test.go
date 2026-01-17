package vt

import (
	"testing"
)

func TestBuildKittyResponse_OK(t *testing.T) {
	response := BuildKittyResponse(true, 0, "")
	expected := "\x1b_GOK\x1b\\"
	if string(response) != expected {
		t.Errorf("Expected %q, got %q", expected, string(response))
	}
}

func TestBuildKittyResponse_OKWithID(t *testing.T) {
	response := BuildKittyResponse(true, 31, "")
	expected := "\x1b_Gi=31;OK\x1b\\"
	if string(response) != expected {
		t.Errorf("Expected %q, got %q", expected, string(response))
	}
}

func TestBuildKittyResponse_Error(t *testing.T) {
	response := BuildKittyResponse(false, 0, "ENOENT:file not found")
	expected := "\x1b_GENOENT:file not found\x1b\\"
	if string(response) != expected {
		t.Errorf("Expected %q, got %q", expected, string(response))
	}
}

func TestBuildKittyResponse_ErrorWithID(t *testing.T) {
	response := BuildKittyResponse(false, 42, "EINVAL:invalid data")
	expected := "\x1b_Gi=42;EINVAL:invalid data\x1b\\"
	if string(response) != expected {
		t.Errorf("Expected %q, got %q", expected, string(response))
	}
}
