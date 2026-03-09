package main

import (
	"strings"
	"testing"
)

func TestModuleVersionPrefersReplacement(t *testing.T) {
	module := goModule{Path: "example.com/mod", Version: "v1.0.0", Replace: &goModule{Path: "example.com/replaced", Version: "v2.0.0"}}
	if got := moduleVersion(module); got != "v2.0.0" {
		t.Fatalf("moduleVersion=%q, want v2.0.0", got)
	}
}

func TestModulePURL(t *testing.T) {
	module := goModule{Path: "github.com/kpumuk/thrift-weaver"}
	if got := modulePURL(module, "v1.2.3"); got != "pkg:golang/github.com/kpumuk/thrift-weaver@v1.2.3" {
		t.Fatalf("modulePURL=%q", got)
	}
}

func TestNewSerialNumber(t *testing.T) {
	serial := newSerialNumber()
	if !strings.HasPrefix(serial, "urn:uuid:") {
		t.Fatalf("serial=%q", serial)
	}
	if len(serial) != len("urn:uuid:00000000-0000-0000-0000-000000000000") {
		t.Fatalf("serial length=%d", len(serial))
	}
}
