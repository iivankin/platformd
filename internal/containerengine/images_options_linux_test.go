//go:build linux && amd64 && cgo

package containerengine

import "testing"

func TestBuildSystemContextUsesBundledImageConfiguration(t *testing.T) {
	engine := &Engine{config: Config{
		RegistriesConf:  "/release/runtime/registries.conf",
		SignaturePolicy: "/release/runtime/policy.json",
	}}
	context := engine.buildSystemContext()
	if context.SystemRegistriesConfPath != engine.config.RegistriesConf {
		t.Fatalf("registries config = %q", context.SystemRegistriesConfPath)
	}
	if context.SignaturePolicyPath != engine.config.SignaturePolicy {
		t.Fatalf("signature policy = %q", context.SignaturePolicyPath)
	}
}
